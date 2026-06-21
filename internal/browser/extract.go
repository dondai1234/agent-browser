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

// extractTableJS finds the first <table>, returns rows. If the first row is all
// <th>, it's used as headers and rows become objects {header: value}; otherwise
// an array of arrays. null if no table.
const extractTableJS = `(function(){
  var t=document.querySelector('table'); if(!t) return null;
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
})()`

// extractLinksJS returns [{text, href}] for every <a>, capped (a nav can have
// dozens; the cap tames a huge dump while the full set stays available via find).
const extractLinksJS = `(function(){
  var a=[]; var links=document.querySelectorAll('a');
  for(var i=0;i<links.length && a.length<200;i++){
    var x=links[i];
    a.push({text:(x.innerText||x.textContent||'').trim().slice(0,120), href:x.href});
  }
  return a;
})()`

// extractListJS picks the largest <ul>/<ol> (most direct <li> children) and
// returns its item texts. null if no list. Direct children only so a nested
// menu doesn't flatten into one list.
const extractListJS = `(function(){
  var lists=document.querySelectorAll('ul,ol'); var best=null, bestN=0;
  lists.forEach(function(l){var n=l.querySelectorAll(':scope > li').length; if(n>bestN){bestN=n;best=l}});
  if(!best) return null;
  var items=[]; var li=best.querySelectorAll(':scope > li');
  for(var i=0;i<li.length && i<200;i++){items.push((li[i].innerText||'').trim().slice(0,200))}
  return items;
})()`

// extractArticleJS returns the main content text: <article>, else <main>, else
// [role=main]. null if none (the caller points the agent to read for body text).
const extractArticleJS = `(function(){
  var a=document.querySelector('article')||document.querySelector('main')||document.querySelector('[role=main]');
  if(!a) return null;
  return a.innerText;
})()`

// extractCap bounds the JSON we return so a giant table/link dump can't blow up
// the response. The agent can narrow with a more specific page/section.
const extractCap = 16000

// Extract returns structured data for a kind: table (rows, JSON), links
// ([{text,href}], JSON), list (item texts, JSON), form ([{ref,role,name,value}],
// JSON from the cached AX tree - no CDP round-trip), article (main content text).
// One targeted DOM eval for the DOM kinds; form is free (cached tree).
func (s *Session) Extract(kind string) (string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "form":
		return s.extractForm(), nil
	case "table", "links", "list", "article":
	default:
		return "", fmt.Errorf("unknown extract kind %q (use: table | links | list | form | article)", kind)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return "", errors.New("no tab")
	}
	var js string
	switch kind {
	case "table":
		js = extractTableJS
	case "links":
		js = extractLinksJS
	case "list":
		js = extractListJS
	case "article":
		js = extractArticleJS
	}
	var raw string
	if err := chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
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
		return "", fmt.Errorf("no %s found on the page; use see/read for the raw content", kind)
	}
	if kind == "article" {
		// article comes back as a JSON-encoded string; decode to plain text,
		// truncate, and return readable (not JSON-quoted).
		var text string
		if err := json.Unmarshal([]byte(raw), &text); err != nil {
			return "", fmt.Errorf("extract article: %w", err)
		}
		return truncate(text, 12000), nil
	}
	return truncate(raw, extractCap), nil
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
