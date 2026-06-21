package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldHistory: a multi-step flow (navigate + 3 acts) must be recorded
// in the session log, queryable via history with verdicts + URLs - so the agent
// can re-orient after a long flow without re-snapshotting or holding it all in
// context. Also checks the last=N filter and errors=true filter.
func TestRealWorldHistory(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Username", "value": "standard_user"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Password", "value": "secret_sauce"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Login"})

	hist := callTool(t, sess, ctx, "history", map[string]any{})
	t.Logf("history:\n%s", hist)
	if !strings.Contains(hist, "navigate") {
		t.Errorf("history should record the navigate, got:\n%s", hist)
	}
	if !strings.Contains(hist, "act ") {
		t.Errorf("history should record the act calls, got:\n%s", hist)
	}
	if !strings.Contains(hist, "navigated to") {
		t.Errorf("history should carry verdicts, got:\n%s", hist)
	}
	if !strings.Contains(hist, "inventory") {
		t.Errorf("history should show the login reached inventory, got:\n%s", hist)
	}
	// 4 actions ran (navigate + 3 acts) -> at least 4 entry lines.
	if strings.Count(hist, "\n") < 4 {
		t.Errorf("expected >=4 history entry lines, got %d:\n%s", strings.Count(hist, "\n"), hist)
	}

	// last=2 filter returns only the two most recent.
	h2 := callTool(t, sess, ctx, "history", map[string]any{"last": 2})
	if !strings.Contains(h2, "showing last 2") {
		t.Errorf("last=2 should note 'showing last 2', got:\n%s", h2)
	}

	// errors=true on a clean flow (no CHALLENGE) -> empty.
	he := callTool(t, sess, ctx, "history", map[string]any{"errors": true})
	if !strings.Contains(he, "empty") {
		t.Errorf("errors filter with no blocked actions should be empty, got:\n%s", he)
	}
}
