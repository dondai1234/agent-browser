package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/runtime"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

// openSelectComboboxJS is the in-page dance for a button+listbox dropdown (the
// pattern aria-haspopup="listbox" on a button/div that opens a popup of
// role=option). Unlike a native <select> (selectJS) or an autocomplete input
// (fillJS), selecting an option here REQUIRES: open the popup, wait for the
// listbox, click the matching option. It runs as one CallFunctionOn on the
// trigger element so there's no round-trip per step.
//
// Match order: exact (case-insensitive) > substring. Returns the selected
// option's text, or null if no listbox/option matched (so the Go side reports a
// real error instead of a silent no-op - the silent-failure guard).
const openSelectComboboxJS = `function(v) {
  var dl = Date.now() + 2500;
  var fire = function(el) {
    try { el.focus({preventScroll:true}); } catch(e) {}
    try { el.scrollIntoView({block:'center'}); } catch(e) {}
    var evts = ['pointerdown','mousedown','pointerup','mouseup','click'];
    for (var j=0;j<evts.length;j++) { try { el.dispatchEvent(new MouseEvent(evts[j],{bubbles:true,cancelable:true,view:window,buttons:1})); } catch(e) {} }
    try { el.click(); } catch(e) {}
  };
  fire(this);
  // resolve the controlled listbox: aria-controls -> aria-owns -> nearest visible [role=listbox]
  var lb = null, ctrl = this.getAttribute('aria-controls'), owns = this.getAttribute('aria-owns');
  while (Date.now() < dl && !lb) {
    if (ctrl) lb = document.getElementById(ctrl);
    if (!lb && owns) { var o = document.getElementById(owns); if (o) lb = o.querySelector('[role="listbox"]') || (o.getAttribute && o.getAttribute('role')==='listbox' ? o : null); }
    if (!lb) lb = document.querySelector('[role="listbox"]:not([aria-hidden="true"]):not([hidden])');
    if (!lb) { /* poll */ var t = Date.now(); while (Date.now()-t < 80) {} }
  }
  if (!lb) return null;
  // wait for at least one option
  var opts = [];
  while (Date.now() < dl) { opts = lb.querySelectorAll('[role="option"]'); if (opts.length) break; var t = Date.now(); while (Date.now()-t < 80) {} }
  if (!opts.length) return null;
  var V = String(v||'').toLowerCase();
  var pick = function(t){ return (t||'').replace(/\s+/g,' ').trim().toLowerCase(); };
  var match = null;
  for (var i=0;i<opts.length;i++){ if (pick(opts[i].innerText||opts[i].textContent)===V){ match=opts[i]; break; } }
  if (!match) for (var i=0;i<opts.length;i++){ var t=pick(opts[i].innerText||opts[i].textContent); if (t.indexOf(V)>=0){ match=opts[i]; break; } }
  if (!match) return null;
  fire(match);
  return (match.innerText||match.textContent||'').replace(/\s+/g,' ').trim();
}`

// isListboxCombobox reports whether an element is a button/div combobox that
// opens a listbox popup (aria-haspopup="listbox"), detectable from the a11y
// props at no CDP cost. This is the custom-dropdown case: selecting an option
// needs an open-then-click dance, not a fill or a native <select> select.
func isListboxCombobox(el snapshot.Element) bool {
	if el.Role == "combobox" {
		return true
	}
	hp := el.Props[accessibility.PropertyNameHasPopup]
	return strings.EqualFold(hp, "listbox") || strings.Contains(strings.ToLower(hp), "listbox")
}

// openSelectComboboxLocked runs the open-select dance on a listbox-combobox
// trigger by ref, returning the selected option's text. Caller must hold s.mu.
func (s *Session) openSelectComboboxLocked(ctx context.Context, t *tab, ref, value string) (string, error) {
	id, err := s.resolveRefLocked(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("resolve combobox %q: %w", ref, err)
	}
	return s.openSelectByIDLocked(ctx, id, value)
}

// openSelectByIDLocked runs the open-select dance on a trigger given its remote
// object id (the shared core for the ref + selector + id call sites). Returns
// the selected option's text, or an error if no listbox/option matched. Caller
// must hold s.mu; ctx carries the executor.
func (s *Session) openSelectByIDLocked(ctx context.Context, id runtime.RemoteObjectID, value string) (string, error) {
	arg, _ := json.Marshal(value)
	res, exc, err := runtime.CallFunctionOn(openSelectComboboxJS).
		WithObjectID(id).
		WithReturnByValue(true).
		WithArguments([]*runtime.CallArgument{{Value: jsontext.Value(arg)}}).
		Do(ctx)
	if err != nil {
		return "", fmt.Errorf("combobox open-select: %w", err)
	}
	if exc != nil {
		return "", fmt.Errorf("combobox open-select failed: %s", exc.Text)
	}
	if res == nil || len(res.Value) == 0 || string(res.Value) == "null" {
		return "", fmt.Errorf("combobox open-select: no listbox/option matched %q (open the dropdown with act, then see level=refs role=option to read the option names)", value)
	}
	var sel string
	_ = json.Unmarshal(res.Value, &sel)
	return sel, nil
}
