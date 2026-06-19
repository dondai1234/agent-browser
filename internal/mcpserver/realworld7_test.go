package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldEval: eval is on by default; the operator can
// disable it with --no-eval. This also guards the latent bug where Eval called
// runtime.Evaluate(script).Do(t.ctx) directly and got "invalid context" (t.ctx
// has no chromedp executor - every action must wrap CDP calls in
// chromedp.Run(t.ctx, chromedp.ActionFunc(...))). eval was never live-tested
// before (it was off by default), so the bug hid until the default flip.
func TestRealWorldEval(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})

	got := callTool(t, sess, ctx, "eval", map[string]any{"script": "document.title"})
	if !strings.Contains(got, "Example Domain") {
		t.Errorf("eval(document.title) = %q, want it to contain \"Example Domain\"", got)
	}

	// A computed value comes back as raw JSON.
	got2 := callTool(t, sess, ctx, "eval", map[string]any{"script": "40+2"})
	if !strings.Contains(got2, "42") {
		t.Errorf("eval(40+2) = %q, want 42", got2)
	}
}
