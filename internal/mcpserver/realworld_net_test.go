package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldNetVerdict: an action that triggers a real XHR must surface a
// "net:" signal in the verdict - the "did it hit the API" loop. Wikipedia's
// search box fires an opensearch XHR on input; filling it (with a settle long
// enough for the response to land in the action window) must produce a verdict
// that names the request + status. This guards the read-only network listener
// end-to-end (the plumbing unit tests cover the rendering/filtering).
func TestRealWorldNetVerdict(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org"})
	boxes := callTool(t, sess, ctx, "find", map[string]any{"role": "searchbox"})
	if !strings.Contains(boxes, "searchbox") {
		boxes = callTool(t, sess, ctx, "find", map[string]any{"role": "textbox"})
	}
	ref := refFor(t, boxes, "Search")

	// settleMs=2000 gives the autocomplete XHR time to respond within the
	// action window (the net read happens after buildTree).
	out := callTool(t, sess, ctx, "fill", map[string]any{"ref": ref, "value": "Albert Einstein", "settleMs": 2000})
	t.Logf("fill out:\n%s", out)

	if !strings.HasPrefix(strings.TrimSpace(out), "verdict:") {
		t.Fatalf("fill output should start with a verdict line, got:\n%s", out)
	}
	if !strings.Contains(out, "net:") {
		t.Fatalf("fill triggered an XHR but the verdict has no net: signal:\n%s", out)
	}
	// The net: line must carry a status code (a bare "net:" with no status would
	// be a malformed summary). Wikipedia opensearch responds 200.
	if !strings.Contains(out, "200") {
		t.Errorf("net: signal should report a status, got:\n%s", out)
	}
}
