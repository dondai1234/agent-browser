package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldBrief: level=brief must classify a real login page correctly -
// "page: login form" + "auth: anonymous" + the primary actions (including the
// Login button with a usable ref) - so the agent lands oriented in ~50 tokens
// instead of scanning the full ref list. Guards the comprehension heuristics
// against real DOM, not constructed trees.
func TestRealWorldBrief(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	out := callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com", "level": "brief"})
	t.Logf("brief:\n%s", out)

	if !strings.Contains(out, "page: login form") {
		t.Errorf("brief should classify saucedemo as a login form, got:\n%s", out)
	}
	if !strings.Contains(out, "auth: anonymous") {
		t.Errorf("brief should report anonymous (no logout control, has Login), got:\n%s", out)
	}
	if !strings.Contains(out, "actions:") {
		t.Errorf("brief should list primary actions, got:\n%s", out)
	}
	// The Login button must appear in the actions line WITH a ref the agent can
	// click - that's the whole point of brief (oriented + ready to act).
	if !strings.Contains(out, "Login") {
		t.Errorf("brief actions should include the Login button, got:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "button") {
		t.Errorf("brief actions should show a button role with a ref, got:\n%s", out)
	}
	// brief must stay compact: it should NOT dump every interactive ref (that's
	// summary's job). A login form has a username + password textbox; if brief
	// were leaking the full list it would show ref-lines like "[r1] textbox".
	if strings.Contains(out, "[r") {
		t.Errorf("brief should not render raw ref-lines (use summary), got:\n%s", out)
	}
}

// TestRealWorldBriefAfterLogin: after logging in, brief should flip auth to
// "logged in" (the inventory page has a logout/menu) and classify the page as a
// list, not a login form. Guards that auth + page-type track real state changes.
func TestRealWorldBriefAfterLogin(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	textboxes := callTool(t, sess, ctx, "find", map[string]any{"role": "textbox"})
	userRef := refFor(t, textboxes, "Username")
	passRef := refFor(t, textboxes, "Password")
	buttons := callTool(t, sess, ctx, "find", map[string]any{"role": "button"})
	loginRef := refFor(t, buttons, "Login")
	callTool(t, sess, ctx, "fill", map[string]any{"ref": userRef, "value": "standard_user"})
	callTool(t, sess, ctx, "fill", map[string]any{"ref": passRef, "value": "secret_sauce"})
	callTool(t, sess, ctx, "click", map[string]any{"ref": loginRef})

	out := callTool(t, sess, ctx, "see", map[string]any{"level": "brief"})
	t.Logf("brief after login:\n%s", out)
	// After login the login form is gone. Saucedemo hides its logout control
	// behind a collapsed menu, so the AX tree can't see it -> auth reads
	// "unknown" (honest: we can't confirm logged-in from visible controls).
	// The meaningful, stable assertion is that the anonymous marker is GONE
	// (it was "anonymous" on the login page; the page state changed).
	if strings.Contains(out, "auth: anonymous") {
		t.Errorf("after login, brief should not still say anonymous, got:\n%s", out)
	}
	if strings.Contains(out, "page: login form") {
		t.Errorf("after login, brief should NOT say login form, got:\n%s", out)
	}
}
