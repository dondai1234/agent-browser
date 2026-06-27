package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// extractTableJS finds the first <table> under root, returns rows. If the first
// row is all <th>, it's used as headers and rows become objects {header: value};
// otherwise an array of arrays. null if no table. root is document or a scoped
// element (selector param).
const extractTableJS = `(function(root){
  var t=root.querySelector('table'); if(!t) return null;
  var trs=t.querySelectorAll('tr'); var rows=[];
  trs.forEach(function(tr){var c=[];tr.querySelectorAll('th,td').forEach(function(x){c.push((x.innerText||'').trim())});if(c.length)rows.push(c)});
  if(!rows.length) return null;
  var firstTr=trs[0]; var isHeader=firstTr&&Array.from(firstTr.children).every(function(x){return x.tagName==='TH'});
  if(isHeader){
    var h=rows[0]; var out=[];
    for(var i=1;i<rows.length;i++){var o={};for(var j=0;j<h.length;j++){o[h[j]]=rows[i][j]||''};out.push(o)}
    return out;
  }
  return rows;
})`

// extractLinksJS returns [{text, href}] for every <a> under root, capped (a nav
// can have dozens; the cap tames a huge dump while the full set stays available
// via find).
const extractLinksJS = `(function(root){
  var a=[]; var links=root.querySelectorAll('a');
  for(var i=0;i<links.length && a.length<200;i++){
    var x=links[i];
    a.push({text:(x.innerText||x.textContent||'').trim().slice(0,120), href:x.href});
  }
  return a;
})`

// extractListJS picks the largest <ul>/<ol> under root (most direct <li>
// children) and returns its item texts. null if no list. Direct children only so
// a nested menu doesn't flatten into one list.
const extractListJS = `(function(root){
  var lists=root.querySelectorAll('ul,ol'); var best=null, bestN=0;
  lists.forEach(function(l){var n=l.querySelectorAll(':scope > li').length; if(n>bestN){bestN=n;best=l}});
  if(!best) return null;
  var items=[]; var li=best.querySelectorAll(':scope > li');
  for(var i=0;i<li.length && i<200;i++){items.push((li[i].innerText||'').trim().slice(0,200))}
  return items;
})`

// extractArticleJS returns the main content text under root: <article>, else
// <main>, else [role=main], else root itself (so a selector scoping to a region
// returns that region's text). null only if root itself is null (caller guard).
const extractArticleJS = `(function(root){
  var a=root.querySelector('article')||root.querySelector('main')||root.querySelector('[role=main]');
  if(!a) a=root;
  if(!a) return null;
  return a.innerText;
})`

// extractCap bounds the JSON we return so a giant table/link dump can't blow up
// the response. The agent can narrow with selector= or a more specific page.
const extractCap = 16000

// articleCap bounds extract article's plain-text response (the lead of a long
// article is what the agent usually wants; raise via maxChars for the whole).
const articleCap = 12000

// textItemCap bounds each item in extract text's array (one element's text).
const textItemCap = 4000

// Extract returns structured data for a kind:
//
//	table (rows, JSON), links ([{text,href}], JSON), list (item texts, JSON),
//	form ([{ref,role,name,value}], JSON from the cached AX tree - no CDP round
//	trip), article (main content text), text ([string] of each matched element's
//	text, requires selector).
//
// selector scopes table/links/list/article to a region (querySelector) and is
// required for text (querySelectorAll, all matches). maxChars caps the response
// (defaults: 16000 structured / 12000 article); lower it to spend fewer tokens.
// One targeted DOM eval for the DOM kinds; form is free (cached tree).
func (s *Session) Extract(kind, selector string, maxChars int) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	selector = strings.TrimSpace(selector)
	switch kind {
	case "form":
		// form comes from the cached AX tree (no DOM eval); selector doesn't apply.
		return s.extractForm(), nil
	case "table", "links", "list", "article", "text":
	default:
		return "", fmt.Errorf("unknown extract kind %q (use: table | links | list | form | article | text)", kind)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return "", errors.New("no tab")
	}

	// Build the eval script. text uses querySelectorAll (all matches' text);
	// the others use a (function(root){...}) body called with document or a
	// scoped querySelector result.
	var js string
	if kind == "text" {
		if selector == "" {
			return "", fmt.Errorf("extract text requires a selector (CSS selector of the element(s) whose text you want, e.g. \".stars\", \"#price\")")
		}
		selJSON, _ := json.Marshal(selector)
		js = `(function(){ var els=document.querySelectorAll(` + string(selJSON) + `); if(!els||!els.length) return null; var out=[]; for(var i=0;i<els.length&&i<100;i++){ out.push((els[i].innerText||els[i].textContent||'').trim()); } return out; })()`
	} else {
		body := map[string]string{
			"table":   extractTableJS,
			"links":   extractLinksJS,
			"list":    extractListJS,
			"article": extractArticleJS,
		}[kind]
		if selector == "" {
			js = "(" + body + ")(document)"
		} else {
			selJSON, _ := json.Marshal(selector)
			js = `(function(){ var root=document.querySelector(` + string(selJSON) + `); if(!root) return null; return (` + body + `)(root); })()`
		}
	}

	var raw string
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		// ReturnByValue so arrays/objects serialize into res.Value (the known
		// chromedp quirk: without it, array returns don't come back).
		res, exc, err := runtime.Evaluate(js).WithReturnByValue(true).Do(ctx)
		if err != nil {
			return fmt.Errorf("extract %s: %w", kind, err)
		}
		if exc != nil {
			return fmt.Errorf("extract %s failed: %s", kind, exc.Text)
		}
		if res != nil && len(res.Value) > 0 {
			raw = string(res.Value)
		}
		return nil
	})); err != nil {
		return "", err
	}
	if raw == "" || raw == "null" {
		note := ""
		if selector != "" {
			note = fmt.Sprintf(" under selector %q", selector)
		}
		return "", fmt.Errorf("no %s found%s; use see/read for the raw content, or a different selector", kind, note)
	}

	// Caps: maxChars overrides; else defaults per kind.
	acap, scap := articleCap, extractCap
	if maxChars > 0 {
		acap, scap = maxChars, maxChars
	}
	switch kind {
	case "article":
		// article comes back as a JSON-encoded string; decode to plain text,
		// truncate, and return readable (not JSON-quoted).
		var text string
		if err := json.Unmarshal([]byte(raw), &text); err != nil {
			return "", fmt.Errorf("extract article: %w", err)
		}
		return truncate(text, acap), nil
	case "text":
		// raw is a JSON array of strings; cap each item, then the whole.
		var arr []string
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return truncate(raw, scap), nil
		}
		for i, v := range arr {
			arr[i] = truncate(v, textItemCap)
		}
		b, err := json.Marshal(arr)
		if err != nil {
			return truncate(raw, scap), nil
		}
		return truncate(string(b), scap), nil
	default:
		return truncate(raw, scap), nil
	}
}

// formCtrl is one row of extract form: a control's ref + role + name + value
// (+ checked for checkbox/switch), so the agent can fill/toggle via act/click
// without scanning the full ref list.
type formCtrl struct {
	Ref     string `json:"ref"`
	Role    string `json:"role"`
	Name    string `json:"name"`
	Value   string `json:"value,omitempty"`
	Checked string `json:"checked,omitempty"`
}

// extractForm returns the page's form controls as JSON from the cached AX tree
// (no CDP round-trip - free). Covers textbox/searchbox/combobox/checkbox/radio/
// spinbutton/slider/switch. Empty array (with a hint) if none.
func (s *Session) extractForm() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return "[]"
	}
	var ctrls []formCtrl
	for _, e := range t.tree.Elems {
		var checked string
		switch e.Role {
		case "checkbox", "switch":
			checked = e.Props[accessibility.PropertyNameChecked]
		case "textbox", "searchbox", "combobox", "radio", "spinbutton", "slider":
		default:
			continue
		}
		c := formCtrl{Ref: e.Ref, Role: e.Role, Name: e.Name, Value: e.Value}
		if checked == "true" || checked == "mixed" {
			c.Checked = checked
		}
		ctrls = append(ctrls, c)
	}
	if len(ctrls) == 0 {
		return "[] (no form controls found; use see/find)"
	}
	b, err := json.Marshal(ctrls)
	if err != nil {
		return "[]"
	}
	return string(b)
}
