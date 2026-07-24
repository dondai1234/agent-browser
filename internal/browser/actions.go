package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

// resolveBackendLocked resolves a backendNodeID to a remote object ID. Caller
// must hold s.mu.
func (s *Session) resolveBackendLocked(ctx context.Context, backend int64, ref string) (runtime.RemoteObjectID, error) {
	r, err := dom.ResolveNode().WithBackendNodeID(cdp.BackendNodeID(backend)).Do(ctx)
	if err != nil {
		return "", fmt.Errorf("resolve ref %q: %w", ref, err)
	}
	return r.ObjectID, nil
}

// tabURLLocked returns the current URL of the tab without rebuilding the tree.
// Caller must hold s.mu.
func (s *Session) tabURLLocked(t *tab) string {
	var u string
	_ = s.run(t, chromedp.Location(&u))
	return u
}

// NavigateAndSee navigates the current tab and returns its new tree. Atomic.
func (s *Session) NavigateAndSee(raw string) (*snapshot.Tree, error) {
	clean, err := ValidateURL(raw, s.AllowInsecureSchemes)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.navigateLocked(clean)
}

// navigateLocked is the lock-held core of NavigateAndSee (so a caller that
// already holds s.mu - e.g. Login - can navigate without a re-entrant lock
// deadlock). `clean` must already be a validated URL. Caller must hold s.mu.
func (s *Session) navigateLocked(clean string) (*snapshot.Tree, error) {
	if err := s.ensureBrowserLocked(); err != nil {
		return nil, err
	}
	t := s.curTabLocked()
	if t == nil {
		return nil, errors.New("no tab")
	}
	s.invalidateTabLocked(t)
	if err := s.run(t,
		chromedp.Navigate(clean),
		chromedp.WaitReady("body", chromedp.ByQuery),
	); err != nil {
		return nil, fmt.Errorf("navigate: %w", err)
	}
	if err := s.buildTreeLocked(); err != nil {
		return nil, err
	}
	cur := s.curTabLocked()
	cur.tree.Challenge = detectChallengeTitleURL(cur.tree.URL, cur.tree.Title)
	if cur.tree.Challenge != "" {
		// Managed challenge (Cloudflare/DataDome). With good stealth it often
		// auto-clears after a few seconds - wait for the real page before
		// surfacing the challenge to the agent.
		if s.waitForChallengeClearLocked(cur, 8*time.Second) {
			if err := s.buildTreeLocked(); err == nil {
				cur = s.curTabLocked()
				cur.tree.Challenge = detectChallengeTitleURL(cur.tree.URL, cur.tree.Title)
			}
		}
	}
	if cur.tree.Challenge == "" {
		if ch := s.detectChallengeDOMLocked(cur); ch != "" {
			cur.tree.Challenge = ch
		}
	}
	// Auto-dismiss a cookie/consent banner before orienting the agent. It's the
	// #1 real-world blocker (overlays the page, intercepts clicks, bloats the
	// AX tree). High-confidence only (OneTrust/Didomi/... + cookie-context
	// scoring) so a real dialog is never dismissed. Skips on a challenge page
	// (the verdict is the block, not the banner). A settle lets the removal
	// animation finish before the tree rebuild.
	overlayVerdict := ""
	if cur.tree.Challenge == "" {
		if label := s.dismissOverlaysLocked(cur); label != "" {
			overlayVerdict = label
			time.Sleep(overlaySettle)
			if err := s.buildTreeLocked(); err == nil {
				cur = s.curTabLocked()
				cur.tree.Challenge = detectChallengeTitleURL(cur.tree.URL, cur.tree.Title)
			}
			cur.tree.Overlay = overlayVerdict
		}
	}
	// Recover from consent redirects (cookie/GDPR/privacy pages). Some sites
	// (especially EU-targeted) redirect to a consent page on first visit. The
	// overlay dismissal above tries to click accept/reject, but the click may
	// not complete the consent flow (synthetic events, cross-domain cookies,
	// async redirects). If we detect a consent redirect:
	//   1. Wait for any post-click navigation (the dismiss may redirect back).
	//   2. If still on consent page, re-navigate to the original URL.
	//   3. If re-navigation also redirects, try a broader page-level dismiss.
	//   4. After broader dismiss, wait + re-navigate one final time.
	//   5. If still redirected, report it clearly so the agent can act.
	if cur.tree.Challenge == "" && isConsentRedirect(clean, cur.tree.URL) {
		// 1. Wait for post-click navigation.
		time.Sleep(500 * time.Millisecond)
		postURL := s.tabURLLocked(t)
		if postURL != "" && !isConsentRedirect(clean, postURL) && postURL != cur.tree.URL {
			// Dismiss triggered a redirect back (or elsewhere). Rebuild tree.
			if err := s.buildTreeLocked(); err == nil {
				cur = s.curTabLocked()
			}
		} else if isConsentRedirect(clean, postURL) || postURL == "" {
			// 2. Still on consent page. Re-navigate to the original URL.
			if err := s.run(t, chromedp.Navigate(clean), chromedp.WaitReady("body", chromedp.ByQuery)); err == nil {
				if err := s.buildTreeLocked(); err == nil {
					cur = s.curTabLocked()
				}
			}
			if isConsentRedirect(clean, cur.tree.URL) {
				// 3. Re-navigation still redirected. Try broader page-level dismiss.
				if label := s.dismissConsentPageLocked(cur); label != "" {
					if overlayVerdict != "" {
						overlayVerdict += "; " + label
					} else {
						overlayVerdict = label
					}
				}
				// 4. Wait for post-click navigation.
				time.Sleep(1 * time.Second)
				postURL2 := s.tabURLLocked(t)
				if postURL2 == "" || isConsentRedirect(clean, postURL2) {
					// 5. Final re-navigate.
					if err := s.run(t, chromedp.Navigate(clean), chromedp.WaitReady("body", chromedp.ByQuery)); err == nil {
						if err := s.buildTreeLocked(); err == nil {
							cur = s.curTabLocked()
						}
					}
				} else {
					// Broader dismiss triggered a redirect. Rebuild tree.
					if err := s.buildTreeLocked(); err == nil {
						cur = s.curTabLocked()
					}
				}
			}
		}
		if overlayVerdict != "" {
			cur.tree.Overlay = overlayVerdict
		}
	}
	navVerdict := fmt.Sprintf("navigated to %s", cur.tree.URL)
	if cur.tree.Title != "" {
		navVerdict += fmt.Sprintf(" %q", cur.tree.Title)
	}
	if cur.tree.Challenge != "" {
		navVerdict = "CHALLENGE: " + cur.tree.Challenge
	} else if isConsentRedirect(clean, cur.tree.URL) {
		navVerdict = fmt.Sprintf("CONSENT REDIRECT: %s -> %s (auto-dismiss did not complete the consent flow; try act intent=\"Accept\" to click the consent button manually, or js to set the consent cookie)", clean, cur.tree.URL)
		if overlayVerdict != "" {
			navVerdict += "; " + overlayVerdict
		}
	} else if overlayVerdict != "" {
		navVerdict += "; " + overlayVerdict
	}
	s.recordHistoryLocked(fmt.Sprintf("navigate %s", clean), navVerdict, cur.tree.URL)
	return cur.tree, nil
}

// mutateAndSee runs an action on the current tab, waits for the DOM to settle,
// re-builds the tree, and returns the delta + new tree. Atomic (holds s.mu).
func (s *Session) mutateAndSee(action string, settle time.Duration, do func(ctx context.Context) error) (*snapshot.Delta, *snapshot.Tree, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return nil, nil, ErrNoSnapshot
	}
	before := t.tree
	startTs := time.Now()
	if err := s.run(t, chromedp.ActionFunc(do)); err != nil {
		url := ""
		if before != nil {
			url = before.URL
		}
		s.recordHistoryLocked(action, "error: "+err.Error(), url)
		return nil, nil, err
	}
	if settle > 0 {
		time.Sleep(settle)
	}
	return s.finishMutationLocked(t, before, startTs, action)
}

// finishMutationLocked rebuilds the tree, computes the delta + verdict (and the
// net: summary for non-navigation actions), records the action in the session
// log, and returns the after-tree. Shared by mutateAndSee and Act so the verdict
// + history logic is identical across every action path. Caller must hold s.mu.
func (s *Session) finishMutationLocked(t *tab, before *snapshot.Tree, startTs time.Time, action string) (*snapshot.Delta, *snapshot.Tree, error) {
	if err := s.buildTreeFastLocked(); err != nil {
		// The action fired but the page is navigating/wedged and won't re-snapshot
		// in one pull (a click triggered a hanging nav, a challenge stalled, the
		// renderer tore down mid-load). That's not an action failure - return a
		// soft verdict so the agent knows the click/fill happened and calls see to
		// get fresh refs, instead of a ~16s hard error that reads like the click
		// failed. The before-tree's refs may be stale (the page changed), so after
		// is nil and deltaOut renders just the verdict line.
		url := ""
		if before != nil {
			url = before.URL
		}
		soft := &snapshot.Delta{Verdict: "action fired; page is loading or unreachable - call see to refresh refs", Confidence: "uncertain"}
		s.recordHistoryLocked(action, soft.Verdict, url)
		return soft, nil, nil
	}
	after := s.curTabLocked().tree
	d := snapshot.Diff(before, after)
	d.Verdict = d.InferVerdict()
	if after != nil && after.Challenge != "" {
		// A bot-check interstitial means the action didn't achieve its intent -
		// override the verdict so the agent sees the block first.
		d.Verdict = "CHALLENGE: " + after.Challenge
		d.Confidence = "uncertain"
	} else if !d.Navigated {
		// For non-navigation actions, fold in the XHR/Fetch responses that fired
		// during the action window - the "did it hit the API" signal. Skipped on
		// navigation (page load floods the buffer with asset noise) and on
		// challenges (the verdict is the block, not the network).
		evts := s.recentNetLocked(t, startTs)
		if len(evts) > 0 {
			d.Verdict += "; net: " + summarizeNet(evts)
		}
		// Confidence scoring: confirmed if DOM changed significantly or XHRs with
		// 2xx fired; likely if content shifted or any XHR fired; uncertain otherwise.
		has2xx := false
		for _, e := range evts {
			if e.status >= 200 && e.status < 300 {
				has2xx = true
				break
			}
		}
		switch {
		case d.HasChanges() || has2xx:
			d.Confidence = "confirmed"
		case d.ContentChanged || len(evts) > 0:
			d.Confidence = "likely"
		default:
			d.Confidence = "uncertain"
		}
	} else {
		// Navigation is always confirmed (the URL changed).
		d.Confidence = "confirmed"
	}
	// Self-Diagnosing Verdicts: when the action didn't clearly succeed, run a
	// lightweight DOM diagnostic (visible errors, HTML5 validation, CSS modals)
	// and append an actionable suggestion to the verdict. This eliminates the
	// investigation loop: the agent knows WHY the action failed and WHAT to do
	// next, without calling see/find/js. Skips on confirmed actions (zero
	// overhead for successful actions) and on challenges (the verdict is the
	// block, not the diagnostic).
	if d.Confidence != "confirmed" && d.Confidence != "" && !strings.HasPrefix(d.Verdict, "CHALLENGE:") {
		if diag := s.runActionDiagnosticsLocked(t); diag != "" {
			d.Verdict += "; " + diag
		}
	}
	url := ""
	if after != nil {
		url = after.URL
	}
	s.recordHistoryLocked(action, d.Verdict, url)
	return d, after, nil
}

// clickNodeLocked positions the real mouse over the element (scrollIntoView,
// getBoundingClientRect + iframe-offset via window.top, then mouseMoved) to set
// :hover/mouseover + make the action undetectable, then dispatches a synthetic
// click() for reliable handler triggering. Pure real-mouse press/release is
// flaky at firing React/SPA click handlers in headless Chrome; mouseMoved alone
// doesn't click, so there's no double-click.
func (s *Session) clickNodeLocked(ctx context.Context, id runtime.RemoteObjectID) error {
	res, exc, err := runtime.CallFunctionOn(
		"function() { try { this.scrollIntoView({block:'center'}); } catch(e) {} var r = this.getBoundingClientRect(); var x = r.left + r.width/2, y = r.top + r.height/2; var win = this.ownerDocument.defaultView; while (win !== window.top) { var f = win.frameElement; if (!f) break; var fr = f.getBoundingClientRect(); x += fr.left; y += fr.top; win = win.parent; } return [x, y]; }").
		WithReturnByValue(true).WithObjectID(id).Do(ctx)
	var center [2]float64
	if err == nil && exc == nil && res != nil && len(res.Value) > 0 && json.Unmarshal(res.Value, &center) == nil {
		if s.stealth {
			_ = moveMousePath(ctx, center[0], center[1])
		} else {
			_ = input.DispatchMouseEvent(input.MouseMoved, center[0], center[1]).Do(ctx)
		}
	}
	_, exc2, ferr := runtime.CallFunctionOn("function() { this.click(); }").WithObjectID(id).Do(ctx)
	if ferr != nil {
		return fmt.Errorf("click: %w", ferr)
	}
	if exc2 != nil {
		return fmt.Errorf("click failed: %s", exc2.Text)
	}
	return nil
}

// ClickAndSee clicks a ref (real mouse) and returns the delta + new tree.
func (s *Session) ClickAndSee(ref string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(fmt.Sprintf("click %s", ref), settle, func(ctx context.Context) error {
		id, err := s.resolveRefLocked(ctx, ref)
		if err != nil {
			return err
		}
		return s.clickNodeLocked(ctx, id)
	})
}

// FillAndSee sets an input value (dispatches input+change) and returns delta.
func (s *Session) FillAndSee(ref, value string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(fmt.Sprintf("fill %s =%q", ref, value), settle, func(ctx context.Context) error {
		id, err := s.resolveRefLocked(ctx, ref)
		if err != nil {
			return err
		}
		return s.fillNodeLocked(ctx, id, value)
	})
}

// SelectAndSee sets a <select> value and returns the delta.
func (s *Session) SelectAndSee(ref, value string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(fmt.Sprintf("select %s =%q", ref, value), settle, func(ctx context.Context) error {
		id, err := s.resolveRefLocked(ctx, ref)
		if err != nil {
			return err
		}
		return s.selectNodeLocked(ctx, id, value)
	})
}

// FillMany fills several inputs by ref in one call (e.g. a whole checkout form
// from extract form's refs), then re-snapshots once - one round-trip instead of
// N. Refs are filled in sorted order for determinism. Returns the combined
// delta + verdict like a single fill.
func (s *Session) FillMany(fields map[string]string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	if len(fields) == 0 {
		return nil, nil, errors.New("no fields to fill")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return nil, nil, ErrNoSnapshot
	}
	before := t.tree
	startTs := time.Now()
	refs := make([]string, 0, len(fields))
	for r := range fields {
		refs = append(refs, r)
	}
	sort.Strings(refs)
	filled := 0
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		for _, ref := range refs {
			id, e := s.resolveRefLocked(ctx, ref)
			if e != nil {
				return fmt.Errorf("fill ref %q: %w", ref, e)
			}
			if e := s.fillNodeLocked(ctx, id, fields[ref]); e != nil {
				return e
			}
			filled++
		}
		return nil
	})); err != nil {
		return nil, nil, err
	}
	if settle > 0 {
		time.Sleep(settle)
	}
	return s.finishMutationLocked(t, before, startTs, fmt.Sprintf("fill %d fields", filled))
}

// ScrollAndSee scrolls by dx/dy CSS pixels and returns the delta.
func (s *Session) ScrollAndSee(dx, dy int, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	delta, after, err := s.mutateAndSee(fmt.Sprintf("scroll %d %d", dx, dy), settle, func(ctx context.Context) error {
		_, exc, err := runtime.Evaluate(fmt.Sprintf("window.scrollBy(%d, %d)", dx, dy)).Do(ctx)
		if err != nil {
			return fmt.Errorf("scroll: %w", err)
		}
		if exc != nil {
			return fmt.Errorf("scroll failed: %s", exc.Text)
		}
		return nil
	})
	if err != nil {
		return delta, after, err
	}
	// Append the scroll position so the agent knows whether to keep scrolling
	// ("more below") or stop ("at bottom") - the loop-closer for lazy-loaded
	// lists. One cheap eval; re-lock briefly since mutateAndSee released.
	s.mu.Lock()
	delta.Verdict += "; scroll " + s.scrollInfoLocked(s.curTabLocked())
	s.mu.Unlock()
	return delta, after, nil
}

// scrollInfoLocked returns a compact scroll-position string: "fits viewport",
// "at bottom (Npx)", or "Y/Npx (more below)". Caller must hold s.mu.
func (s *Session) scrollInfoLocked(t *tab) string {
	if t == nil {
		return "?"
	}
	var pos [3]float64
	if err := s.run(t, chromedp.Evaluate(`[window.scrollY||0, window.innerHeight||0, (document.documentElement?document.documentElement.scrollHeight:0)||0]`, &pos)); err != nil {
		return "?"
	}
	y, vh, sh := pos[0], pos[1], pos[2]
	if sh <= vh {
		return "fits viewport"
	}
	if y+vh >= sh-2 {
		return fmt.Sprintf("at bottom (%.0fpx)", sh)
	}
	return fmt.Sprintf("%.0f/%.0fpx (more below)", y, sh)
}

// ScrollToAndSee scrolls an element by ref into view (block:center) and returns
// the delta + scroll position - for when the agent has a ref but it's off-screen
// and wants to read/screenshot it without guessing pixel deltas.
func (s *Session) ScrollToAndSee(ref string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	delta, after, err := s.mutateAndSee(fmt.Sprintf("scroll to %s", ref), settle, func(ctx context.Context) error {
		id, err := s.resolveRefLocked(ctx, ref)
		if err != nil {
			return err
		}
		_, exc, err := runtime.CallFunctionOn(`function() { try { this.scrollIntoView({block:'center'}); } catch(e) {} return true; }`).WithObjectID(id).Do(ctx)
		if err != nil {
			return fmt.Errorf("scroll to ref %q: %w", ref, err)
		}
		if exc != nil {
			return fmt.Errorf("scroll to ref %q failed: %s", ref, exc.Text)
		}
		return nil
	})
	if err != nil {
		return delta, after, err
	}
	s.mu.Lock()
	delta.Verdict += "; scroll " + s.scrollInfoLocked(s.curTabLocked())
	s.mu.Unlock()
	return delta, after, nil
}

// namedKeys maps named keys to code + Windows virtual key code. text is set
// only where a char should insert (Space); action keys (Enter, Escape, ...)// rely on the browser's native default action, which REAL CDP key events fire
// (synthetic JS KeyboardEvents do NOT - that's why press_key exists).
var namedKeys = map[string]struct {
	code, text string
	vk         int64
}{
	"Enter":      {"Enter", "\r", 13},
	"Escape":     {"Escape", "", 27},
	"Tab":        {"Tab", "\t", 9},
	"Backspace":  {"Backspace", "", 8},
	"Delete":     {"Delete", "", 46},
	"ArrowUp":    {"ArrowUp", "", 38},
	"ArrowDown":  {"ArrowDown", "", 40},
	"ArrowLeft":  {"ArrowLeft", "", 37},
	"ArrowRight": {"ArrowRight", "", 39},
	"Home":       {"Home", "", 36},
	"End":        {"End", "", 35},
	"PageUp":     {"PageUp", "", 33},
	"PageDown":   {"PageDown", "", 34},
	"Space":      {"Space", " ", 32},
}

// keyParams resolves a key string (named key or single char) into CDP fields.
func keyParams(key string) (k, code, text string, vk int64) {
	if info, ok := namedKeys[key]; ok {
		return key, info.code, info.text, info.vk
	}
	if r := []rune(key); len(r) == 1 {
		ch := string(r[0])
		upper := ch
		if len(ch) == 1 && ch[0] >= 'a' && ch[0] <= 'z' {
			upper = string(ch[0] - 32)
		}
		return ch, ch, ch, int64(upper[0])
	}
	return key, "", "", 0 // best-effort fallback
}

// parseModifiers parses a "ctrl+shift"-style string into CDP modifier flags.
func parseModifiers(mods string) input.Modifier {
	var m input.Modifier
	for _, part := range strings.Split(strings.ToLower(strings.TrimSpace(mods)), "+") {
		switch strings.TrimSpace(part) {
		case "ctrl", "control":
			m |= input.ModifierCtrl
		case "shift":
			m |= input.ModifierShift
		case "alt", "option":
			m |= input.ModifierAlt
		case "meta", "cmd", "command":
			m |= input.ModifierMeta
		}
	}
	return m
}

// validateKeyPress returns an error if key is not a named key or a single
// character. press_key takes ONE key, not a string of text; typing text is
// fill's job. Extracted so the rule is unit-testable without a browser. The
// agent failing this (e.g. press_key key="weather in tokyo") was a silent no-op
// before - the dispatched keyDown with a multi-char key string does nothing
// useful, and the agent can't tell.
func validateKeyPress(key string) error {
	if key == "" {
		return fmt.Errorf("press_key: key required (a named key like Enter/Escape/Tab, or a single character); to type text use fill, or act with a value")
	}
	if _, ok := namedKeys[key]; ok {
		return nil
	}
	if r := []rune(key); len(r) == 1 {
		return nil
	}
	return fmt.Errorf("press_key: %q is not a named key (Enter, Escape, Tab, ...) or a single character; to type text into a field use fill, or act with a value", key)
}

// PressKeyAndSee dispatches a real keyDown + keyUp (CDP Input.dispatchKeyEvent)
// on the focused element and returns the delta. Real key events fire native
// default actions (Enter submits, Escape closes, Tab moves focus, a char
// inserts) - synthetic JS KeyboardEvents do not. To TYPE TEXT into a field use
// fill (or act with a value), not press_key: press_key takes ONE named key or
// ONE character, not a string.
func (s *Session) PressKeyAndSee(ref, key, modifiers string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	if err := validateKeyPress(key); err != nil {
		return nil, nil, err
	}
	action := fmt.Sprintf("press_key %s", key)
	if ref != "" {
		action = fmt.Sprintf("press_key %s @%s", key, ref)
	}
	return s.mutateAndSee(action, settle, func(ctx context.Context) error {
		// Optional: focus a specific element first so the key lands on it (e.g.
		// press Enter on a chosen input to submit its form, without a separate
		// click/fill). Uses the native .focus(), which moves focus + fires focus.
		if ref != "" {
			id, err := s.resolveRefLocked(ctx, ref)
			if err != nil {
				return err
			}
			if _, exc, e := runtime.CallFunctionOn(`function(){ try { this.focus(); } catch(e){} return true; }`).WithObjectID(id).Do(ctx); e != nil {
				return fmt.Errorf("focus ref %q: %w", ref, e)
			} else if exc != nil {
				return fmt.Errorf("focus ref %q: %s", ref, exc.Text)
			}
		}
		k, code, text, vk := keyParams(key)
		mods := parseModifiers(modifiers)
		if err := input.DispatchKeyEvent(input.KeyDown).
			WithKey(k).WithCode(code).WithText(text).
			WithWindowsVirtualKeyCode(vk).WithModifiers(mods).Do(ctx); err != nil {
			return fmt.Errorf("press_key: %w", err)
		}
		if err := input.DispatchKeyEvent(input.KeyUp).
			WithKey(k).WithCode(code).
			WithWindowsVirtualKeyCode(vk).WithModifiers(mods).Do(ctx); err != nil {
			return fmt.Errorf("press_key up: %w", err)
		}
		return nil
	})
}

// hoverNodeLocked moves the real mouse to the element's viewport center (CDP
// Input.dispatchMouseEvent mouseMoved), with iframe-offset accumulation. Real
// mouse position triggers CSS :hover + JS mouseover/mouseenter - synthetic JS
// mouseover does NOT trigger CSS :hover.
func (s *Session) hoverNodeLocked(ctx context.Context, id runtime.RemoteObjectID) error {
	res, exc, err := runtime.CallFunctionOn(
		"function() { try { this.scrollIntoView({block:'center'}); } catch(e) {} var r = this.getBoundingClientRect(); var x = r.left + r.width/2, y = r.top + r.height/2; var win = this.ownerDocument.defaultView; while (win !== window.top) { var f = win.frameElement; if (!f) break; var fr = f.getBoundingClientRect(); x += fr.left; y += fr.top; win = win.parent; } return [x, y]; }").
		WithReturnByValue(true).WithObjectID(id).Do(ctx)
	var center [2]float64
	if err != nil || exc != nil || res == nil || len(res.Value) == 0 || json.Unmarshal(res.Value, &center) != nil {
		return fmt.Errorf("hover: could not locate element center (err=%v exc=%v)", err, exc)
	}
	return input.DispatchMouseEvent(input.MouseMoved, center[0], center[1]).Do(ctx)
}

// HoverAndSee hovers an element by ref and returns the delta (e.g. a hover menu
// appearing as Added elements).
func (s *Session) HoverAndSee(ref string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(fmt.Sprintf("hover %s", ref), settle, func(ctx context.Context) error {
		id, err := s.resolveRefLocked(ctx, ref)
		if err != nil {
			return err
		}
		return s.hoverNodeLocked(ctx, id)
	})
}

// Upload sets files on a file input. If ref is non-empty it targets that
// element; otherwise it auto-finds the first <input type=file> on the page
// (file inputs are usually absent from the a11y tree, so ref-less auto-find
// is the common path).
func (s *Session) Upload(ref string, paths []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return errors.New("no tab")
	}
	return s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		var objectID runtime.RemoteObjectID
		if ref != "" {
			id, err := s.resolveRefLocked(ctx, ref)
			if err != nil {
				return err
			}
			objectID = id
		} else {
			res, exc, err := runtime.Evaluate("document.querySelector('input[type=file]')").Do(ctx)
			if err != nil {
				return fmt.Errorf("find file input: %w", err)
			}
			if exc != nil {
				return fmt.Errorf("find file input failed: %s", exc.Text)
			}
			if res == nil || res.ObjectID == "" {
				return errors.New("no file input found on the page; pass a ref to target a specific element")
			}
			objectID = res.ObjectID
		}
		if err := dom.SetFileInputFiles(paths).WithObjectID(objectID).Do(ctx); err != nil {
			return fmt.Errorf("upload: %w", err)
		}
		return nil
	}))
}

// Screenshot captures the viewport (default), the full page (fullPage=true),
// or a specific element by ref (ref set, clipped to its bounding box). The ref
// path scrollIntoViews first, then clips a captureScreenshot to the element's
// iframe-offset-aware viewport box.
func (s *Session) Screenshot(fullPage bool, ref string) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return nil, errors.New("no tab")
	}
	if ref != "" {
		var buf []byte
		err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			id, e := s.resolveRefLocked(ctx, ref)
			if e != nil {
				return e
			}
			res, exc, e := runtime.CallFunctionOn(`function(){try{this.scrollIntoView({block:'center'})}catch(e){}var r=this.getBoundingClientRect();var win=this.ownerDocument.defaultView;var x=r.left,y=r.top;while(win!==window.top){var f=win.frameElement;if(!f)break;var fr=f.getBoundingClientRect();x+=fr.left;y+=fr.top;win=win.parent}return [x,y,r.width,r.height]}`).WithReturnByValue(true).WithObjectID(id).Do(ctx)
			if e != nil {
				return fmt.Errorf("screenshot ref %q: %w", ref, e)
			}
			if exc != nil {
				return fmt.Errorf("screenshot ref %q: %s", ref, exc.Text)
			}
			var box [4]float64
			if res == nil || json.Unmarshal(res.Value, &box) != nil {
				return fmt.Errorf("screenshot ref %q: could not read bounds", ref)
			}
			b, e := page.CaptureScreenshot().WithClip(&page.Viewport{X: box[0], Y: box[1], Width: box[2], Height: box[3], Scale: 1}).Do(ctx)
			if e != nil {
				return fmt.Errorf("screenshot ref %q: %w", ref, e)
			}
			buf = b
			return nil
		}))
		if err != nil {
			return nil, err
		}
		return buf, nil
	}
	var buf []byte
	if fullPage {
		if err := s.run(t, chromedp.FullScreenshot(&buf, 90)); err != nil {
			return nil, fmt.Errorf("screenshot full: %w", err)
		}
		return buf, nil
	}
	if err := s.run(t, chromedp.CaptureScreenshot(&buf)); err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	return buf, nil
}

// Wait blocks up to d, returning early once a condition is met. One of:
// url (the page URL contains it - e.g. wait for a redirect to /dashboard),
// text (the body text contains it), or gone (the body text no longer contains
// it - e.g. a "Loading..." spinner disappearing). If none are set, just sleeps.
// Returns a short outcome string, or an error on timeout.
func (s *Session) Wait(d time.Duration, text, url, gone string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return "", errors.New("no tab")
	}
	if url == "" && text == "" && gone == "" {
		if d > 0 {
			time.Sleep(d)
		}
		return fmt.Sprintf("waited %s", d), nil
	}
	if d <= 0 {
		// A condition was set but no seconds - default so it isn't an instant
		// timeout (a common agent slip: wait url=/dashboard with seconds=0).
		d = 10 * time.Second
	}
	deadline := time.Now().Add(d)
	for {
		if url != "" {
			var loc string
			if err := s.run(t, chromedp.Location(&loc)); err != nil {
				return "", fmt.Errorf("wait url: %w", err)
			}
			if strings.Contains(loc, url) {
				return fmt.Sprintf("url matched: %s", loc), nil
			}
		} else {
			var inner string
			if err := s.run(t, chromedp.Evaluate("document.body?document.body.innerText:''", &inner)); err != nil {
				return "", fmt.Errorf("wait: %w", err)
			}
			switch {
			case gone != "" && !strings.Contains(inner, gone):
				return fmt.Sprintf("%q gone", gone), nil
			case text != "" && strings.Contains(inner, text):
				return fmt.Sprintf("%q present", text), nil
			}
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	which := fmt.Sprintf("text %q", text)
	if url != "" {
		which = fmt.Sprintf("url contains %q", url)
	} else if gone != "" {
		which = fmt.Sprintf("%q gone", gone)
	}
	return "", fmt.Errorf("wait: %s not satisfied within %s", which, d)
}

// Read returns url + title, plus a ref's text (if ref given) or body text.
// For the body (no ref), offset skips that many chars (pagination).
func (s *Session) Read(ref string, offset int) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return "", ErrNoSnapshot
	}
	var b strings.Builder
	fmt.Fprintf(&b, "url: %s\ntitle: %s\n", t.tree.URL, t.tree.Title)

	if ref != "" {
		var text string
		err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			id, e := s.resolveRefLocked(ctx, ref)
			if e != nil {
				return e
			}
			res, exc, e := runtime.CallFunctionOn(`function() { var t = this.innerText || this.textContent || ''; if (this.tagName === 'A' && this.href) t += '\nhref: ' + this.href; return t; }`).
				WithObjectID(id).Do(ctx)
			if e != nil {
				return fmt.Errorf("read ref %q: %w", ref, e)
			}
			if exc != nil {
				return fmt.Errorf("read ref %q failed: %s", ref, exc.Text)
			}
			if res != nil && len(res.Value) > 0 {
				var v string
				if json.Unmarshal(res.Value, &v) == nil {
					text = v
				}
			}
			return nil
		}))
		if err != nil {
			return "", err
		}
		fmt.Fprintf(&b, "ref %s text: %s\n", ref, truncate(text, 8000))
		return b.String(), nil
	}

	var body string
	if err := s.run(t, chromedp.Evaluate(readBodyJS, &body)); err == nil {
		if offset > 0 {
			if offset < len(body) {
				body = body[offset:]
			} else {
				body = ""
			}
		}
		fmt.Fprintf(&b, "body: %s\n", truncate(body, 8000))
	}
	return b.String(), nil
}

// readBodyJS returns the page's visible text, walking same-origin iframes
// (innerText does not pierce iframes on its own).
const readBodyJS = `(function(){
  var out = [];
  if (document.body) out.push(document.body.innerText);
  var ifs = document.querySelectorAll('iframe');
  for (var i = 0; i < ifs.length; i++) {
    try {
      var d = ifs[i].contentDocument;
      if (d && d.body) out.push('[' + (ifs[i].title || ('iframe' + i)) + ']\n' + d.body.innerText);
    } catch (e) {}
  }
  return out.join('\n\n');
})()`

// Eval runs arbitrary JS in the current tab and returns the raw JSON result.
// Gated by AllowEval (on by default; operator can disable with --no-eval).
func (s *Session) Eval(script string) (string, error) {
	if !s.AllowEval {
		return "", errors.New("eval disabled: the server was started with --no-eval")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return "", errors.New("no tab")
	}
	var out string
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		res, exc, err := runtime.Evaluate(script).WithReturnByValue(true).Do(ctx)
		if err != nil {
			return fmt.Errorf("eval: %w", err)
		}
		if exc != nil {
			return fmt.Errorf("eval failed: %s", exc.Text)
		}
		if res != nil && len(res.Value) > 0 {
			out = maybeUnquoteJSONString(res.Value)
		}
		return nil
	})); err != nil {
		return "", err
	}
	return out, nil
}

// maybeUnquoteJSONString returns the string value if b is a JSON string literal
// (so eval "document.title" returns Title, not "Title"), else the raw bytes
// (objects/numbers/bools stay as their JSON). Objects are NOT touched: an
// eval returning {"a":1} keeps its braces.
func maybeUnquoteJSONString(b []byte) string {
	if len(b) >= 2 && b[0] == '"' && b[len(b)-1] == '"' {
		var s string
		if err := json.Unmarshal(b, &s); err == nil {
			return s
		}
	}
	return string(b)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return fmt.Sprintf("%s...(truncated; %d chars total)", s[:n], len(s))
}
