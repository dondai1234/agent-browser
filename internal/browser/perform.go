package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

// PerformArgs is the full input to the unified `act` tool. Exactly one action
// category is set (default click/fill/select, hover, key press, or upload); the
// target is one of intent (name) / ref / selector (or none for a bare key press
// on the focused element / upload auto-find of the file input). An optional wait
// condition fuses "act, then wait for the result" into one call so the delta
// reflects the post-wait page (the common click-then-wait-for-redirect case).
type PerformArgs struct {
	Intent    string // control name (a11y + DOM-attribute fallback)
	Ref       string // stable ref from see/find
	Selector  string // CSS selector (escape hatch for a11y-invisible elements)
	Value     string // fill/select value (for inputs/dropdowns)
	Role      string // constrain intent matches to a role
	Nth       int    // disambiguate ambiguous intent matches
	Hover     bool   // hover instead of click
	Key       string // press a key (named key or single char)
	Modifiers string // key modifiers: ctrl, shift, alt, meta (+-joined)
	Files     []string
	WaitURL   string // after the action, wait for URL to contain this
	WaitText  string // ...wait for body text to contain this
	WaitGone  string // ...wait for body text to no longer contain this
	WaitMs    int    // wait budget (default 10000)
	SettleMs  int    // DOM settle before re-snapshot (default 150)
}

// PerformResult is the outcome. For an ambiguous intent, Resolved is nil and
// Candidates/CandidatesText carry the ranked matches. For a successful action,
// Delta + After carry the verdict + post-action tree.
type PerformResult struct {
	Verb           string
	Resolved       *snapshot.Element
	Target         string // human label for the non-intent paths ("ref r3", "selector .x", "focused", "auto file input")
	Candidates     []snapshot.Element
	CandidatesText string
	Delta          *snapshot.Delta
	After          *snapshot.Tree
}

// actOnElementLocked performs the role-appropriate action (click/fill/select) on
// a resolved remote object id, picking the verb from the element's role + whether
// a value was supplied. Shared by the intent, ref, and selector paths so the
// "combobox might be a <select> OR an ARIA combobox over an input" probe + the
// fillable-needs-value guard live in one place. Caller must hold s.mu.
func (s *Session) actOnElementLocked(ctx context.Context, id runtime.RemoteObjectID, role, value, ref string) (string, error) {
	hasValue := strings.TrimSpace(value) != ""
	switch {
	case isFillableRole(role):
		if !hasValue {
			return "fill", fmt.Errorf("resolved [%s] is a %s; pass value= to fill it (or name a clickable control)", ref, role)
		}
		return "fill", s.fillNodeLocked(ctx, id, value)
	case role == "combobox" && hasValue:
		// A combobox is one of three things, picked by probing the tag/type:
		//   1. native <select>        -> selectJS (set the option)
		//   2. text input/textarea     -> fill (autocomplete: React/Vue hear input/change)
		//   3. button/div + listbox    -> open-select dance (open popup, click option)
		var tagType string
		if res, _, e := runtime.CallFunctionOn(`function(){return this.tagName + '/' + (this.type||''); }`).WithObjectID(id).Do(ctx); e == nil && res != nil && len(res.Value) > 0 {
			_ = json.Unmarshal(res.Value, &tagType)
		}
		tag := strings.SplitN(tagType, "/", 2)[0]
		switch {
		case tag == "SELECT":
			return "select", s.selectNodeLocked(ctx, id, value)
		case tag == "INPUT" || tag == "TEXTAREA":
			return "fill", s.fillNodeLocked(ctx, id, value)
		default:
			if _, e := s.openSelectByIDLocked(ctx, id, value); e != nil {
				return "open-select", e
			}
			return "open-select", nil
		}
	case role == "combobox":
		return "select", fmt.Errorf("resolved [%s] is a combobox; pass value= to select an option", ref)
	default:
		// value= on a non-fillable/non-combobox: if it's a listbox-combobox the AX
		// tree reported as a plain button (aria-haspopup=listbox, no role=combobox),
		// open-select; otherwise just click (the value is ignored - a misuse, but
		// the click is the safe default, never a guess).
		if hasValue {
			var hp string
			if res, _, e := runtime.CallFunctionOn(`function(){return this.getAttribute('aria-haspopup')||''; }`).WithObjectID(id).Do(ctx); e == nil && res != nil && len(res.Value) > 0 {
				_ = json.Unmarshal(res.Value, &hp)
			}
			if strings.Contains(strings.ToLower(hp), "listbox") {
				if _, e := s.openSelectByIDLocked(ctx, id, value); e != nil {
					return "open-select", e
				}
				return "open-select", nil
			}
		}
		return "click", s.clickNodeLocked(ctx, id)
	}
}

// resolveAndActLocked is the intent path core: resolve `intent` on the cached
// tree (a11y name first, DOM-attribute fallback on no-match), then perform the
// role-appropriate action (click/fill/select, or hover when hover=true). Returns
// the verb + resolved element (or ranked candidates when ambiguous). Does NOT
// re-snapshot - the caller finishes the mutation. Caller must hold s.mu.
func (s *Session) resolveAndActLocked(ctx context.Context, t *tab, intent, value, role string, nth int, hover bool) (verb string, resolved *snapshot.Element, candidates []snapshot.Element, candText string, err error) {
	resolvedEl, cands, rerr := resolveIntent(t.tree, intent, value, role, nth)
	if rerr != nil {
		if len(cands) == 0 {
			// No a11y-name match - DOM-attribute fallback (name/id/placeholder/
			// title/aria-label) for poorly-labeled inputs. Hover has no DOM fallback
			// (hover targets are visible labeled controls); click/fill/select do.
			if hover {
				return "", nil, nil, "", rerr
			}
			domCand, domCands, domErr := s.resolveIntentDOMLocked(t, intent, value, role, nth)
			switch {
			case domErr == nil:
				v, actErr := s.actOnDOMLocked(t, domCand, value)
				if actErr != nil {
					return v, nil, nil, "", actErr
				}
				return v, &snapshot.Element{Ref: "dom", Role: domRoleFor(domCand), Name: domCand.Val}, nil, "", nil
			case errors.Is(domErr, errDOMNoMatch):
				// fall through to the a11y no-match error
			case len(domCands) > 0:
				return "", nil, nil, renderDOMCandidates(domCands), domErr
			}
		}
		return "", nil, cands, "", rerr
	}
	// Role-appropriateness check: if the agent passed a VALUE it wants to FILL,
	// but the a11y match is a clickable (a "Search" link matched before the
	// search input did, or the input had no a11y name), try the DOM fallback for
	// a fillable element and prefer it. This is the fix for intent targeting
	// landing on the wrong element and forcing a selector fallback. Skipped when
	// hover, when nth picked explicitly, or when the a11y match is already
	// fillable/combobox (the clean case - no extra CDP call). Also skipped for a
	// listbox-combobox (a button with aria-haspopup=listbox): value= there means
	// SELECT an option via open-click, not fill a text input.
	hasValue := strings.TrimSpace(value) != ""
	if hasValue && !hover && nth == 0 && !isFillableRole(resolvedEl.Role) && resolvedEl.Role != "combobox" && !isListboxCombobox(resolvedEl) {
		if domCand, _, domErr := s.resolveIntentDOMLocked(t, intent, value, role, 0); domErr == nil && isFillableDomCand(domCand) {
			v, actErr := s.actOnDOMLocked(t, domCand, value)
			if actErr != nil {
				return v, &resolvedEl, nil, "", actErr
			}
			return v, &snapshot.Element{Ref: "dom", Role: domRoleFor(domCand), Name: domCand.Val}, nil, "", nil
		}
	}
	id, err := s.resolveRefLocked(ctx, resolvedEl.Ref)
	if err != nil {
		return "", &resolvedEl, nil, "", err
	}
	if hover {
		return "hover", &resolvedEl, nil, "", s.hoverNodeLocked(ctx, id)
	}
	v, actErr := s.actOnElementLocked(ctx, id, resolvedEl.Role, value, resolvedEl.Ref)
	return v, &resolvedEl, nil, "", actErr
}

// isFillableDomCand reports whether a DOM-fallback candidate is a text-like
// input/textarea/select (i.e. act would FILL it, not click it). Used by the
// role-appropriateness check so a value-bearing intent prefers a fillable DOM
// match over a clickable a11y match.
func isFillableDomCand(c domCandidate) bool {
	return c.Tag == "textarea" || c.Tag == "select" || (c.Tag == "input" && isTextInputType(c.Type))
}

// selectorObjectIDLocked resolves a CSS selector on the top document to a remote
// object id. Returns an error if no element matches (the page may have
// re-rendered). Caller must hold s.mu.
func (s *Session) selectorObjectIDLocked(ctx context.Context, sel string) (runtime.RemoteObjectID, error) {
	selJSON, _ := json.Marshal(sel)
	res, exc, err := runtime.Evaluate(`(function(){ return document.querySelector(` + string(selJSON) + `); })()`).Do(ctx)
	if err != nil {
		return "", fmt.Errorf("selector resolve: %w", err)
	}
	if exc != nil {
		return "", fmt.Errorf("selector resolve failed: %s", exc.Text)
	}
	if res == nil || res.ObjectID == "" {
		return "", fmt.Errorf("no element matches selector %q; the page may have re-rendered - call see, then retry", sel)
	}
	return res.ObjectID, nil
}

// waitCondLocked polls a url/text/gone condition up to ms, returning nil once
// satisfied. Used by Perform to fuse act+wait: the re-snapshot happens after the
// wait, so the delta reflects the post-wait page. Caller must hold s.mu.
func (s *Session) waitCondLocked(t *tab, url, text, gone string, ms int) error {
	if url == "" && text == "" && gone == "" {
		return nil
	}
	if ms <= 0 {
		ms = 10000
	}
	deadline := time.Now().Add(time.Duration(ms) * time.Millisecond)
	for {
		if url != "" {
			var loc string
			if err := s.runTimeout(t, 5*time.Second, chromedp.Location(&loc)); err == nil && strings.Contains(loc, url) {
				return nil
			}
		} else {
			var inner string
			if err := s.runTimeout(t, 5*time.Second, chromedp.Evaluate("document.body?document.body.innerText:''", &inner)); err == nil {
				if gone != "" && !strings.Contains(inner, gone) {
					return nil
				}
				if text != "" && strings.Contains(inner, text) {
					return nil
				}
			}
		}
		if !time.Now().Before(deadline) {
			which := fmt.Sprintf("text %q", text)
			if url != "" {
				which = fmt.Sprintf("url contains %q", url)
			} else if gone != "" {
				which = fmt.Sprintf("%q gone", gone)
			}
			return fmt.Errorf("wait: %s not satisfied within %dms", which, ms)
		}
		time.Sleep(150 * time.Millisecond)
	}
}

// Perform is the unified `act`: one method for every single-action case, with
// optional wait-fusing. See PerformArgs for the contract. Atomic (one lock for
// resolve + act + wait + re-snapshot). Returns a verdict + delta; on navigation
// the verdict says "navigated to <url>" and After carries the new orientation.
func (s *Session) Perform(a PerformArgs) (*PerformResult, error) {
	a.Intent = strings.TrimSpace(a.Intent)
	a.Selector = strings.TrimSpace(a.Selector)
	a.Ref = strings.TrimSpace(a.Ref)
	a.Key = strings.TrimSpace(a.Key)
	settle := settleDur(a.SettleMs)

	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return nil, ErrNoSnapshot
	}
	before := t.tree
	startTs := time.Now()

	// Category 1: key press. Optional focus target (ref/intent; selector not
	// supported - use ref/intent to target a key press).
	if a.Key != "" {
		if err := validateKeyPress(a.Key); err != nil {
			return &PerformResult{}, err
		}
		action := "press_key " + a.Key
		var actErr error
		if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			if a.Ref != "" || a.Intent != "" {
				var id runtime.RemoteObjectID
				var el *snapshot.Element
				var e error
				if a.Ref != "" {
					id, e = s.resolveRefLocked(ctx, a.Ref)
				} else {
					el2, cands, rerr := resolveIntent(t.tree, a.Intent, "", a.Role, a.Nth)
					if rerr != nil {
						if len(cands) > 0 {
							actErr = rerr
							return nil // candidates surfaced below
						}
						return rerr
					}
					el = &el2
					id, e = s.resolveRefLocked(ctx, el.Ref)
				}
				if e != nil {
					return e
				}
				if _, exc, fe := runtime.CallFunctionOn(`function(){ try { this.focus(); } catch(e){} return true; }`).WithObjectID(id).Do(ctx); fe != nil {
					return fmt.Errorf("focus: %w", fe)
				} else if exc != nil {
					return fmt.Errorf("focus failed: %s", exc.Text)
				}
			}
			k, code, txt, vk := keyParams(a.Key)
			mods := parseModifiers(a.Modifiers)
			if err := input.DispatchKeyEvent(input.KeyDown).WithKey(k).WithCode(code).WithText(txt).WithWindowsVirtualKeyCode(vk).WithModifiers(mods).Do(ctx); err != nil {
				return fmt.Errorf("press_key: %w", err)
			}
			if err := input.DispatchKeyEvent(input.KeyUp).WithKey(k).WithCode(code).WithWindowsVirtualKeyCode(vk).WithModifiers(mods).Do(ctx); err != nil {
				return fmt.Errorf("press_key up: %w", err)
			}
			return nil
		})); err != nil {
			s.recordActionErrorLocked(before, action, err)
			return &PerformResult{Verb: "press", Candidates: nil}, err
		}
		if actErr != nil {
			// ambiguous intent for the focus target - surface candidates
			el, cands, _ := resolveIntent(t.tree, a.Intent, "", a.Role, a.Nth)
			_ = el
			res := &PerformResult{Verb: "press", Candidates: cands}
			s.recordActionErrorLocked(before, action, actErr)
			return res, actErr
		}
		if settle > 0 {
			time.Sleep(settle)
		}
		if err := s.waitCondLocked(t, a.WaitURL, a.WaitText, a.WaitGone, a.WaitMs); err != nil {
			s.recordActionErrorLocked(before, action, err)
			return &PerformResult{Verb: "press"}, err
		}
		delta, after, ferr := s.finishMutationLocked(t, before, startTs, action)
		if ferr != nil {
			return &PerformResult{Verb: "press"}, ferr
		}
		return &PerformResult{Verb: "press", Target: keyPressTarget(a), Delta: delta, After: after}, nil
	}

	// Category 2: upload. Target: ref | intent | selector | auto-find file input.
	if len(a.Files) > 0 {
		action := fmt.Sprintf("upload %d file(s)", len(a.Files))
		if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			var objectID runtime.RemoteObjectID
			switch {
			case a.Ref != "":
				id, e := s.resolveRefLocked(ctx, a.Ref)
				if e != nil {
					return e
				}
				objectID = id
			case a.Intent != "":
				el, cands, rerr := resolveIntent(t.tree, a.Intent, "", a.Role, a.Nth)
				if rerr != nil {
					if len(cands) > 0 {
						return rerr
					}
					return rerr
				}
				id, e := s.resolveRefLocked(ctx, el.Ref)
				if e != nil {
					return e
				}
				objectID = id
			case a.Selector != "":
				id, e := s.selectorObjectIDLocked(ctx, a.Selector)
				if e != nil {
					return e
				}
				objectID = id
			default:
				res, exc, e := runtime.Evaluate("document.querySelector('input[type=file]')").Do(ctx)
				if e != nil {
					return fmt.Errorf("find file input: %w", e)
				}
				if exc != nil {
					return fmt.Errorf("find file input failed: %s", exc.Text)
				}
				if res == nil || res.ObjectID == "" {
					return errors.New("no file input found on the page; pass ref/selector/intent to target one")
				}
				objectID = res.ObjectID
			}
			return dom.SetFileInputFiles(a.Files).WithObjectID(objectID).Do(ctx)
		})); err != nil {
			s.recordActionErrorLocked(before, action, err)
			return &PerformResult{Verb: "upload"}, err
		}
		if settle > 0 {
			time.Sleep(settle)
		}
		_ = s.waitCondLocked(t, a.WaitURL, a.WaitText, a.WaitGone, a.WaitMs)
		delta, after, ferr := s.finishMutationLocked(t, before, startTs, action)
		if ferr != nil {
			return &PerformResult{Verb: "upload"}, ferr
		}
		return &PerformResult{Verb: "upload", Delta: delta, After: after}, nil
	}

	// Category 3+4: hover or default (click/fill/select). Target: intent | ref | selector.
	action := ""
	var verb string
	var resolved *snapshot.Element
	var candidates []snapshot.Element
	var candText string
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		switch {
		case a.Intent != "":
			v, r, cands, ct, e := s.resolveAndActLocked(ctx, t, a.Intent, a.Value, a.Role, a.Nth, a.Hover)
			verb, resolved, candidates, candText, _ = v, r, cands, ct, e
			return e
		case a.Ref != "":
			el, ok := t.tree.ByRef(a.Ref)
			if !ok {
				return fmt.Errorf("ref %q not found; refs may be stale after navigation - call see again", a.Ref)
			}
			id, e := s.resolveRefLocked(ctx, a.Ref)
			if e != nil {
				return e
			}
			resolved = &el
			if a.Hover {
				verb = "hover"
				return s.hoverNodeLocked(ctx, id)
			}
			v, actErr := s.actOnElementLocked(ctx, id, el.Role, a.Value, a.Ref)
			verb = v
			return actErr
		case a.Selector != "":
			id, e := s.selectorObjectIDLocked(ctx, a.Selector)
			if e != nil {
				return e
			}
			if a.Hover {
				verb = "hover"
				return s.hoverNodeLocked(ctx, id)
			}
			// Selector path: auto-detect tag/type (the element may not be in the
			// a11y tree, so we can't trust a role - read the tag/type from the DOM).
			v, actErr := s.actBySelectorLocked(ctx, a.Selector, a.Value, "auto")
			verb = v
			return actErr
		default:
			return errors.New("act needs a target: intent (control name), ref, or selector")
		}
	})); err != nil {
		// Ambiguous (candidates present) or no-match or fillable-needs-value:
		// surface the message + the candidate list so the agent disambiguates
		// without a separate find.
		action = performActionLabel(a)
		s.recordActionErrorLocked(before, action, err)
		res := &PerformResult{Verb: verb, Resolved: resolved, Candidates: candidates, CandidatesText: candText}
		return res, err
	}
	action = performActionLabel(a)
	if settle > 0 {
		time.Sleep(settle)
	}
	if err := s.waitCondLocked(t, a.WaitURL, a.WaitText, a.WaitGone, a.WaitMs); err != nil {
		s.recordActionErrorLocked(before, action, err)
		return &PerformResult{Verb: verb, Resolved: resolved}, err
	}
	delta, after, ferr := s.finishMutationLocked(t, before, startTs, action)
	if ferr != nil {
		return &PerformResult{Verb: verb, Resolved: resolved}, ferr
	}
	return &PerformResult{Verb: verb, Resolved: resolved, Delta: delta, After: after}, nil
}

// performActionLabel builds the history-log label for a default/hover perform.
func performActionLabel(a PerformArgs) string {
	switch {
	case a.Hover:
		return fmt.Sprintf("hover %s", performTargetLabel(a))
	case a.Value != "":
		return fmt.Sprintf("act %s =%q", performTargetLabel(a), a.Value)
	default:
		return fmt.Sprintf("act %s", performTargetLabel(a))
	}
}

// performTargetLabel is the short target description for the action log + result.
func performTargetLabel(a PerformArgs) string {
	switch {
	case a.Intent != "":
		return fmt.Sprintf("%q", a.Intent)
	case a.Ref != "":
		return a.Ref
	case a.Selector != "":
		return fmt.Sprintf("selector %q", a.Selector)
	}
	return "?"
}

// keyPressTarget is the result Target label for a key press.
func keyPressTarget(a PerformArgs) string {
	switch {
	case a.Ref != "":
		return a.Ref
	case a.Intent != "":
		return fmt.Sprintf("%q", a.Intent)
	}
	return "focused"
}
