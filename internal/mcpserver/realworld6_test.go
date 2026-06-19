package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRealWorldIframeInteraction: can the agent act on elements INSIDE a
// same-origin iframe? The unified CDP AX tree should give in-iframe elements
// refs, and click/fill should resolve + act on them across the frame boundary.
//
// This is the gap the audit report flagged as "most impactful." `read` already
// pierces same-origin iframes (TestRealWorldIframe); this test covers the
// interaction path (fill an in-iframe input, click an in-iframe button).
//
// The iframe is deliberately offset (left:80px;top:120px) so a click that uses
// iframe-relative coords as viewport coords would miss - making the test
// meaningful, not a coincidence-pass.
func TestRealWorldIframeInteraction(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
<h1>Parent</h1>
<iframe src="/frame" title="formframe" style="position:absolute;left:80px;top:120px;width:420px;height:220px;border:2px solid red"></iframe>
<div id="result">empty</div>
</body></html>`))
	})
	mux.HandleFunc("/frame", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
<input id="msg" placeholder="Msg" aria-label="Msg">
<button id="send" onclick="parent.document.getElementById('result').textContent = document.getElementById('msg').value">Send</button>
</body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	summary := callTool(t, sess, ctx, "navigate", map[string]any{"url": srv.URL, "level": "summary"})
	t.Logf("summary:\n%s", summary)

	// 1. The in-iframe input must appear in the unified tree with a ref.
	inRef := refFor(t, callTool(t, sess, ctx, "find", map[string]any{"role": "textbox", "text": "Msg"}), "Msg")
	if inRef == "" {
		t.Fatalf("in-iframe textbox not found in snapshot; iframe elements not surfaced:\n%s", summary)
	}
	t.Logf("in-iframe input ref: %s", inRef)

	// 2. Fill the in-iframe input.
	fillOut := callTool(t, sess, ctx, "fill", map[string]any{"ref": inRef, "value": "hello-from-iframe"})
	t.Logf("fill delta: %s", fillOut)

	// 3. Find + click the in-iframe button.
	btnRef := refFor(t, callTool(t, sess, ctx, "find", map[string]any{"role": "button", "text": "Send"}), "Send")
	if btnRef == "" {
		t.Fatalf("in-iframe button not found in snapshot:\n%s", summary)
	}
	clickOut := callTool(t, sess, ctx, "click", map[string]any{"ref": btnRef})
	t.Logf("click delta: %s", clickOut)

	// 4. The button wrote the input value into the parent's #result. read the
	// body (which walks same-origin iframes) and confirm the round-trip.
	body := callTool(t, sess, ctx, "read", map[string]any{})
	t.Logf("body after click:\n%s", body)
	if !strings.Contains(body, "hello-from-iframe") {
		t.Errorf("in-iframe fill+click did not propagate to the parent: body does not contain the value.\nbody:\n%s", body)
	}
}
