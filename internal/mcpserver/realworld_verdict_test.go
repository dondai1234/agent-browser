package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldVerdict: the verdict line must reflect what actually happened,
// not just "something changed". A login click navigates -> verdict says
// "navigated ... inventory"; an add-to-cart click flips the button label ->
// verdict says "changed". This guards the v2 foundation (every action returns a
// semantic outcome) against real DOM, not constructed trees.
func TestRealWorldVerdict(t *testing.T) {
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

	// The login click navigates to /inventory.html. The verdict must say so.
	// Asserting the verdict (not just the body) proves the semantic layer fires
	// on a real navigation.
	loginClick := callTool(t, sess, ctx, "click", map[string]any{"ref": loginRef})
	t.Logf("login click:\n%s", loginClick)
	if !strings.HasPrefix(strings.TrimSpace(loginClick), "verdict:") {
		t.Fatalf("click output should start with a verdict line, got:\n%s", loginClick)
	}
	if !strings.Contains(loginClick, "verdict: navigated") || !strings.Contains(loginClick, "inventory") {
		t.Errorf("login verdict should say navigated to inventory, got:\n%s", loginClick)
	}

	// Add-to-cart flips the button label "Add to cart" -> "Remove" (a Changed
	// element, same backend). Verdict must report a change, NOT "no visible
	// effect" - that would mean the verdict missed a real DOM change.
	addButtons := callTool(t, sess, ctx, "find", map[string]any{"role": "button", "text": "Add to cart"})
	if !strings.Contains(addButtons, "Add to cart") {
		t.Fatalf("no Add to cart button found:\n%s", addButtons)
	}
	addRef := refFor(t, addButtons, "Add to cart")
	addClick := callTool(t, sess, ctx, "click", map[string]any{"ref": addRef})
	t.Logf("add-to-cart click:\n%s", addClick)
	if !strings.HasPrefix(strings.TrimSpace(addClick), "verdict:") {
		t.Fatalf("add click output should start with a verdict line, got:\n%s", addClick)
	}
	if strings.Contains(addClick, "no visible effect") {
		t.Errorf("add-to-cart had a visible effect (button -> Remove) but verdict said no visible effect:\n%s", addClick)
	}
	if !strings.Contains(addClick, "changed") {
		t.Errorf("add-to-cart verdict should report a change, got:\n%s", addClick)
	}
}
