package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// collectJS reads __fields ({label: selector}) + __attrs ({label: attrName} or
// null) and returns {label: value}: each selector's first match's innerText,
// or the named attribute when an attr is given for that label, or null when the
// selector doesn't match. One DOM pass, structured + labeled output - the
// multi-value pull without the agent writing JS (which eval requires).
const collectJS = `(function(){
  var fields = __fields, attrs = __attrs || {};
  var out = {};
  for (var label in fields) {
    var sel = fields[label];
    try {
      var el = document.querySelector(sel);
      if (!el) { out[label] = null; continue; }
      var a = attrs[label];
      if (a) { out[label] = el.getAttribute(a); }
      else { out[label] = (el.innerText || el.textContent || '').trim(); }
    } catch(e) { out[label] = null; }
  }
  return out;
})()`

// collectCap bounds the JSON we return so a huge matched region can't blow up
// the response. Each value is also trimmed to collectItemCap.
const (
	collectCap     = 16000
	collectItemCap = 6000
)

// Collect pulls named values off the page in one DOM pass. fields is a
// {label: CSS selector} map; the result is {label: text} JSON. attrs (optional)
// is a {label: attribute name} map - for a field whose value should be an
// attribute instead of text (e.g. {"link": "href"}). A selector that doesn't
// match yields null for that label (not an error), so the agent gets a partial
// result it can branch on. One call replaces N extract calls or a custom eval.
func (s *Session) Collect(fields, attrs map[string]string) (string, error) {
	if len(fields) == 0 {
		return "", errors.New("collect requires fields: a {label: CSS selector} map of the values to pull")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return "", errors.New("no tab")
	}
	fieldsJSON, _ := json.Marshal(fields)
	attrsJSON, _ := json.Marshal(attrs)
	script := "var __fields=" + string(fieldsJSON) + "; var __attrs=" + string(attrsJSON) + "; " + collectJS
	var raw string
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		res, exc, err := runtime.Evaluate(script).WithReturnByValue(true).Do(ctx)
		if err != nil {
			return fmt.Errorf("collect: %w", err)
		}
		if exc != nil {
			return fmt.Errorf("collect failed: %s", exc.Text)
		}
		if res != nil && len(res.Value) > 0 {
			raw = string(res.Value)
		}
		return nil
	})); err != nil {
		return "", err
	}
	if raw == "" || raw == "null" {
		return "", errors.New("collect returned no data (the page may be empty or not loaded)")
	}
	// Trim each string value so one huge region can't dominate the response,
	// then cap the whole JSON. Non-string values (null) pass through.
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return truncate(raw, collectCap), nil
	}
	for k, v := range out {
		if str, ok := v.(string); ok && len(str) > collectItemCap {
			out[k] = truncate(str, collectItemCap)
		}
	}
	b, err := json.Marshal(out)
	if err != nil {
		return truncate(raw, collectCap), nil
	}
	return truncate(string(b), collectCap), nil
}
