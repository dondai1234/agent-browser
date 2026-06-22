package mcpserver

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// serveForm serves an HTML form with a mix of label qualities to reproduce the
// "act label matching may struggle with certain form layouts" report without
// depending on network/flaky sites.
func serveForm(t *testing.T) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Form Layouts</title></head><body>
		<h1>Checkout</h1>
		<form id="f" onsubmit="return false">
			<!-- A: properly associated label -->
			<label for="fullname">Full name</label>
			<input id="fullname" name="fullname" type="text">

			<!-- B: placeholder only, no label -->
			<input name="email" type="email" placeholder="Email address">

			<!-- C: aria-label only -->
			<input name="phone" type="tel" aria-label="Phone number">

			<!-- D: wrapping label (implicit association) -->
			<label>Company <input name="company" type="text"></label>

			<!-- E: no label, no placeholder, no aria - only name/id attrs -->
			<input name="custcode" id="custcode" type="text">

			<!-- F: a <select> with a real label -->
			<label for="plan">Plan</label>
			<select id="plan" name="plan">
				<option value="free">Free</option>
				<option value="pro">Pro</option>
			</select>

			<button type="submit" id="submit">Place order</button>
		</form>
		</body></html>`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// TestReproActLabelLayouts reproduces the opencode "act label matching may
// struggle with certain form layouts" finding. Each intent below targets a
// different label-quality case; act should resolve ALL of them, not just the
// ones with a proper <label for>. Run with AGENT_BROWSER_INTEGRATION=1.
func TestReproActLabelLayouts(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	url := serveForm(t)
	callTool(t, sess, ctx, "navigate", map[string]any{"url": url})

	cases := []struct {
		intent string
		value  string
		desc   string
	}{
		{"Full name", "Bishesh", "A: label-for"},
		{"Email address", "x@y.com", "B: placeholder only"},
		{"Phone number", "123", "C: aria-label only"},
		{"Company", "Acme", "D: wrapping label"},
		{"custcode", "ABC", "E: name/id only (no human label)"},
		{"Place order", "", "submit button"},
	}
	for _, c := range cases {
		args := map[string]any{"intent": c.intent}
		if c.value != "" {
			args["value"] = c.value
		}
		start := time.Now()
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "act", Arguments: args})
		dt := time.Since(start)
		if err != nil {
			t.Errorf("act %q (%s): call error after %s: %v", c.intent, c.desc, dt, err)
			continue
		}
		txt := contentText(res)
		isErr := res.IsError
		t.Logf("act %q (%s): %s err=%v -> %s", c.intent, c.desc, dt.Round(time.Millisecond), isErr, oneLine(txt))
		if isErr {
			t.Errorf("act %q (%s): FAILED to resolve (expected it to work): %s", c.intent, c.desc, oneLine(txt))
		}
		if dt > 15*time.Second {
			t.Errorf("act %q (%s): took %s (slow; possible wedge/hang)", c.intent, c.desc, dt)
		}
	}
}

// TestReproClickIntoHang reproduces the "click timed out, page became wedged,
// reset recovered it" report. A click that triggers a server endpoint that
// never responds should NOT eat the full op-timeout silently if the re-snapshot
// can't stabilize; it should return a bounded outcome. Today this may hit the
// op-timeout - the test records the wall time so we can see the failure mode.
func TestReproClickIntoHang(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	// An endpoint that holds the request open (simulating a wedged navigation).
	hang := make(chan struct{})
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><head><title>Hang</title></head><body>
		<h1>Hang test</h1>
		<a id="go" href="/hang">Go to hanging page</a>
		</body></html>`)
	})
	mux.HandleFunc("/hang", func(w http.ResponseWriter, r *http.Request) {
		<-hang // never completes
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { close(hang); srv.Close() })

	callTool(t, sess, ctx, "navigate", map[string]any{"url": srv.URL})
	start := time.Now()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "click", Arguments: map[string]any{"ref": "r2"}})
	dt := time.Since(start)
	t.Logf("click into hang: %s err=%v isErr=%v -> %s", dt.Round(time.Millisecond), err, resIsErr(res), oneLine(contentText(res)))
	if err != nil {
		t.Fatalf("click call error: %v", err)
	}
	// The click itself should return within a reasonable bound (the re-snapshot
	// can't stabilize on a hanging nav, but it must not wedge the session).
	if dt > 45*time.Second {
		t.Errorf("click into hang took %s - exceeds op-timeout budget; session may wedge", dt)
	}
	// After a click that wedged, the session must still be usable: a fresh
	// navigate to a fast page should work without reset.
	start = time.Now()
	res2, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "navigate", Arguments: map[string]any{"url": srv.URL}})
	dt2 := time.Since(start)
	if err != nil || resIsErr(res2) {
		t.Fatalf("after a wedging click, navigate should still work without reset, got err=%v isErr=%v after %s: %s", err, resIsErr(res2), dt2, oneLine(contentText(res2)))
	}
	t.Logf("navigate after wedge: %s (recovered without reset)", dt2.Round(time.Millisecond))
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " | ")
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}

func resIsErr(r *mcp.CallToolResult) bool {
	if r == nil {
		return false
	}
	return r.IsError
}
