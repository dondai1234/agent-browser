package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/dondai1234/agent-browser/v3/internal/snapshot"
)

// fillJS sets an input/textarea value via the native value setter + dispatches
// input+change so React/Vue/etc. see the change (a plain .value= does not).
const fillJS = `function(v) { try { this.focus(); } catch(e) {} var proto = this.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype; var setter = Object.getOwnPropertyDescriptor(proto, 'value').set; setter.call(this, v); this.dispatchEvent(new Event('input',{bubbles:true})); this.dispatchEvent(new Event('change',{bubbles:true})); return this.value; }`

// selectJS sets a <select> by matching an option's value OR visible text
// (exact, then substring fallback), via the native value setter + change event.
// Returns null when no option matches, so selectNodeLocked can report a no-op
// (the agent must not mistake a failed select for a success).
const selectJS = `function(v) { var opts=this.options, m=null; for(var i=0;i<opts.length;i++){ if(opts[i].value===v||opts[i].text===v||opts[i].textContent===v){m=opts[i];break;} } if(!m){ for(var i=0;i<opts.length;i++){ if(opts[i].textContent.indexOf(v)>=0){m=opts[i];break;} } } if(!m) return null; var setter=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set; setter.call(this,m.value); this.dispatchEvent(new Event('change',{bubbles:true})); return this.value; }`

// fillNodeLocked sets an input/textarea value by remote object id. Caller must
// hold s.mu. Factored out of FillAndSee so Act can fill a resolved element under
// the same lock without re-entering mutateAndSee (which would deadlock).
func (s *Session) fillNodeLocked(ctx context.Context, id runtime.RemoteObjectID, value string) error {
	arg, _ := json.Marshal(value)
	_, exc, err := runtime.CallFunctionOn(fillJS).
		WithObjectID(id).
		WithArguments([]*runtime.CallArgument{{Value: jsontext.Value(arg)}}).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("fill: %w", err)
	}
	if exc != nil {
		return fmt.Errorf("fill failed: %s", exc.Text)
	}
	return nil
}

// selectNodeLocked sets a <select> value by remote object id. Caller must hold
// s.mu. Factored out of SelectAndSee for Act's use.
func (s *Session) selectNodeLocked(ctx context.Context, id runtime.RemoteObjectID, value string) error {
	arg, _ := json.Marshal(value)
	res, exc, err := runtime.CallFunctionOn(selectJS).
		WithObjectID(id).
		WithReturnByValue(true).
		WithArguments([]*runtime.CallArgument{{Value: jsontext.Value(arg)}}).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	if exc != nil {
		return fmt.Errorf("select failed: %s", exc.Text)
	}
	// selectJS returns null when no option matched value/text/substring; report it
	// so the agent doesn't think a no-op select succeeded.
	if res == nil || len(res.Value) == 0 || string(res.Value) == "null" {
		return fmt.Errorf("select: no option matching %q (check the option values/text with see or find role=option)", value)
	}
	return nil
}

func isFillableRole(role string) bool {
	return role == "textbox" || role == "searchbox" || role == "spinbutton"
}

// isClickableRole lists roles whose default action is a click (used by the act
// resolver to pick the verb + to score click-appropriateness when no value).
func isClickableRole(role string) bool {
	switch role {
	case "button", "link", "menuitem", "menuitemcheckbox", "menuitemradio",
		"tab", "checkbox", "radio", "switch", "option", "treeitem":
		return true
	}
	return false
}

// errAmbiguous is returned by Act when multiple controls match the intent and
// none is a clear winner; the caller surfaces the candidate list so the agent
// can disambiguate with nth/role or fall back to click/fill by ref.
var errAmbiguous = errors.New("ambiguous: multiple elements match the intent; pass nth (1-based) or role to pick, or use a more specific name")

// errDOMNoMatch is the DOM fallback's "nothing matched" sentinel: it tells Act
// to fall through to the a11y no-match error instead of trying to act on a
// zero-value candidate (which would run querySelector("") and throw).
var errDOMNoMatch = errors.New("no DOM-attribute match")

// ActResult holds an Act outcome. If Resolved is nil and Candidates is non-empty,
// the intent was ambiguous (surface candidates). If both are empty/nil the intent
// matched nothing. Otherwise the action ran and Delta/After carry the verdict.
type ActResult struct {
	Resolved       *snapshot.Element  // the element acted on (nil if ambiguous/no-match)
	Verb           string             // "click" | "fill" | "select"
	Candidates     []snapshot.Element // present when an A11Y match was ambiguous (Resolved nil); ranked best-first
	CandidatesText string             // rendered candidate list for non-a11y matches (the DOM fallback); empty for a11y matches
	Delta          *snapshot.Delta
	After          *snapshot.Tree
}

type scoredEl struct {
	el    snapshot.Element
	score int
}

// resolveIntent finds the control named `intent` on the cached tree (local
// heuristics, no LLM) and decides whether to act. Matching: the element name
// contains the intent (case-insensitive). Scoring: exact name > name starts
// with intent > substring; plus a boost for role-appropriateness given whether
// a value was supplied (value -> prefer fillable; no value -> prefer clickable).
// Decision: one match -> act; one clear exact match -> act; nth given -> pick
// the Nth ranked; otherwise -> ambiguous (return ranked candidates, don't guess).
func resolveIntent(tree *snapshot.Tree, intent, value, role string, nth int) (snapshot.Element, []snapshot.Element, error) {
	intent = strings.TrimSpace(intent)
	if intent == "" {
		return snapshot.Element{}, nil, errors.New("intent required: the name/label of the control to act on (e.g. \"Sign in\", \"Username\", \"Add to cart\")")
	}
	needle := strings.ToLower(intent)
	hasValue := strings.TrimSpace(value) != ""
	var scored []scoredEl
	for _, el := range tree.Elems {
		if role != "" && el.Role != role {
			continue
		}
		// value= is ONLY for fillable/combobox (the act contract). So when a value
		// is supplied, restrict a11y candidates to fillable/combobox - otherwise a
		// clickable with an exact name (Wikipedia's "Search" button) outranks the
		// search input and act clicks the button instead of filling the input.
		// nth is an explicit pick, so don't restrict when the agent pinned it.
		if hasValue && nth == 0 && !isFillableRole(el.Role) && el.Role != "combobox" {
			continue
		}
		name := strings.ToLower(el.Name)
		matched := name != "" && strings.Contains(name, needle)
		// A custom combobox (button+listbox) often has no accessible NAME - Chrome
		// puts its visible label/selection in the VALUE (e.g. combobox ="Select a
		// country"). Match the intent against the value too so an unnamed combobox
		// is addressable by its label (scored below a real name match).
		valMatch := false
		if !matched && el.Role == "combobox" && el.Value != "" {
			valMatch = strings.Contains(strings.ToLower(el.Value), needle)
		}
		if !matched && !valMatch {
			continue
		}
		sc := 10
		switch {
		case name == needle:
			sc = 100
		case strings.HasPrefix(name, needle):
			sc = 50
		}
		switch {
		case hasValue && isFillableRole(el.Role):
			sc += 20
		case !hasValue && isClickableRole(el.Role):
			sc += 20
		case hasValue && el.Role == "combobox":
			sc += 15
		}
		scored = append(scored, scoredEl{el, sc})
	}
	if len(scored) == 0 {
		return snapshot.Element{}, nil, fmt.Errorf("no element named %q found; use see/find to check the page", intent)
	}
	sort.SliceStable(scored, func(i, j int) bool { return scored[i].score > scored[j].score })

	// nth: explicit pick from the ranked list. nth>0 is 1-based from the top
	// (nth=1 = best match); nth<0 is from the end (nth=-1 = last match, -2 =
	// second-last) - the "add the priciest of N identical Add-to-cart buttons"
	// case without the agent having to count.
	if nth != 0 {
		idx := nth - 1
		if nth < 0 {
			idx = len(scored) + nth // nth=-1 -> len-1 (last)
		}
		if idx < 0 || idx >= len(scored) {
			return snapshot.Element{}, nil, fmt.Errorf("nth=%d but only %d matches for %q", nth, len(scored), intent)
		}
		return scored[idx].el, nil, nil
	}
	if len(scored) == 1 {
		return scored[0].el, nil, nil
	}
	// Act if the top match is clearly ahead of the runner-up: an exact name over
	// a non-exact one (gap >= 50), or a role-appropriateness boost (value +
	// fillable, or no-value + clickable) that separates them by >= 20. Ties -
	// e.g. several "Add to cart" buttons, or a button + link both named "Sign
	// in" - are ambiguous: hand back the ranked candidates, don't guess.
	if scored[0].score-scored[1].score >= 20 {
		return scored[0].el, nil, nil
	}
	cands := make([]snapshot.Element, len(scored))
	for i, sc := range scored {
		cands[i] = sc.el
	}
	return snapshot.Element{}, cands, errAmbiguous
}

// Act resolves `intent` to a control on the current page and performs the
// default action for its role: click for buttons/links/menuitems/tabs/checkboxes
// etc., fill (with value) for textbox/searchbox/spinbutton, select (with value)
// for combobox. Returns the resolved element, the verb, and a verdict + delta
// (same machinery as click/fill). Atomic (one lock for resolve + act + re-snapshot).
//
// Matching is two-tier: first the a11y name (what Chrome's AX tree computed from
// the label/placeholder/aria-label), then, on no-match, a DOM-attribute fallback
// over name/id/placeholder/title/aria-label so poorly-labeled inputs (no <label>,
// no placeholder surfaced in the a11y name, only a name=/id= the agent knows from
// HTML or extract form) are still reachable by intent. The DOM fallback runs
// only on no-match, so the hot path pays no extra CDP round-trip.
func (s *Session) Act(intent, value, role string, nth int, settle time.Duration) (*ActResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return nil, ErrNoSnapshot
	}
	intent = strings.TrimSpace(intent)
	before := t.tree
	startTs := time.Now()

	resolved, candidates, rerr := resolveIntent(t.tree, intent, value, role, nth)
	if rerr != nil {
		if len(candidates) == 0 {
			// No a11y-name match - try the DOM-attribute fallback for
			// poorly-labeled inputs (name/id/placeholder/title/aria-label).
			domCand, domCands, domErr := s.resolveIntentDOMLocked(t, intent, value, role, nth)
			switch {
			case domErr == nil:
				verb, actErr := s.actOnDOMLocked(t, domCand, value)
				if actErr != nil {
					s.recordActionErrorLocked(before, fmt.Sprintf("act %q (%s, dom)", intent, verb), actErr)
					return &ActResult{Verb: verb}, actErr
				}
				if settle > 0 {
					time.Sleep(settle)
				}
				delta, after, ferr := s.finishMutationLocked(t, before, startTs, fmt.Sprintf("act %q (%s, dom)", intent, verb))
				if ferr != nil {
					return &ActResult{Verb: verb}, ferr
				}
				return &ActResult{Resolved: &snapshot.Element{Ref: "dom", Role: domRoleFor(domCand), Name: domCand.Val}, Verb: verb, Delta: delta, After: after}, nil
			case errors.Is(domErr, errDOMNoMatch):
				// No DOM match either - fall through to the a11y no-match error below.
			case len(domCands) > 0:
				// Ambiguous, or nth out of range - surface the ranked candidates.
				return &ActResult{CandidatesText: renderDOMCandidates(domCands)}, domErr
			}
		}
		s.recordActionErrorLocked(before, fmt.Sprintf("act %q", intent), rerr)
		return &ActResult{Candidates: candidates}, rerr
	}

	verb := "click"
	var actErr error
	// resolveRefLocked + the node action must run inside chromedp.Run so the ctx
	// carries the cdp executor (a bare .Do(t.ctx) fails with "invalid context" -
	// the known chromedp quirk; mutateAndSee wraps the same way).
	switch {
	case isFillableRole(resolved.Role):
		if strings.TrimSpace(value) == "" {
			return &ActResult{Resolved: &resolved}, fmt.Errorf("resolved [%s] %s %q is a %s; pass a value to fill it (or name a clickable control instead)", resolved.Ref, resolved.Role, resolved.Name, resolved.Role)
		}
		verb = "fill"
		actErr = s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			id, err := s.resolveRefLocked(ctx, resolved.Ref)
			if err != nil {
				return err
			}
			return s.fillNodeLocked(ctx, id, value)
		}))
	case resolved.Role == "combobox" && strings.TrimSpace(value) != "":
		// A combobox is one of three things, picked by probing the tag/type:
		//   1. native <select>           -> selectJS (set the option)
		//   2. a text input/textarea      -> fill (autocomplete: React/Vue hear input/change)
		//   3. a button/div + listbox     -> open-select dance (open popup, click option)
		actErr = s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			id, err := s.resolveRefLocked(ctx, resolved.Ref)
			if err != nil {
				return err
			}
			var tagType string
			if res, _, e := runtime.CallFunctionOn(`function(){return this.tagName + '/' + (this.type||''); }`).WithObjectID(id).Do(ctx); e == nil && res != nil && len(res.Value) > 0 {
				_ = json.Unmarshal(res.Value, &tagType)
			}
			tag := strings.SplitN(tagType, "/", 2)[0]
			switch {
			case tag == "SELECT":
				verb = "select"
				return s.selectNodeLocked(ctx, id, value)
			case tag == "INPUT" || tag == "TEXTAREA":
				verb = "fill"
				return s.fillNodeLocked(ctx, id, value)
			default:
				// button/div listbox-combobox: open the popup + click the option.
				verb = "open-select"
				_, e := s.openSelectComboboxLocked(ctx, t, resolved.Ref, value)
				return e
			}
		}))
	case resolved.Role == "combobox":
		// A combobox/select with no value: clicking it usually just opens the
		// dropdown (rarely the intent). Tell the agent to pass a value.
		return &ActResult{Resolved: &resolved}, fmt.Errorf("resolved [%s] %s %q is a combobox; pass a value to select an option (or name a clickable control)", resolved.Ref, resolved.Role, resolved.Name)
	default:
		// A button/link: normally just click. But if the agent passed a VALUE and
		// this is a listbox-combobox (a button with aria-haspopup=listbox), value=
		// means SELECT an option - run the open-select dance instead of a bare
		// click (a bare click would only open the popup and leave it open).
		if strings.TrimSpace(value) != "" && isListboxCombobox(resolved) {
			verb = "open-select"
			actErr = s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
				_, e := s.openSelectComboboxLocked(ctx, t, resolved.Ref, value)
				return e
			}))
		} else {
			actErr = s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
				id, err := s.resolveRefLocked(ctx, resolved.Ref)
				if err != nil {
					return err
				}
				return s.clickNodeLocked(ctx, id)
			}))
		}
	}
	if actErr != nil {
		s.recordActionErrorLocked(before, fmt.Sprintf("act %q (%s)", intent, verb), actErr)
		return &ActResult{Resolved: &resolved, Verb: verb}, actErr
	}
	if settle > 0 {
		time.Sleep(settle)
	}
	delta, after, ferr := s.finishMutationLocked(t, before, startTs, fmt.Sprintf("act %q (%s)", intent, verb))
	if ferr != nil {
		return &ActResult{Resolved: &resolved, Verb: verb}, ferr
	}
	return &ActResult{Resolved: &resolved, Verb: verb, Delta: delta, After: after}, nil
}

// recordActionErrorLocked appends a failed-action entry to the session log so
// history errors=true can surface it (a click that timed out, an act that found
// nothing, a fill that threw). Caller must hold s.mu.
func (s *Session) recordActionErrorLocked(before *snapshot.Tree, action string, err error) {
	url := ""
	if before != nil {
		url = before.URL
	}
	s.recordHistoryLocked(action, "error: "+err.Error(), url)
}

// intentDOMJS is the DOM-attribute fallback matcher. It reads __needle/__role
// (set by the caller via a prefixed var declaration so the args are safely
// JSON-encoded, no string interpolation) and returns a JSON array of ranked
// candidates: interactive elements whose name/id/placeholder/title/aria-label
// (or button/link text) contains the intent, each with a unique CSS selector so
// actOnDOMLocked can re-find + act on the chosen one without a ref.
const intentDOMJS = `(function(){
  var needle = __needle, wantRole = __role, hasValue = __hasValue;
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
  function isFillableTag(tag,type){ if(tag==='textarea'||tag==='select')return true; if(tag==='input'){ var t=(type||'text').toLowerCase(); return t==='text'||t==='email'||t==='tel'||t==='url'||t==='search'||t==='password'||t==='number'; } return false; }
  function isClickableTag(tag,type){ if(tag==='button'||tag==='a')return true; if(tag==='input'){ var t=(type||'').toLowerCase(); return t==='submit'||t==='button'||t==='image'||t==='reset'||t==='checkbox'||t==='radio'||t==='file'; } return false; }
  var tags=['input','textarea','select','button','a'];
  var roleTags={button:['button','a'],link:['a'],textbox:['input','textarea'],searchbox:['input'],combobox:['select'],spinbutton:['input'],checkbox:['input'],radio:['input'],switch:['input'],menuitem:['button','a'],tab:['button','a'],option:['option']};
  var out=[];
  for(var ti=0; ti<tags.length; ti++){
    var tag=tags[ti];
    if(wantRole){ var allow=roleTags[wantRole]; if(!allow||allow.indexOf(tag)<0) continue; }
    var els=document.querySelectorAll(tag);
    for(var j=0;j<els.length;j++){
      var el=els[j];
      if(el.disabled) continue;
      var type=(el.tagName==='INPUT')?(el.type||'text'):'';
      // value= is for fillable tags only; a bare intent is for clickables. Skip
      // the wrong family so a value-bearing "Search" finds the input, not the
      // "Search" button (mirrors the a11y restriction in resolveIntent).
      if(hasValue && !isFillableTag(tag,type)) continue;
      if(!hasValue && !isClickableTag(tag,type)) continue;
      var keys=['aria-label','placeholder','name','id','title'];
      var best=null,bestAttr='';
      for(var k=0;k<keys.length;k++){
        var av;
        if(keys[k]==='aria-label') av=el.getAttribute('aria-label')||'';
        else if(keys[k]==='placeholder') av=el.placeholder||'';
        else if(keys[k]==='name') av=el.name||'';
        else if(keys[k]==='id') av=el.id||'';
        else av=el.title||'';
        var lv=(av||'').toLowerCase();
        if(lv && lv.indexOf(needle)>=0){ best=av; bestAttr=keys[k]; break; }
      }
      if(!best){ var txt=(el.textContent||'').trim(); if(txt && txt.toLowerCase().indexOf(needle)>=0){ best=txt; bestAttr='text'; } }
      if(!best) continue;
      var bl=best.toLowerCase(); var sc=10; if(bl===needle) sc=100; else if(bl.indexOf(needle)===0) sc=50;
      // Role-aware boost: when the agent passed a value it wants to FILL, prefer
      // text inputs/textareas/selects; when no value it wants to CLICK, prefer
      // buttons/links. This stops a value-bearing "Search" intent from landing
      // on a "Search" link instead of the search input.
      if(hasValue && isFillableTag(tag,type)) sc+=20; else if(!hasValue && isClickableTag(tag,type)) sc+=20;
      out.push({tag:tag,type:type,attr:bestAttr,val:best,score:sc,sel:uniqueSel(el)});
    }
  }
  out.sort(function(a,b){ return b.score-a.score; });
  return JSON.stringify(out);
})()`

// domCandidate is one DOM-attribute match from the intent fallback.
type domCandidate struct {
	Tag   string `json:"tag"`
	Type  string `json:"type"`
	Attr  string `json:"attr"`
	Val   string `json:"val"`
	Score int    `json:"score"`
	Sel   string `json:"sel"`
}

// resolveIntentDOMLocked is the fallback when no a11y-name match exists: query
// the DOM for interactive elements whose name/id/placeholder/title/aria-label
// (or button/link text) contains the intent, ranked best-first. One CDP
// evaluate, root document only (no iframe pierce - the a11y path handles
// iframes via the merged tree). Returns the chosen candidate (or zero value),
// the ranked list (for the ambiguous case), and errAmbiguous when several match
// with no clear winner; nil error with an empty list when nothing matches.
// Caller must hold s.mu.
func (s *Session) resolveIntentDOMLocked(t *tab, intent, value, role string, nth int) (domCandidate, []domCandidate, error) {
	needleJSON, _ := json.Marshal(strings.ToLower(strings.TrimSpace(intent)))
	roleJSON, _ := json.Marshal(strings.ToLower(strings.TrimSpace(role)))
	hasValueJSON, _ := json.Marshal(strings.TrimSpace(value) != "")
	script := "var __needle=" + string(needleJSON) + "; var __role=" + string(roleJSON) + "; var __hasValue=" + string(hasValueJSON) + "; " + intentDOMJS
	var raw string
	if err := s.runTimeout(t, axPollTimeout, chromedp.Evaluate(script, &raw)); err != nil {
		return domCandidate{}, nil, err
	}
	var cands []domCandidate
	if strings.TrimSpace(raw) == "" || raw == "null" || raw == "[]" {
		return domCandidate{}, nil, errDOMNoMatch
	}
	if err := json.Unmarshal([]byte(raw), &cands); err != nil {
		return domCandidate{}, nil, err
	}
	if len(cands) == 0 {
		return domCandidate{}, nil, errDOMNoMatch
	}
	if nth != 0 {
		idx := nth - 1
		if nth < 0 {
			idx = len(cands) + nth
		}
		if idx < 0 || idx >= len(cands) {
			return domCandidate{}, cands, fmt.Errorf("nth=%d but only %d DOM matches for %q", nth, len(cands), intent)
		}
		return cands[idx], cands, nil
	}
	if len(cands) == 1 || (len(cands) >= 2 && cands[0].Score-cands[1].Score >= 20) {
		return cands[0], cands, nil
	}
	return domCandidate{}, cands, errAmbiguous
}

// actOnDOMLocked resolves a DOM fallback candidate's unique CSS selector to a
// remote object id and performs the role-appropriate action (click/fill/select)
// on it, all inside one chromedp.Run so the ctx carries the CDP executor (a bare
// .Do(t.ctx) fails with "invalid context"). Returns the verb it performed.
// Caller must hold s.mu.
func (s *Session) actOnDOMLocked(t *tab, cand domCandidate, value string) (string, error) {
	verb := "click"
	err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		selJSON, _ := json.Marshal(cand.Sel)
		res, exc, e := runtime.Evaluate(`(function(){ return document.querySelector(` + string(selJSON) + `); })()`).Do(ctx)
		if e != nil {
			return e
		}
		if exc != nil {
			return fmt.Errorf("%s", exc.Text)
		}
		if res == nil || res.ObjectID == "" {
			return fmt.Errorf("DOM element not found (selector %q); the page may have re-rendered - call see, then retry", cand.Sel)
		}
		id := res.ObjectID
		hasValue := strings.TrimSpace(value) != ""
		switch {
		case cand.Tag == "select":
			if !hasValue {
				verb = "select"
				return fmt.Errorf("resolved a <select> (%q); pass a value to select an option", cand.Val)
			}
			verb = "select"
			return s.selectNodeLocked(ctx, id, value)
		case cand.Tag == "textarea" || (cand.Tag == "input" && isTextInputType(cand.Type)):
			if !hasValue {
				verb = "fill"
				return fmt.Errorf("resolved an input %q; pass a value to fill it (or name a clickable control)", cand.Val)
			}
			verb = "fill"
			return s.fillNodeLocked(ctx, id, value)
		default:
			verb = "click"
			return s.clickNodeLocked(ctx, id)
		}
	}))
	return verb, err
}

// isTextInputType reports whether an <input type=...> is a text-like field that
// fill targets (vs a checkbox/radio/submit/button/file that click targets).
func isTextInputType(typ string) bool {
	switch strings.ToLower(typ) {
	case "text", "email", "tel", "url", "search", "password", "number":
		return true
	}
	return false
}

// domRoleFor maps a DOM candidate's tag/type to the a11y role name used in the
// act response (so the agent sees e.g. [dom] textbox "custcode" (fill)).
func domRoleFor(c domCandidate) string {
	switch {
	case c.Tag == "select":
		return "combobox"
	case c.Tag == "textarea":
		return "textbox"
	case c.Tag == "input":
		switch strings.ToLower(c.Type) {
		case "checkbox":
			return "checkbox"
		case "radio":
			return "radio"
		case "search", "searchbox":
			return "searchbox"
		case "number":
			return "spinbutton"
		case "submit", "button", "image", "reset":
			return "button"
		default:
			return "textbox"
		}
	case c.Tag == "a":
		return "link"
	case c.Tag == "button":
		return "button"
	}
	return c.Tag
}

// renderDOMCandidates renders the DOM fallback's ranked candidates for the
// ambiguous response (the a11y path uses RenderElements; DOM candidates have no
// ref, so they get a [dom] tag + the matched attribute instead).
func renderDOMCandidates(cands []domCandidate) string {
	var b strings.Builder
	limit := len(cands)
	if limit > 8 {
		limit = 8
	}
	for i := 0; i < limit; i++ {
		c := cands[i]
		fmt.Fprintf(&b, "[dom] %s", c.Tag)
		if c.Type != "" {
			fmt.Fprintf(&b, " type=%q", c.Type)
		}
		fmt.Fprintf(&b, " %s=%q", c.Attr, c.Val)
		b.WriteByte('\n')
	}
	if len(cands) > 8 {
		fmt.Fprintf(&b, "... and %d more (pass a more specific name, or role/nth to pick)\n", len(cands)-8)
	}
	return strings.TrimRight(b.String(), "\n")
}
