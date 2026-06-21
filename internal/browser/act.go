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

	"github.com/dondai1234/agent-browser/v2/internal/snapshot"
)

// fillJS sets an input/textarea value via the native value setter + dispatches
// input+change so React/Vue/etc. see the change (a plain .value= does not).
const fillJS = `function(v) { try { this.focus(); } catch(e) {} var proto = this.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype; var setter = Object.getOwnPropertyDescriptor(proto, 'value').set; setter.call(this, v); this.dispatchEvent(new Event('input',{bubbles:true})); this.dispatchEvent(new Event('change',{bubbles:true})); return this.value; }`

// selectJS sets a <select> by matching an option's value OR visible text
// (exact, then substring fallback), via the native value setter + change event.
const selectJS = `function(v) { var opts=this.options, m=null; for(var i=0;i<opts.length;i++){ if(opts[i].value===v||opts[i].text===v||opts[i].textContent===v){m=opts[i];break;} } if(!m){ for(var i=0;i<opts.length;i++){ if(opts[i].textContent.indexOf(v)>=0){m=opts[i];break;} } } if(!m) return this.value; var setter=Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype,'value').set; setter.call(this,m.value); this.dispatchEvent(new Event('change',{bubbles:true})); return this.value; }`

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
	_, exc, err := runtime.CallFunctionOn(selectJS).
		WithObjectID(id).
		WithArguments([]*runtime.CallArgument{{Value: jsontext.Value(arg)}}).
		Do(ctx)
	if err != nil {
		return fmt.Errorf("select: %w", err)
	}
	if exc != nil {
		return fmt.Errorf("select failed: %s", exc.Text)
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

// ActResult holds an Act outcome. If Resolved is nil and Candidates is non-empty,
// the intent was ambiguous (surface candidates). If both are empty/nil the intent
// matched nothing. Otherwise the action ran and Delta/After carry the verdict.
type ActResult struct {
	Resolved   *snapshot.Element // the element acted on (nil if ambiguous/no-match)
	Verb       string            // "click" | "fill" | "select"
	Candidates []snapshot.Element // present when ambiguous (Resolved nil); ranked best-first
	Delta      *snapshot.Delta
	After      *snapshot.Tree
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
		name := strings.ToLower(el.Name)
		if name == "" || !strings.Contains(name, needle) {
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

	// nth: explicit 1-based pick from the ranked list.
	if nth > 0 {
		if nth > len(scored) {
			return snapshot.Element{}, nil, fmt.Errorf("nth=%d but only %d matches for %q", nth, len(scored), intent)
		}
		return scored[nth-1].el, nil, nil
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
func (s *Session) Act(intent, value, role string, nth int, settle time.Duration) (*ActResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return nil, ErrNoSnapshot
	}
	resolved, candidates, rerr := resolveIntent(t.tree, intent, value, role, nth)
	if rerr != nil {
		// Ambiguous (candidates populated) or no-match (candidates empty).
		return &ActResult{Candidates: candidates}, rerr
	}
	before := t.tree
	startTs := time.Now()
	verb := "click"
	// resolveRefLocked + the node action must run inside chromedp.Run so the
	// ctx carries the cdp executor (a bare .Do(t.ctx) fails with "invalid
	// context" - the known chromedp quirk; mutateAndSee wraps the same way).
	switch {
	case isFillableRole(resolved.Role):
		if strings.TrimSpace(value) == "" {
			return &ActResult{Resolved: &resolved}, fmt.Errorf("resolved [%s] %s %q is a %s; pass a value to fill it (or name a clickable control instead)", resolved.Ref, resolved.Role, resolved.Name, resolved.Role)
		}
		verb = "fill"
		if err := chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			id, err := s.resolveRefLocked(ctx, resolved.Ref)
			if err != nil {
				return err
			}
			return s.fillNodeLocked(ctx, id, value)
		})); err != nil {
			return &ActResult{Resolved: &resolved, Verb: verb}, err
		}
	case resolved.Role == "combobox" && strings.TrimSpace(value) != "":
		verb = "select"
		if err := chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			id, err := s.resolveRefLocked(ctx, resolved.Ref)
			if err != nil {
				return err
			}
			return s.selectNodeLocked(ctx, id, value)
		})); err != nil {
			return &ActResult{Resolved: &resolved, Verb: verb}, err
		}
	default:
		if err := chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
			id, err := s.resolveRefLocked(ctx, resolved.Ref)
			if err != nil {
				return err
			}
			return s.clickNodeLocked(ctx, id)
		})); err != nil {
			return &ActResult{Resolved: &resolved, Verb: verb}, err
		}
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
