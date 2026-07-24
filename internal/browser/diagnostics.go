package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

// Self-Diagnosing Verdicts: when an action doesn't clearly succeed, the verdict
// automatically explains WHY and suggests WHAT TO DO NEXT. Three layers:
//
// Layer 1 ("Did you mean?"): when resolveIntent can't find an element, the error
// includes the top 3 closest matches by name similarity (Levenshtein). The agent
// can retry with the right name immediately, no see/find needed.
//
// Layer 2 ("What blocked it?"): when an action was executed but produced no
// confirmed effect (uncertain/likely), a lightweight DOM diagnostic checks for
// visible error messages, HTML5 validation failures, and CSS modals that the
// a11y tree might miss.
//
// Layer 3 ("What now?"): the diagnostic results are appended to the verdict as
// an actionable suggestion ("errors visible: Email is required. Fix and retry"
// or "modal 'Login' opened - call see to inspect").
//
// Zero extra tool calls. No LLM in the loop. Pure heuristics. Runs only when
// the action didn't clearly succeed (confirmed actions skip the diagnostic).

// closestMatches returns a formatted "did you mean?" string with the top n
// elements on the page whose names are most similar to intent. Uses normalized
// Levenshtein distance (1 - dist/maxLen) as the similarity score. Only
// includes elements with similarity >= 0.3 (30%). Returns "" if no candidates.
func closestMatches(tree *snapshot.Tree, intent string, n int) string {
	needle := strings.ToLower(strings.TrimSpace(intent))
	if needle == "" || tree == nil {
		return ""
	}
	type cand struct {
		el  snapshot.Element
		sim float64
	}
	var cands []cand
	for _, el := range tree.Elems {
		name := strings.TrimSpace(strings.ToLower(el.Name))
		if name == "" {
			continue
		}
		d := levenshtein(needle, name)
		maxLen := len(needle)
		if len(name) > maxLen {
			maxLen = len(name)
		}
		sim := 1.0 - float64(d)/float64(maxLen)
		if sim >= 0.3 {
			cands = append(cands, cand{el, sim})
		}
	}
	if len(cands) == 0 {
		return ""
	}
	sort.SliceStable(cands, func(i, j int) bool { return cands[i].sim > cands[j].sim })
	if len(cands) > n {
		cands = cands[:n]
	}
	var parts []string
	for _, c := range cands {
		parts = append(parts, fmt.Sprintf("%q (%s, %s)", c.el.Name, c.el.Role, c.el.Ref))
	}
	return "did you mean: " + strings.Join(parts, ", ") + "?"
}

// levenshtein computes the edit distance between two strings. Used by
// closestMatches for fuzzy name matching when resolveIntent finds no match.
func levenshtein(a, b string) int {
	la, lb := len(a), len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	prev := make([]int, lb+1)
	curr := make([]int, lb+1)
	for j := 0; j <= lb; j++ {
		prev[j] = j
	}
	for i := 1; i <= la; i++ {
		curr[0] = i
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			curr[j] = prev[j-1] + cost
			if prev[j]+1 < curr[j] {
				curr[j] = prev[j] + 1
			}
			if curr[j-1]+1 < curr[j] {
				curr[j] = curr[j-1] + 1
			}
		}
		prev, curr = curr, prev
	}
	return prev[lb]
}

// actionDiagnosticsJS scans the page for signs of failure after an action that
// produced no confirmed effect. Checks: visible error messages (role=alert,
// .error, .invalid-feedback, etc.), HTML5 validation errors on form inputs,
// and CSS modals/dialogs that the a11y tree might miss. Returns a compact JSON
// object. Deduplicates error text. Caps at 3 errors + 1 modal.
const actionDiagnosticsJS = `(() => {
  var result = { errors: [], modal: null };
  var seen = new Set();
  var errorSelectors = '[role="alert"], .error, .invalid-feedback, .field-error, .error-message, .form-error, .alert-danger, .alert:not(.alert-success), [data-error], .text-danger, .text-error';
  document.querySelectorAll(errorSelectors).forEach(function(el) {
    var t = (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
    if (t && t.length < 200 && el.offsetParent !== null && !seen.has(t)) {
      seen.add(t);
      result.errors.push(t);
    }
  });
  document.querySelectorAll('input, select, textarea').forEach(function(el) {
    if (el.willValidate && !el.checkValidity() && el.validationMessage) {
      var msg = el.validationMessage;
      if (!seen.has(msg)) {
        seen.add(msg);
        result.errors.push(msg);
      }
    }
  });
  var modal = document.querySelector('[role="dialog"], dialog, .modal, .modal-dialog');
  if (modal && modal.offsetParent !== null) {
    result.modal = modal.getAttribute('aria-label') || modal.id || (modal.querySelector('h1,h2,h3,h4,.modal-title') || {}).textContent || 'dialog';
    if (result.modal) result.modal = result.modal.trim().slice(0, 60);
  }
  result.errors = result.errors.slice(0, 3);
  return result;
})()`

// actionDiagnosticResult is the JSON struct returned by actionDiagnosticsJS.
type actionDiagnosticResult struct {
	Errors []string `json:"errors"`
	Modal  string   `json:"modal"`
}

// runActionDiagnosticsLocked runs the diagnostic JS on the current tab and
// returns a formatted, actionable string to append to the verdict. Returns ""
// if no errors or modals were found (nothing to diagnose). Caller must hold
// s.mu. One Evaluate call; lightweight.
func (s *Session) runActionDiagnosticsLocked(t *tab) string {
	if t == nil {
		return ""
	}
	var diag actionDiagnosticResult
	_ = s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		res, exc, err := runtime.Evaluate(actionDiagnosticsJS).WithReturnByValue(true).Do(ctx)
		if err != nil || exc != nil {
			return nil
		}
		if res != nil && len(res.Value) > 0 {
			_ = json.Unmarshal(res.Value, &diag)
		}
		return nil
	}))
	return formatDiagnostics(diag)
}

// formatDiagnostics turns the diagnostic result into an actionable verdict
// suffix. Returns "" when there's nothing to report.
func formatDiagnostics(diag actionDiagnosticResult) string {
	if len(diag.Errors) == 0 && diag.Modal == "" {
		return ""
	}
	var parts []string
	if len(diag.Errors) > 0 {
		quoted := make([]string, len(diag.Errors))
		for i, e := range diag.Errors {
			quoted[i] = fmt.Sprintf("%q", e)
		}
		parts = append(parts, "errors visible: "+strings.Join(quoted, ", ")+". Fix those fields and retry")
	}
	if diag.Modal != "" {
		parts = append(parts, fmt.Sprintf("modal %q opened - call see to inspect", diag.Modal))
	}
	return strings.Join(parts, "; ")
}
