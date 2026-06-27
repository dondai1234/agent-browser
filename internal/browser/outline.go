package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// outlineJS walks the top document (and same-origin iframes) for the semantic
// containers an agent scrapes - headings, tables, lists, forms, article/main,
// and landmark regions - and emits one line per container with a WORKING CSS
// selector + a label + a count. This is the missing discovery view: the agent
// reads `outline` to pick the selectors it then feeds to `js`, instead of
// guessing selectors blindly and ping-ponging see/extract/read until one hits.
//
// Output lines (compact, ~1 line per container, capped at outlineCap):
//
//	h2 "#repo-content-pinned-container h2" "Releases"
//	table "table Releases" (8 rows)
//	ul ".release-list" (12 items)
//	form "form#login" (3 fields)
//	article "article"
//	region "nav[aria-label='Repo']" "Repository navigation"
//
// uniqueSel is inlined (self-contained - outline does not run through the js
// helper wrapper, so it can't rely on $/$$/frame being in scope).
const outlineJS = `(function(){
  function uniqueSel(el){
    if(!el || el.nodeType !== 1) return '';
    if(el.id){ try { return '#'+CSS.escape(el.id); } catch(e){} }
    var parts = [];
    var cur = el;
    while(cur && cur.nodeType === 1 && cur !== document.documentElement){
      var p = cur.parentNode;
      if(!p){ parts.unshift(cur.tagName.toLowerCase()); break; }
      var idx = Array.prototype.indexOf.call(p.children, cur) + 1;
      parts.unshift(cur.tagName.toLowerCase() + ':nth-child(' + idx + ')');
      cur = p;
      if(cur && cur.id){ try { parts.unshift('#' + CSS.escape(cur.id)); break; } catch(e){} }
    }
    return parts.join(' > ');
  }
  function label(el){
    var l = el.getAttribute('aria-label') || el.getAttribute('title') || '';
    if(!l){ var h = el.querySelector('h1,h2,h3,h4,h5,h6'); if(h) l = (h.textContent || '').trim().slice(0, 60); }
    if(!l && el.id) l = el.id;
    return l.trim();
  }
  function esc(s){ return '"' + String(s).replace(/"/g, '\\"') + '"'; }
  var out = [];
  var seen = {};
  function push(line){ if(!seen[line]){ seen[line] = 1; out.push(line); } }
  function scan(root, prefix){
    var heads = root.querySelectorAll('h1,h2,h3,h4,h5,h6');
    for(var i = 0; i < heads.length && out.length < 120; i++){
      var h = heads[i];
      push(h.tagName.toLowerCase() + ' ' + esc(uniqueSel(h)) + ' ' + esc((h.textContent || '').trim().slice(0, 80)));
    }
    var tables = root.querySelectorAll('table');
    for(var i = 0; i < tables.length && out.length < 120; i++){
      var t = tables[i]; var n = t.rows ? t.rows.length : 0;
      push('table ' + esc(uniqueSel(t)) + ' (' + n + ' rows)');
    }
    var lists = root.querySelectorAll('ul,ol');
    for(var i = 0; i < lists.length && out.length < 120; i++){
      var l = lists[i]; var n = l.querySelectorAll(':scope > li').length;
      if(n >= 3) push(l.tagName.toLowerCase() + ' ' + esc(uniqueSel(l)) + ' (' + n + ' items)');
    }
    var forms = root.querySelectorAll('form');
    for(var i = 0; i < forms.length && out.length < 120; i++){
      var f = forms[i]; var n = f.querySelectorAll('input,select,textarea').length;
      if(n > 0) push('form ' + esc(uniqueSel(f)) + ' (' + n + ' fields)');
    }
    var arts = root.querySelectorAll('article,main,[role=main]');
    for(var i = 0; i < arts.length && out.length < 120; i++){
      push((arts[i].getAttribute('role') || arts[i].tagName.toLowerCase()) + ' ' + esc(uniqueSel(arts[i])));
    }
    var regions = root.querySelectorAll('[role=region],[role=navigation],[role=banner],[role=complementary],[role=search],[role=contentinfo],nav,header,footer,aside,section');
    for(var i = 0; i < regions.length && out.length < 120; i++){
      var r = regions[i]; var l = label(r);
      var name = r.getAttribute('role') || r.tagName.toLowerCase();
      if(l) push(name + ' ' + esc(uniqueSel(r)) + ' ' + esc(l));
    }
  }
  scan(document, '');
  var ifs = document.querySelectorAll('iframe');
  for(var i = 0; i < ifs.length; i++){
    try {
      var d = ifs[i].contentDocument;
      if(d && d.body){ scan(d, ifs[i].title || ('iframe' + i)); }
    } catch(e){}
  }
  return out.slice(0, 100).join('\n');
})()`

// outlineCap bounds the outline response (containers, not content - 100 lines is
// a generous page; the agent narrows by region if needed).
const outlineCap = 8000

// Outline returns the page's semantic skeleton: each container (heading, table,
// list, form, article, region) with a working CSS selector + label + count, so
// the agent can pick selectors for `js` without guessing. One DOM eval; walks
// same-origin iframes. Empty string (with a hint) if the page has no containers.
func (s *Session) Outline() (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return "", errors.New("no tab")
	}
	var raw string
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		res, exc, err := runtime.Evaluate(outlineJS).WithReturnByValue(true).Do(ctx)
		if err != nil {
			return fmt.Errorf("outline: %w", err)
		}
		if exc != nil {
			return fmt.Errorf("outline failed: %s", exc.Text)
		}
		if res != nil && len(res.Value) > 0 {
			var s2 string
			if json.Unmarshal(res.Value, &s2) == nil {
				raw = s2
			}
		}
		return nil
	})); err != nil {
		return "", err
	}
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "no semantic containers found (the page may be empty or a challenge; try see level=refs)", nil
	}
	return truncate(raw, outlineCap), nil
}
