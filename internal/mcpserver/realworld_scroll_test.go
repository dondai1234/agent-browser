package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldScrollAwareness: after a scroll, the verdict must report the
// scroll position so the agent knows whether to keep scrolling ("more below")
// or stop ("at bottom") - the loop-closer for lazy-loaded/long pages.
func TestRealWorldScrollAwareness(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Software_agent"})

	// A partial scroll on a long article -> "more below".
	out := callTool(t, sess, ctx, "scroll", map[string]any{"dy": 2000})
	t.Logf("scroll 2000:\n%s", out)
	if !strings.Contains(out, "scroll ") {
		t.Fatalf("scroll verdict should include a scroll position, got:\n%s", out)
	}
	if !strings.Contains(out, "more below") {
		t.Errorf("partial scroll on a long page should say 'more below', got:\n%s", out)
	}

	// A huge scroll -> "at bottom".
	out2 := callTool(t, sess, ctx, "scroll", map[string]any{"dy": 100000})
	t.Logf("scroll 100000:\n%s", out2)
	if !strings.Contains(out2, "at bottom") {
		t.Errorf("scrolling past the end should say 'at bottom', got:\n%s", out2)
	}
}
