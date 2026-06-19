package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/input"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/dondai1234/agent-browser/internal/snapshot"
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

// NavigateAndSee navigates the current tab and returns its new tree. Atomic.
func (s *Session) NavigateAndSee(raw string) (*snapshot.Tree, error) {
	clean, err := ValidateURL(raw, s.AllowInsecureSchemes)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return nil, errors.New("no tab")
	}
	t.tree = nil
	if err := chromedp.Run(t.ctx,
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
	return cur.tree, nil
}

// mutateAndSee runs an action on the current tab, waits for the DOM to settle,
// re-builds the tree, and returns the delta + new tree. Atomic (holds s.mu).
func (s *Session) mutateAndSee(settle time.Duration, do func(ctx context.Context) error) (*snapshot.Delta, *snapshot.Tree, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return nil, nil, ErrNoSnapshot
	}
	before := t.tree
	if err := chromedp.Run(t.ctx, chromedp.ActionFunc(do)); err != nil {
		return nil, nil, err
	}
	if settle > 0 {
		time.Sleep(settle)
	}
	if err := s.buildTreeLocked(); err != nil {
		return nil, nil, err
	}
	after := s.curTabLocked().tree
	return snapshot.Diff(before, after), after, nil
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
	return s.mutateAndSee(settle, func(ctx context.Context) error {
		id, err := s.resolveRefLocked(ctx, ref)
		if err != nil {
			return err
		}
		return s.clickNodeLocked(ctx, id)
	})
}

// FillAndSee sets an input value (dispatches input+change) and returns delta.
func (s *Session) FillAndSee(ref, value string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(settle, func(ctx context.Context) error {
		id, err := s.resolveRefLocked(ctx, ref)
		if err != nil {
			return err
		}
		arg, _ := json.Marshal(value)
		_, exc, err := runtime.CallFunctionOn(
			"function(v) { try { this.focus(); } catch(e) {} var proto = this.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype; var setter = Object.getOwnPropertyDescriptor(proto, 'value').set; setter.call(this, v); this.dispatchEvent(new Event('input',{bubbles:true})); this.dispatchEvent(new Event('change',{bubbles:true})); return this.value; }").
			WithObjectID(id).
			WithArguments([]*runtime.CallArgument{{Value: jsontext.Value(arg)}}).
			Do(ctx)
		if err != nil {
			return fmt.Errorf("fill ref %q: %w", ref, err)
		}
		if exc != nil {
			return fmt.Errorf("fill ref %q failed: %s", ref, exc.Text)
		}
		return nil
	})
}

// SelectAndSee sets a <select> value and returns the delta.
func (s *Session) SelectAndSee(ref, value string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(settle, func(ctx context.Context) error {
		id, err := s.resolveRefLocked(ctx, ref)
		if err != nil {
			return err
		}
		arg, _ := json.Marshal(value)
		_, exc, err := runtime.CallFunctionOn(
			"function(v) { var opts=this.options, m=null; for(var i=0;i<opts.length;i++){ if(opts[i].value===v||opts[i].text===v||opts[i].textContent===v){m=opts[i];break;} } if(!m){ for(var i=0;i<opts.length;i++){ if(opts[i].textContent.indexOf(v)>=0){m=opts[i];break;} } } if(!m) return this.value; var setter=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set; setter.call(this,m.value); this.dispatchEvent(new Event('change',{bubbles:true})); return this.value; }").
			WithObjectID(id).
			WithArguments([]*runtime.CallArgument{{Value: jsontext.Value(arg)}}).
			Do(ctx)
		if err != nil {
			return fmt.Errorf("select ref %q: %w", ref, err)
		}
		if exc != nil {
			return fmt.Errorf("select ref %q failed: %s", ref, exc.Text)
		}
		return nil
	})
}

// ScrollAndSee scrolls by dx/dy CSS pixels and returns the delta.
func (s *Session) ScrollAndSee(dx, dy int, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(settle, func(ctx context.Context) error {
		_, exc, err := runtime.Evaluate(fmt.Sprintf("window.scrollBy(%d, %d)", dx, dy)).Do(ctx)
		if err != nil {
			return fmt.Errorf("scroll: %w", err)
		}
		if exc != nil {
			return fmt.Errorf("scroll failed: %s", exc.Text)
		}
		return nil
	})
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

// PressKeyAndSee dispatches a real keyDown + keyUp (CDP Input.dispatchKeyEvent)
// on the focused element and returns the delta. Real key events fire native
// default actions (Enter submits, Escape closes, Tab moves focus, a char
// inserts) - synthetic JS KeyboardEvents do not.
func (s *Session) PressKeyAndSee(key, modifiers string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(settle, func(ctx context.Context) error {
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
	return s.mutateAndSee(settle, func(ctx context.Context) error {
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
	return chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
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

// Screenshot captures the current tab's viewport as a PNG.
func (s *Session) Screenshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return nil, errors.New("no tab")
	}
	var buf []byte
	if err := chromedp.Run(t.ctx, chromedp.CaptureScreenshot(&buf)); err != nil {
		return nil, fmt.Errorf("screenshot: %w", err)
	}
	return buf, nil
}

// Wait blocks up to d; if text is set, returns early once it appears in body.
func (s *Session) Wait(d time.Duration, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return errors.New("no tab")
	}
	if text == "" {
		if d > 0 {
			time.Sleep(d)
		}
		return nil
	}
	deadline := time.Now().Add(d)
	for {
		var inner string
		if err := chromedp.Run(t.ctx, chromedp.Evaluate("document.body?document.body.innerText:''", &inner)); err != nil {
			return fmt.Errorf("wait: %w", err)
		}
		if strings.Contains(inner, text) {
			return nil
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(150 * time.Millisecond)
	}
	return fmt.Errorf("wait: text %q not found within %s", text, d)
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
		err := chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			id, e := s.resolveRefLocked(ctx, ref)
			if e != nil {
				return e
			}
			res, exc, e := runtime.CallFunctionOn("function() { return this.innerText || this.textContent || ''; }").
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
	if err := chromedp.Run(t.ctx, chromedp.Evaluate(readBodyJS, &body)); err == nil {
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
	if err := chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
		res, exc, err := runtime.Evaluate(script).Do(ctx)
		if err != nil {
			return fmt.Errorf("eval: %w", err)
		}
		if exc != nil {
			return fmt.Errorf("eval failed: %s", exc.Text)
		}
		if res != nil && len(res.Value) > 0 {
			out = string(res.Value)
		}
		return nil
	})); err != nil {
		return "", err
	}
	return out, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return fmt.Sprintf("%s...(truncated; %d chars total)", s[:n], len(s))
}
