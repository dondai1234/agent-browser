package mcpserver

import (
	"strings"
	"testing"
)

// TestV209NavigateBackForwardLocal: isolate back/forward/reload from network
// flakiness using local servers. If this passes, the real-site
// TestRealWorldNavigateBackForwardReload failure is network flakiness, not a
// code regression.
func TestV209NavigateBackForwardLocal(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	pageA := servePage(t, `<!doctype html><html><head><title>Local A</title></head><body><h1>AAA</h1></body></html>`)
	pageB := servePage(t, `<!doctype html><html><head><title>Local B</title></head><body><h1>BBB</h1></body></html>`)

	a := callTool(t, sess, ctx, "navigate", map[string]any{"url": pageA})
	if !strings.Contains(a, "Local A") {
		t.Fatalf("nav A: %q", a)
	}
	b := callTool(t, sess, ctx, "navigate", map[string]any{"url": pageB})
	if !strings.Contains(b, "Local B") {
		t.Fatalf("nav B: %q", b)
	}
	back := callTool(t, sess, ctx, "navigate", map[string]any{"action": "back"})
	t.Logf("back:\n%s", back)
	if !strings.Contains(back, "Local A") {
		t.Errorf("back should return to Local A, got:\n%s", back)
	}
	fwd := callTool(t, sess, ctx, "navigate", map[string]any{"action": "forward"})
	t.Logf("forward:\n%s", fwd)
	if !strings.Contains(fwd, "Local B") {
		t.Errorf("forward should return to Local B, got:\n%s", fwd)
	}
	reload := callTool(t, sess, ctx, "navigate", map[string]any{"action": "reload"})
	t.Logf("reload:\n%s", reload)
	if !strings.Contains(reload, "Local B") {
		t.Errorf("reload should stay on Local B, got:\n%s", reload)
	}
}
