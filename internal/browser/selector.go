package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/dondai1234/goshawk/v4/internal/snapshot"
)

// findSelectorJS runs querySelectorAll(selector) on the top document and returns
// a JSON array of {tag, type, name, sel} for each match, where name is the best
// available label (aria-label > placeholder > title > textContent) and sel is a
// unique CSS selector for that node. This is the escape hatch for elements the
// a11y tree does NOT surface (custom widgets, presentational divs, shadow-bound
// controls that Chrome still exposes via CSS) - the gap a pure a11y-tree tool
// leaves versus a CSS-selector tool. __sel is JSON-encoded by the caller.
const findSelectorJS = `(function(){
  var sel = __sel;
  function uniqueSel(el){
    if (el && el.id) { try { return '#'+CSS.escape(el.id); } catch(e){} }
    var parts=[];
    while (el && el.nodeType===1 && el!==document.documentElement){
      var p=el.parentNode;
      if(!p){ parts.unshift(el.tagName.toLowerCase()); break; }
      var idx=Array.prototype.indexOf.call(p.children, el)+1;
      parts.unshift(el.tagName.toLowerCase()+':nth-child('+idx+')');
      el=p;
      if(el && el.id){ try { parts.unshift('#'+CSS.escape(el.id)); break; } catch(e){} }
    }
    return parts.join(' > ');
  }
  var out=[];
  var els=document.querySelectorAll(sel);
  for(var i=0;i<els.length && i<50;i++){
    var el=els[i];
    var name=el.getAttribute('aria-label')||el.placeholder||el.title||'';
    if(!name){ var t=(el.textContent||'').trim(); if(t) name=t.slice(0,80); }
    out.push({tag:el.tagName.toLowerCase(), type:(el.tagName==='INPUT')?(el.type||'text'):'', name:name, sel:uniqueSel(el)});
  }
  return JSON.stringify(out);
})()`

// SelectorMatch is one CSS-selector find result.
type SelectorMatch struct {
	Tag  string `json:"tag"`
	Type string `json:"type"`
	Name string `json:"name"`
	Sel  string `json:"sel"`
}

// FindSelector runs querySelectorAll(selector) and returns each match with a
// unique CSS selector + best-available label. It does NOT consult the a11y
// tree: this reaches elements the a11y tree drops (custom widgets,
// presentational nodes), closing the CSS-selector gap versus tools that target
// by selector. To ACT on a result, pass its `sel` to click/fill/act's `selector`
// param. If the element is also in the a11y tree, find by role/text will give a
// ref usable everywhere. Caller-friendly: empty selector -> error, not a dump.
func (s *Session) FindSelector(selector string) ([]SelectorMatch, error) {
	selector = strings.TrimSpace(selector)
	if selector == "" {
		return nil, fmt.Errorf("selector required: a CSS selector (e.g. \".btn-checkout\", \"div[role=widget]\", \"#login button\")")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return nil, ErrNoSnapshot
	}
	selJSON, _ := json.Marshal(selector)
	script := "var __sel=" + string(selJSON) + "; " + findSelectorJS
	var raw string
	if err := s.runTimeout(t, axPollTimeout, chromedp.Evaluate(script, &raw)); err != nil {
		return nil, fmt.Errorf("find selector: %w", err)
	}
	if strings.TrimSpace(raw) == "" || raw == "null" || raw == "[]" {
		return nil, fmt.Errorf("no elements match selector %q", selector)
	}
	var matches []SelectorMatch
	if err := json.Unmarshal([]byte(raw), &matches); err != nil {
		return nil, fmt.Errorf("find selector parse: %w", err)
	}
	if len(matches) == 0 {
		return nil, fmt.Errorf("no elements match selector %q", selector)
	}
	return matches, nil
}

// RenderSelectorMatches renders find-selector results as dense [css] lines the
// agent can read + feed back as a selector.
func RenderSelectorMatches(ms []SelectorMatch) string {
	var b strings.Builder
	limit := len(ms)
	if limit > 20 {
		limit = 20
	}
	for i := 0; i < limit; i++ {
		m := ms[i]
		fmt.Fprintf(&b, "[css] %s", m.Tag)
		if m.Type != "" {
			fmt.Fprintf(&b, " type=%q", m.Type)
		}
		if m.Name != "" {
			fmt.Fprintf(&b, " %q", m.Name)
		}
		fmt.Fprintf(&b, " sel=%q\n", m.Sel)
	}
	if len(ms) > 20 {
		fmt.Fprintf(&b, "... and %d more (narrow the selector)\n", len(ms)-20)
	}
	return strings.TrimRight(b.String(), "\n")
}

// uniqueSelJS computes a unique CSS selector for `this` (a resolved DOM node):
// #id when present, else a nth-child path up to the nearest id/document root.
// Used by SelectorForRef so a find-by-a11y result can also be addressed by CSS
// selector in `js` - bridging the a11y-ref world and the selector world.
const uniqueSelJS = `function(){
  if(this && this.id){ try { return '#'+CSS.escape(this.id); } catch(e){} }
  var parts=[]; var el=this;
  while(el && el.nodeType===1 && el!==document.documentElement){
    var p=el.parentNode;
    if(!p){ parts.unshift(el.tagName.toLowerCase()); break; }
    var idx=Array.prototype.indexOf.call(p.children, el)+1;
    parts.unshift(el.tagName.toLowerCase()+':nth-child('+idx+')');
    el=p;
    if(el && el.id){ try { parts.unshift('#'+CSS.escape(el.id)); break; } catch(e){} }
  }
  return parts.join(' > ');
}`

// SelectorForRef resolves a stable ref to a unique CSS selector on the current
// tab, so an element located via the a11y tree (find by role/text) can also be
// targeted by selector in `js`. One resolve + one cheap eval. Returns an error
// if the ref is gone or has no backing DOM node (a virtual a11y node).
func (s *Session) SelectorForRef(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return "", ErrNoSnapshot
	}
	el, ok := t.tree.ByRef(ref)
	if !ok {
		return "", fmt.Errorf("ref %q not found; call see to refresh refs", ref)
	}
	if el.Backend == 0 {
		return "", fmt.Errorf("ref %q has no backing DOM node (virtual a11y node); use selector= in find instead", ref)
	}
	var sel string
	err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		id, e := s.resolveBackendLocked(ctx, el.Backend, ref)
		if e != nil {
			return e
		}
		res, exc, e := runtime.CallFunctionOn(uniqueSelJS).WithReturnByValue(true).WithObjectID(id).Do(ctx)
		if e != nil {
			return fmt.Errorf("selector for ref %q: %w", ref, e)
		}
		if exc != nil {
			return fmt.Errorf("selector for ref %q: %s", ref, exc.Text)
		}
		if res != nil && len(res.Value) > 0 {
			_ = json.Unmarshal(res.Value, &sel)
		}
		return nil
	}))
	if err != nil {
		return "", err
	}
	if sel == "" {
		return "", fmt.Errorf("ref %q resolved to no selector", ref)
	}
	return sel, nil
}

// actBySelectorLocked resolves a CSS selector to a remote object id on the top
// document and performs the role-appropriate action by tag/type: click for
// buttons/links/inputs-that-arent-text, fill (native value setter + input/change)
// for text inputs/textareas, select for <select>. mode is "click" / "fill" /
// "auto" (auto detects from tag/type + whether value is present). Returns the
// verb performed. Reuses clickNodeLocked/fillNodeLocked/selectNodeLocked so the
// click reliability + React/Vue fill semantics are identical to the ref path.
// Caller must hold s.mu.
func (s *Session) actBySelectorLocked(ctx context.Context, sel, value, mode string) (string, error) {
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
	id := res.ObjectID
	// Detect tag + input type so we pick the right verb (one cheap eval).
	var info struct {
		Tag  string `json:"tag"`
		Type string `json:"type"`
	}
	tagJSON, _, e := runtime.CallFunctionOn(`function(){ return {tag:this.tagName.toLowerCase(), type:(this.tagName==='INPUT')?(this.type||'text'):''}; }`).
		WithReturnByValue(true).WithObjectID(id).Do(ctx)
	if e == nil && tagJSON != nil && len(tagJSON.Value) > 0 {
		_ = json.Unmarshal(tagJSON.Value, &info)
	}
	hasValue := strings.TrimSpace(value) != ""
	verb := "click"
	switch {
	case mode == "fill" || (mode == "auto" && (info.Tag == "textarea" || (info.Tag == "input" && isTextInputType(info.Type)))):
		if !hasValue {
			return "fill", fmt.Errorf("resolved %s (%q); pass a value to fill it", info.Tag, sel)
		}
		verb = "fill"
		return verb, s.fillNodeLocked(ctx, id, value)
	case mode == "click":
		return verb, s.clickNodeLocked(ctx, id)
	case mode == "select":
		// Explicit select-by-selector: the element MUST be a <select>, else the
		// agent used the wrong tool (click/fill/act for other tags).
		if info.Tag != "select" {
			return "select", fmt.Errorf("selector %q resolved a <%s>, not a <select>; use click/fill/act for other elements", sel, info.Tag)
		}
		if !hasValue {
			return "select", fmt.Errorf("resolved a <select> (%q); pass a value to select an option", sel)
		}
		return "select", s.selectNodeLocked(ctx, id, value)
	case info.Tag == "select":
		if !hasValue {
			return "select", fmt.Errorf("resolved a <select> (%q); pass a value to select an option", sel)
		}
		verb = "select"
		return verb, s.selectNodeLocked(ctx, id, value)
	default:
		// auto + no text-input match + (no value OR a non-fillable): click.
		return verb, s.clickNodeLocked(ctx, id)
	}
}

// ClickSelector clicks document.querySelector(selector) and returns the delta.
func (s *Session) ClickSelector(selector string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(fmt.Sprintf("click selector %q", selector), settle, func(ctx context.Context) error {
		_, e := s.actBySelectorLocked(ctx, selector, "", "click")
		return e
	})
}

// FillSelector fills document.querySelector(selector) with value + returns delta.
func (s *Session) FillSelector(selector, value string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(fmt.Sprintf("fill selector %q =%q", selector, value), settle, func(ctx context.Context) error {
		_, e := s.actBySelectorLocked(ctx, selector, value, "fill")
		return e
	})
}

// SelectSelector sets a <select> dropdown found by CSS selector and returns
// the delta. The selector path for unlabeled dropdowns (no a11y name, no
// name/id/placeholder for act's DOM fallback) - e.g. a sort <select> with only
// a class. The element must be a <select>.
func (s *Session) SelectSelector(selector, value string, settle time.Duration) (*snapshot.Delta, *snapshot.Tree, error) {
	return s.mutateAndSee(fmt.Sprintf("select selector %q =%q", selector, value), settle, func(ctx context.Context) error {
		_, e := s.actBySelectorLocked(ctx, selector, value, "select")
		return e
	})
}

// ActSelector acts on document.querySelector(selector): auto-detects tag/type
// and clicks/fills/selects. Returns the verb + delta. The selector escape hatch
// for elements the a11y tree drops.
func (s *Session) ActSelector(selector, value string, settle time.Duration) (string, *snapshot.Delta, *snapshot.Tree, error) {
	var verb string
	d, after, err := s.mutateAndSee(fmt.Sprintf("act selector %q", selector), settle, func(ctx context.Context) error {
		v, e := s.actBySelectorLocked(ctx, selector, value, "auto")
		verb = v
		return e
	})
	return verb, d, after, err
}
