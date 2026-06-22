package mcpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// servePage serves one HTML document at / and returns its URL.
func servePage(t *testing.T, html string) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, html)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestV209SelectNoMatch: select with a value that matches no option must report
// an error (not silently no-op). select with a real option still works.
func TestV209SelectNoMatch(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	url := servePage(t, `<!doctype html><html><head><title>Sel</title></head><body>
		<h1>Plans</h1>
		<label for="s">Plan</label>
		<select id="s" name="plan"><option value="free">Free</option><option value="pro">Pro</option></select>
		</body></html>`)
	callTool(t, sess, ctx, "navigate", map[string]any{"url": url})

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "act", Arguments: map[string]any{"intent": "Plan", "value": "Nonexistent"}})
	if err != nil {
		t.Fatalf("act select call: %v", err)
	}
	txt := contentText(res)
	if !res.IsError || !strings.Contains(txt, "no option matching") {
		t.Errorf("select no-match should be isError with 'no option matching', got err=%v: %s", res.IsError, oneLine(txt))
	}

	// A real option still selects cleanly (sanity).
	ok, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "act", Arguments: map[string]any{"intent": "Plan", "value": "Pro"}})
	if err != nil {
		t.Fatalf("act select real call: %v", err)
	}
	if resIsErr(ok) {
		t.Errorf("select Pro should succeed, got: %s", oneLine(contentText(ok)))
	}
}

// TestV209PressKeyRef: press_key with a ref focuses that element first, so Enter
// on a chosen input submits its form (native default), revealing a result.
func TestV209PressKeyRef(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	url := servePage(t, `<!doctype html><html><head><title>Form</title></head><body>
		<h1>Search</h1>
		<form id="f" onsubmit="document.getElementById('r').hidden=false; document.getElementById('r').focus(); return false;">
			<input id="q" name="q" type="text" aria-label="Query">
		</form>
		<button id="r" hidden>Done</button>
		</body></html>`)
	callTool(t, sess, ctx, "navigate", map[string]any{"url": url})

	// Find the Query input ref via find.
	found := callTool(t, sess, ctx, "find", map[string]any{"role": "textbox", "text": "Query"})
	ref := firstRef(found)
	if ref == "" {
		t.Fatalf("could not find the Query textbox ref in:\n%s", found)
	}
	out := callTool(t, sess, ctx, "press_key", map[string]any{"key": "Enter", "ref": ref})
	t.Logf("press_key Enter ref=%s:\n%s", ref, out)
	if !strings.Contains(out, "Done") {
		t.Errorf("press_key Enter on the input should submit the form + reveal 'Done', got:\n%s", out)
	}
}

// TestV209WaitDefaultSeconds: wait with a condition but seconds=0 defaults to a
// real budget (not an instant timeout), so an async condition can satisfy it.
func TestV209WaitDefaultSeconds(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	url := servePage(t, `<!doctype html><html><head><title>Wait</title></head><body>
		<h1>Slow</h1>
		<script>setTimeout(function(){ var d=document.createElement('p'); d.id='ready'; d.textContent='READY'; document.body.appendChild(d); }, 1500);</script>
		</body></html>`)
	callTool(t, sess, ctx, "navigate", map[string]any{"url": url})
	// seconds=0 + a text condition -> defaults to 10s; READY appears at 1.5s.
	out, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "wait", Arguments: map[string]any{"seconds": 0, "text": "READY"}})
	if err != nil {
		t.Fatalf("wait call: %v", err)
	}
	txt := contentText(out)
	if resIsErr(out) || !strings.Contains(txt, "READY") {
		t.Errorf("wait seconds=0 text=READY should default to a real budget + match, got err=%v: %s", resIsErr(out), oneLine(txt))
	}
}

// TestV209EvalUnquote: eval of a string result returns the unquoted string (so
// document.title -> Title, not "Title"). Object results keep their JSON braces.
func TestV209EvalUnquote(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	url := servePage(t, `<!doctype html><html><head><title>My Title</title></head><body><h1>x</h1></body></html>`)
	callTool(t, sess, ctx, "navigate", map[string]any{"url": url})

	title := callTool(t, sess, ctx, "eval", map[string]any{"script": "document.title"})
	t.Logf("eval document.title -> %q", title)
	if title != "My Title" {
		t.Errorf("eval document.title should be the unquoted 'My Title', got %q (unquote not applied?)", title)
	}

	obj := callTool(t, sess, ctx, "eval", map[string]any{"script": "({a:1})"})
	t.Logf("eval ({a:1}) -> %q", obj)
	if !strings.HasPrefix(strings.TrimSpace(obj), "{") {
		t.Errorf("eval of an object should keep JSON braces, got %q", obj)
	}
}

// TestV209HistoryRecordsFailures: a failed act (no match) is recorded in history
// so history errors=true surfaces it, not just CHALLENGE verdicts.
func TestV209HistoryRecordsFailures(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	url := servePage(t, `<!doctype html><html><head><title>H</title></head><body><h1>Nothing here</h1></body></html>`)
	callTool(t, sess, ctx, "navigate", map[string]any{"url": url})

	// A no-match act (no such control on the page).
	if _, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "act", Arguments: map[string]any{"intent": "NonexistentControl"}}); err != nil {
		t.Fatalf("act call: %v", err)
	}

	hist := callTool(t, sess, ctx, "history", map[string]any{"errors": true})
	t.Logf("history errors=true:\n%s", hist)
	if !strings.Contains(hist, "error:") || !strings.Contains(hist, "NonexistentControl") {
		t.Errorf("history errors=true should list the failed act (error: ... NonexistentControl), got:\n%s", hist)
	}
}

// TestV209ResetPreservesTabs: reset on an alive browser re-navigates only the
// current tab; other tabs (and their pages/logins) are kept.
func TestV209ResetPreservesTabs(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	pageA := servePage(t, `<!doctype html><html><head><title>Page A</title></head><body><h1>AAA</h1></body></html>`)
	pageB := servePage(t, `<!doctype html><html><head><title>Page B</title></head><body><h1>BBB</h1></body></html>`)

	callTool(t, sess, ctx, "navigate", map[string]any{"url": pageA})
	callTool(t, sess, ctx, "tabs", map[string]any{"action": "new", "url": pageB})

	// Reset the current tab (t2, page B) with no url -> about:blank; t1 (page A) kept.
	reset := callTool(t, sess, ctx, "reset", map[string]any{})
	t.Logf("reset:\n%s", reset)
	list := callTool(t, sess, ctx, "tabs", map[string]any{"action": "list"})
	t.Logf("tabs after reset:\n%s", list)
	if !strings.Contains(list, "t1") || !strings.Contains(list, "t2") {
		t.Errorf("reset should keep both tabs, got:\n%s", list)
	}
	// t1 must still be on page A (its URL/title preserved).
	if !strings.Contains(list, pageA) && !strings.Contains(list, "Page A") {
		t.Errorf("reset should preserve t1's page A, got:\n%s", list)
	}
}

// firstRef extracts the first [rN] ref from a find/see response.
func firstRef(s string) string {
	for i := 0; i+2 < len(s); i++ {
		if s[i] == '[' && s[i+1] == 'r' && s[i+2] >= '0' && s[i+2] <= '9' {
			j := i + 3
			for j < len(s) && s[j] >= '0' && s[j] <= '9' {
				j++
			}
			if j < len(s) && s[j] == ']' {
				return s[i+1 : j]
			}
		}
	}
	return ""
}
