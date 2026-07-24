package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
	"github.com/go-json-experiment/json/jsontext"

	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

// FormFillResult holds the outcome of a batch form fill.
type FormFillResult struct {
	Filled  int      // number of fields successfully filled
	Skipped int      // number of fields skipped (already in desired state)
	Errors  []string // per-field errors (empty if all succeeded)
	Valid   []string // validation errors detected after filling
	Delta   *snapshot.Delta
	After   *snapshot.Tree
}

// truthy returns true for values that mean "check/on" for checkboxes/switches.
func formTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "on", "check", "checked":
		return true
	case "false", "0", "no", "off", "uncheck", "unchecked":
		return false
	}
	return true // default: assume the agent wants it on
}

// sliderJS sets a range/slider value via the native value setter + input/change.
const sliderJS = `function(v) {
  var setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
  setter.call(this, String(v));
  this.dispatchEvent(new Event('input', {bubbles: true}));
  this.dispatchEvent(new Event('change', {bubbles: true}));
  return this.value;
}`

// checkboxCheckedJS reads the current checked state of a checkbox/switch.
const checkboxCheckedJS = `function() { return !!this.checked; }`

// validationErrorsJS scans the page for visible validation errors after a form
// fill: role=alert, .error, .invalid-feedback, .field-error, and HTML5
// validity messages on form inputs. Returns a compact string per error.
const validationErrorsJS = `(function(){
  var vis = function(el) {
    if (!el || !el.isConnected) return false;
    var s = getComputedStyle(el);
    if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
    var r = el.getBoundingClientRect();
    return r.width > 1 && r.height > 1;
  };
  var txt = function(el) { return (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim(); };
  var errs = [];
  // role=alert
  document.querySelectorAll('[role="alert"]').forEach(function(el) {
    if (vis(el)) { var t = txt(el); if (t && t.length < 200 && errs.indexOf(t) < 0) errs.push(t); }
  });
  // common error class patterns
  document.querySelectorAll('.error, .invalid-feedback, .field-error, .form-error, [class*="error" i]').forEach(function(el) {
    if (vis(el)) { var t = txt(el); if (t && t.length > 2 && t.length < 200 && errs.indexOf(t) < 0) errs.push(t); }
  });
  // HTML5 constraint validation on visible form inputs
  document.querySelectorAll('input, select, textarea').forEach(function(el) {
    if (!vis(el) || !el.willValidate || el.validity.valid) return;
    var msg = el.validationMessage;
    if (msg && errs.indexOf(msg) < 0) errs.push(msg);
  });
  return errs.length ? JSON.stringify(errs) : '';
})()`

// FormFill fills multiple form fields in one call. Each entry is {label: value}.
// For each label, goshawk resolves the field (a11y name + DOM fallback), detects
// the type, and performs the right action: fill for text inputs, select for
// dropdowns, toggle for checkboxes/switches, click for radios (value = option
// label), set for sliders, upload for files. Then re-snapshots once and checks
// for validation errors. Atomic (one lock). Returns a delta + per-field results.
func (s *Session) FormFill(fields map[string]string, settleMs int) (*FormFillResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.formFillLocked(fields, settleMs)
}

// formFillLocked is the lock-held implementation. Caller must hold s.mu.
func (s *Session) formFillLocked(fields map[string]string, settleMs int) (*FormFillResult, error) {
	if len(fields) == 0 {
		return nil, fmt.Errorf("form fill: no fields provided (pass fields={\"Label\": \"value\", ...})")
	}
	// Remove the s.mu.Lock/defer s.mu.Unlock() since the caller (Perform) already
	// holds the lock. This was a DEADLOCK: Perform acquires s.mu, then calls
	// FormFill which tried to acquire s.mu again. sync.Mutex is not reentrant.
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return nil, ErrNoSnapshot
	}
	before := t.tree
	startTs := time.Now()
	settle := time.Duration(settleMs) * time.Millisecond
	if settle <= 0 {
		settle = 50 * time.Millisecond // short per-field settle
	}

	// Sort keys for deterministic order.
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	result := &FormFillResult{}
	var lastErr string

	for _, label := range keys {
		value := fields[label]
		// 1. Resolve the field by intent (a11y name first, DOM fallback).
		resolved, candidates, rerr := resolveIntent(t.tree, label, value, "", 0)
		if rerr != nil {
			// Try the DOM fallback directly.
			if len(candidates) == 0 {
				domCand, _, domErr := s.resolveIntentDOMLocked(t, label, value, "", 0)
				if domErr == nil {
					_, actErr := s.actOnDOMLocked(t, domCand, value)
					if actErr != nil {
						result.Errors = append(result.Errors, fmt.Sprintf("%q: %v", label, actErr))
						lastErr = actErr.Error()
					} else {
						result.Filled++
					}
					time.Sleep(settle)
					continue
				}
				// For radios: the label is the group name, the value is the
				// option label. Try resolving the VALUE as a radio intent.
				if radioEl, radioCands, radioErr := resolveIntent(t.tree, value, "", "radio", 0); radioErr == nil {
					if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
						id, e := s.resolveRefLocked(ctx, radioEl.Ref)
						if e != nil {
							return e
						}
						return s.clickNodeLocked(ctx, id)
					})); err != nil {
						result.Errors = append(result.Errors, fmt.Sprintf("%q (radio %q): %v", label, value, err))
						lastErr = err.Error()
					} else {
						result.Filled++
					}
					time.Sleep(settle)
					continue
				} else if len(radioCands) > 0 {
					// Ambiguous radio match: try auto-picking the most visible candidate.
					if picked, perr := s.pickMostVisibleLocked(t, radioCands); perr == nil {
						if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
							id, e := s.resolveRefLocked(ctx, picked.Ref)
							if e != nil {
								return e
							}
							return s.clickNodeLocked(ctx, id)
						})); err != nil {
							result.Errors = append(result.Errors, fmt.Sprintf("%q (radio %q): %v", label, value, err))
							lastErr = err.Error()
						} else {
							result.Filled++
						}
						time.Sleep(settle)
						continue
					}
					result.Errors = append(result.Errors, fmt.Sprintf("%q: ambiguous radio match for %q (%d options)", label, value, len(radioCands)))
					lastErr = "ambiguous"
				} else {
					result.Errors = append(result.Errors, fmt.Sprintf("%q: %v", label, rerr))
					lastErr = rerr.Error()
				}
				continue
			}
			// Ambiguous: try auto-picking the most visible candidate before
			// falling back to the manual disambiguation error.
			if picked, perr := s.pickMostVisibleLocked(t, candidates); perr == nil {
				resolved = picked
				goto fieldResolved
			}
			result.Errors = append(result.Errors, fmt.Sprintf("%q: %v (%d candidates)", label, rerr, len(candidates)))
			lastErr = rerr.Error()
			continue
		}

	fieldResolved:
		// 2. Perform the action based on the resolved element's role + tag/type.
		actErr := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			id, e := s.resolveRefLocked(ctx, resolved.Ref)
			if e != nil {
				return e
			}
			// Probe tag/type for type-specific handling.
			var tagType string
			if res, _, e := runtime.CallFunctionOn(`function(){return this.tagName + '/' + (this.type||''); }`).WithObjectID(id).Do(ctx); e == nil && res != nil && len(res.Value) > 0 {
				_ = json.Unmarshal(res.Value, &tagType)
			}
			tag := strings.SplitN(tagType, "/", 2)[0]
			typ := ""
			if len(strings.SplitN(tagType, "/", 2)) > 1 {
				typ = strings.ToLower(strings.SplitN(tagType, "/", 2)[1])
			}

			switch {
			case isFillableRole(resolved.Role):
				return s.fillNodeLocked(ctx, id, value)

			case resolved.Role == "combobox":
				switch {
				case tag == "SELECT":
					return s.selectNodeLocked(ctx, id, value)
				case tag == "INPUT" || tag == "TEXTAREA":
					return s.fillNodeLocked(ctx, id, value)
				default:
					_, e := s.openSelectByIDLocked(ctx, id, value)
					return e
				}

			case resolved.Role == "checkbox" || resolved.Role == "switch":
				want := formTruthy(value)
				// Read the current checked state.
				res, _, e := runtime.CallFunctionOn(checkboxCheckedJS).WithReturnByValue(true).WithObjectID(id).Do(ctx)
				if e != nil {
					return fmt.Errorf("check state: %w", e)
				}
				currentlyChecked := false
				if res != nil && len(res.Value) > 0 {
					_ = json.Unmarshal(res.Value, &currentlyChecked)
				}
				if currentlyChecked == want {
					result.Skipped++ // already in the desired state
					return nil
				}
				return s.clickNodeLocked(ctx, id)

			case resolved.Role == "radio":
				// The value is the option label; resolve it as a radio and click.
				// If we got here, the label itself matched a radio, so just click it.
				return s.clickNodeLocked(ctx, id)

			case resolved.Role == "slider":
				// Set the value via the native setter + input/change events.
				arg, _ := json.Marshal(value)
				_, exc, e := runtime.CallFunctionOn(sliderJS).
					WithObjectID(id).
					WithArguments([]*runtime.CallArgument{{Value: jsontext.Value(arg)}}).
					Do(ctx)
				if e != nil {
					return fmt.Errorf("slider: %w", e)
				}
				if exc != nil {
					return fmt.Errorf("slider failed: %s", exc.Text)
				}
				return nil

			case resolved.Role == "button" || resolved.Role == "link":
				// Click the button (value is ignored for buttons/links).
				return s.clickNodeLocked(ctx, id)

			case tag == "INPUT" && typ == "file":
				// File upload: value is comma-separated paths.
				paths := strings.Split(value, ",")
				for i := range paths {
					paths[i] = strings.TrimSpace(paths[i])
				}
				return domSetFileInputFiles(ctx, id, paths)

			default:
				// Fallback: try fill (for text-like) or click.
				if tag == "INPUT" || tag == "TEXTAREA" {
					return s.fillNodeLocked(ctx, id, value)
				}
				return s.clickNodeLocked(ctx, id)
			}
		}))

		if actErr != nil {
			result.Errors = append(result.Errors, fmt.Sprintf("%q: %v", label, actErr))
			lastErr = actErr.Error()
		} else {
			result.Filled++
		}
		time.Sleep(settle)
	}

	// 3. Re-snapshot once.
	delta, after, ferr := s.finishMutationLocked(t, before, startTs, fmt.Sprintf("form fill (%d fields)", len(fields)))
	if ferr != nil {
		result.Delta = delta
		return result, ferr
	}
	result.Delta = delta
	result.After = after

	// 4. Check for validation errors.
	if after != nil && after.URL != "" {
		var valErrs string
		_ = s.run(t, chromedp.Evaluate(validationErrorsJS, &valErrs))
		if valErrs != "" && valErrs != "null" {
			var errs []string
			if json.Unmarshal([]byte(valErrs), &errs) == nil && len(errs) > 0 {
				result.Valid = errs
			}
		}
	}

	// Augment the verdict with form-fill results.
	if delta != nil {
		if len(result.Errors) > 0 {
			delta.Verdict = fmt.Sprintf("form fill: %d filled, %d errors", result.Filled, len(result.Errors))
		} else if len(result.Valid) > 0 {
			delta.Verdict = fmt.Sprintf("form fill: %d filled, %d validation errors", result.Filled, len(result.Valid))
		} else {
			delta.Verdict = fmt.Sprintf("form fill: %d filled, %d skipped", result.Filled, result.Skipped)
		}
	}

	_ = lastErr // used for debugging; the per-field errors are in result.Errors
	return result, nil
}

// domSetFileInputFiles sets files on a file input by remote object id.
func domSetFileInputFiles(ctx context.Context, id runtime.RemoteObjectID, paths []string) error {
	return dom.SetFileInputFiles(paths).WithObjectID(id).Do(ctx)
}
