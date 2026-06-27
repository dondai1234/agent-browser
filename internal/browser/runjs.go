package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// jsHelpers is the injected DOM helper API exposed to every `js` call. It turns
// the agent's one-liners into clean JSON without per-call LLM cost and without
// the agent reinventing querySelector/innerText boilerplate every time. The
// helpers are the v3 "get data" hero: the agent writes
//
//	return {stars: text('#stars'), lang: attr('.lang','aria-label'), items: $$('li').map(text)}
//
// and gets JSON in one round-trip. Keep this list tight + general-purpose; every
// helper here is one a scraping agent reaches for constantly. `wait` is async so
// it yields to the event loop (a busy loop would block the XHR callbacks the
// condition is waiting on); the wrapper is async + AwaitPromise so `await` works.
const jsHelpers = `
function $(sel, ctx){ ctx = ctx || document; return ctx.querySelector(sel); }
function $$(sel, ctx){ ctx = ctx || document; return Array.prototype.slice.call(ctx.querySelectorAll(sel)); }
function _el(x){ return (typeof x === 'string') ? $(x) : x; }
function text(x){ var e = _el(x); if(!e) return ''; return (e.innerText || e.textContent || '').trim(); }
function attr(x, name){ var e = _el(x); return e ? (e.getAttribute(name) || '') : ''; }
function html(x){ var e = _el(x); return e ? (e.outerHTML || '') : ''; }
function visible(x){ var e = _el(x); if(!e) return false; var r = e.getBoundingClientRect(); if(r.width > 0 && r.height > 0) return true; var s = getComputedStyle(e); return s.display !== 'none' && s.visibility !== 'hidden' && s.opacity !== '0'; }
function data(x, k){ var e = _el(x); return e ? (e.dataset[k] || '') : ''; }
function prop(x, name){ var e = _el(x); return e ? e[name] : ''; }
function form(sel){ var f = $(sel); if(!f) return null; var out = {}; Array.prototype.forEach.call(f.querySelectorAll('input,select,textarea'), function(el){ if(!el.name) return; var v = el.type === 'checkbox' || el.type === 'radio' ? (el.checked ? el.value : '') : el.value; if(el.type !== 'radio' || el.checked) out[el.name] = v; }); return out; }
function meta(name){ var m = document.querySelector('meta[name=' + JSON.stringify(name) + '], meta[property=' + JSON.stringify(name) + ']'); return m ? (m.content || '') : ''; }
function table(sel){ var t = $(sel); if(!t) return null; var trs = t.querySelectorAll('tr'); var rows = []; trs.forEach(function(tr){ var c = []; tr.querySelectorAll('th,td').forEach(function(x){ c.push((x.innerText || '').trim()); }); if(c.length) rows.push(c); }); if(!rows.length) return null; var first = trs[0]; var isH = first && Array.prototype.every.call(first.children, function(x){ return x.tagName === 'TH'; }); if(isH){ var h = rows[0]; var out = []; for(var i = 1; i < rows.length; i++){ var o = {}; for(var j = 0; j < h.length; j++) o[h[j]] = rows[i][j] || ''; out.push(o); } return out; } return rows; }
function links(sel){ return (sel ? $$(sel) : $$('a')).map(function(a){ return { text: (a.innerText || a.textContent || '').trim().slice(0, 160), href: a.href }; }); }
function rect(x){ var e = _el(x); if(!e) return null; var r = e.getBoundingClientRect(); return { x: r.left, y: r.top, w: r.width, h: r.height }; }
function xpath(xp){ var it = document.evaluate(xp, document, null, XPathResult.ORDERED_NODE_SNAPSHOT_TYPE, null); var out = []; for(var i = 0; i < it.snapshotLength; i++) out.push(it.snapshotItem(i)); return out; }
function frame(id){ var f = Array.prototype.slice.call(document.querySelectorAll('iframe')).find(function(i){ return i.title === id || i.name === id || i.id === id; }); return f ? f.contentDocument : null; }
async function wait(fn, ms){ ms = ms || 5000; var d = Date.now() + ms; while(Date.now() < d){ if(fn()) return true; await new Promise(function(r){ setTimeout(r, 100); }); } return false; }
`

// returnRe matches a standalone `return` keyword so we can tell an expression
// (`document.title`) from a function body (`return {a:1}` / `const x=..; return x`).
// A bare expression gets wrapped as `return (expr)`; a body that already returns
// is used verbatim. Word-boundary match keeps false positives low; the rare case
// of "return" inside a string literal is acceptable (wrap your call in `return`).
var returnRe = regexp.MustCompile(`\breturn\b`)

// awaitSelJS is the optional pre-amble that waits for a selector to appear before
// running the user script - fuses "wait for the table to load" + "scrape it" into
// one js call. __sel is JSON-encoded by the caller. Resolves on the top document.
const awaitSelJS = `async function __awaitSel(){
  var sel = %s;
  var d = Date.now() + %d;
  while(Date.now() < d){ if(document.querySelector(sel)) return true; await new Promise(function(r){ setTimeout(r, 100); }); }
  return false;
}
if(!(await __awaitSel())) return {__error: "await: selector " + %s + " not found within %dms"};
`

// jsCap bounds the JSON a `js` call can return so a giant dump can't blow up the
// agent's context. The agent narrows with tighter selectors / maxChars.
const jsCap = 20000

// RunJS runs a JavaScript snippet in the current tab with the helper API in
// scope and returns the result as clean JSON (a bare string is unquoted to plain
// text; objects/arrays stay JSON). awaitSel (when set) waits for a CSS selector
// to appear first (up to awaitMs), fusing wait+scrape. The wrapper is async with
// AwaitPromise so the `wait` helper + `await` in user code work; a thrown error
// is captured as {__error: msg} and surfaced as a tool error (not a cryptic CDP
// exception). Gated by AllowEval. Caller-friendly: empty script -> error.
func (s *Session) RunJS(script, awaitSel string, awaitMs, maxChars int) (string, error) {
	if !s.AllowEval {
		return "", errors.New("js disabled: the server was started with --no-eval")
	}
	script = strings.TrimSpace(script)
	if script == "" {
		return "", errors.New("js: script required (a JS expression or a function body that returns; helpers $, $$, text, attr, table, links, wait, ... are in scope)")
	}
	if awaitMs <= 0 {
		awaitMs = 5000
	}
	cap := jsCap
	if maxChars > 0 {
		cap = maxChars
	}

	// Two forms: a bare expression is wrapped as `return (expr);`; a body that
	// already has its own `return` is used verbatim. If the expression form is a
	// SyntaxError (the script was actually a statement body without a return -
	// `let x=...; ...`, a top-level `throw`, a `for` loop), retry once with the
	// verbatim body so `js` is robust to both forms without the agent thinking
	// about it. The try/catch captures runtime throws as {__error: msg}.
	hasReturn := returnRe.MatchString(script)
	bodyExpr := "return (" + script + ");"
	bodyRaw := script
	primary := bodyRaw
	if !hasReturn {
		primary = bodyExpr
	}

	var preamble string
	if sel := strings.TrimSpace(awaitSel); sel != "" {
		selJSON, _ := json.Marshal(sel)
		preamble = fmt.Sprintf(awaitSelJS, string(selJSON), awaitMs, string(selJSON), awaitMs)
	}

	wrap := func(body string) string {
		return "(async function(){\n" + jsHelpers + preamble + "try {\n" + body + "\n} catch(e){ return {__error: e.message}; }\n})()"
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return "", errors.New("no tab")
	}
	raw, excDetail, err := s.evalWrappedLocked(t, wrap(primary))
	if err != nil {
		return "", err
	}
	if excDetail != "" && !hasReturn && isSyntaxError(excDetail) {
		// The expression wrap choked on a statement body - retry verbatim.
		raw, excDetail, err = s.evalWrappedLocked(t, wrap(bodyRaw))
		if err != nil {
			return "", err
		}
	}
	if excDetail != "" {
		return "", fmt.Errorf("js failed: %s", excDetail)
	}
	if raw == "" || raw == "undefined" || raw == "null" {
		return "", nil
	}
	// Surface a captured page-side runtime error as a tool error.
	if obj, ok := parseErrObject([]byte(raw)); ok {
		return "", fmt.Errorf("js: %s", obj)
	}
	// A bare string result is unquoted to plain text (document.title -> Title);
	// objects/arrays/numbers/bools keep their JSON so the shape is preserved.
	out := maybeUnquoteJSONString([]byte(raw))
	return truncate(out, cap), nil
}

// evalWrappedLocked runs one wrapped script and returns the raw JSON result, a
// non-empty excDetail when the page threw/failed to parse (the full message,
// not the truncated "Uncaught"), and a transport-level err. Caller holds s.mu.
func (s *Session) evalWrappedLocked(t *tab, wrapped string) (raw, excDetail string, err error) {
	e := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		res, exc, e := runtime.Evaluate(wrapped).WithReturnByValue(true).WithAwaitPromise(true).Do(ctx)
		if e != nil {
			return fmt.Errorf("js: %w", e)
		}
		if exc != nil {
			excDetail = excDetailText(exc)
			return nil
		}
		if res != nil && len(res.Value) > 0 {
			raw = string(res.Value)
		}
		return nil
	}))
	return raw, excDetail, e
}

// excDetailText extracts the fullest available message from a CDP exception:
// the Exception's Description ("Error: boom\n    at ...") carries the message;
// fall back to exc.Text. The raw exc.Text is often just "Uncaught".
func excDetailText(exc *runtime.ExceptionDetails) string {
	if exc == nil {
		return ""
	}
	if exc.Exception != nil {
		if exc.Exception.Description != "" {
			return exc.Exception.Description
		}
		if len(exc.Exception.Value) > 0 {
			var v any
			if json.Unmarshal(exc.Exception.Value, &v) == nil {
				return fmt.Sprintf("%v", v)
			}
		}
	}
	return exc.Text
}

// isSyntaxError reports whether an exception detail is a parse-time error (the
// signal to retry the expression-wrap as a verbatim body).
func isSyntaxError(detail string) bool {
	return strings.Contains(detail, "SyntaxError")
}

// parseErrObject reports whether b is a {"__error": "..."} object and returns
// the message. Lets RunJS turn a captured page-side throw into a tool error.
func parseErrObject(b []byte) (string, bool) {
	var probe struct {
		Error string `json:"__error"`
	}
	if err := json.Unmarshal(b, &probe); err == nil && probe.Error != "" {
		return probe.Error, true
	}
	return "", false
}
