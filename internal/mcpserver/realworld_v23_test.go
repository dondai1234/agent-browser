package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldExtractSelectorScope: selector= scopes extraction to a region,
// so the agent pulls just the part it needs instead of the whole page. On a
// Wikipedia article, extract article (whole <main>) is large; the same call
// scoped to #firstHeading returns just the title. This is the #1 token-saving
// lever the live benchmark showed was missing.
func TestRealWorldExtractSelectorScope(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Software_agent"})

	whole := callTool(t, sess, ctx, "extract", map[string]any{"kind": "article"})
	t.Logf("whole article len=%d", len(whole))
	if len(whole) < 500 {
		t.Fatalf("whole article too short, len=%d:\n%s", len(whole), whole)
	}

	scoped := callTool(t, sess, ctx, "extract", map[string]any{"kind": "article", "selector": "#firstHeading"})
	t.Logf("scoped (#firstHeading) = %q", scoped)
	if len(scoped) == 0 {
		t.Fatalf("scoped extract returned empty")
	}
	// Scoped to a single heading must be far smaller than the whole article.
	if len(scoped) >= len(whole) {
		t.Errorf("selector scoping should shrink the output: scoped=%d whole=%d", len(scoped), len(whole))
	}
	// The heading text should actually appear (the page's H1 title).
	if strings.TrimSpace(scoped) == "" {
		t.Errorf("scoped extract should return the heading text, got empty")
	}
}

// TestRealWorldExtractText: kind=text + selector returns each matched element's
// text as a JSON array - the targeted value pull (a price, a count, a heading),
// no JS authoring.
func TestRealWorldExtractText(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Software_agent"})
	out := callTool(t, sess, ctx, "extract", map[string]any{"kind": "text", "selector": "h1"})
	t.Logf("text h1 = %s", out)
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("extract text should return a JSON array, got:\n%s", out)
	}
	// The page has one h1 (#firstHeading); its text must be present.
	if !strings.Contains(out, "Software agent") {
		t.Errorf("text h1 should contain the page title 'Software agent', got:\n%s", out)
	}
}

// TestRealWorldExtractTextRequiresSelector: text without a selector is a usage
// error (isError), not a silent empty success.
func TestRealWorldExtractTextRequiresSelector(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Software_agent"})
	res := callToolResult(t, sess, ctx, "extract", map[string]any{"kind": "text"})
	text := contentText(res)
	if !res.IsError {
		t.Errorf("extract text without a selector should be isError, got:\n%s", text)
	}
	if !strings.Contains(text, "selector") {
		t.Errorf("error should point to the missing selector, got:\n%s", text)
	}
}

// TestRealWorldExtractMaxChars: maxChars caps the response so a long article
// returns a short, token-cheap slice with the truncation marker.
func TestRealWorldExtractMaxChars(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Software_agent"})
	out := callTool(t, sess, ctx, "extract", map[string]any{"kind": "article", "maxChars": 300})
	t.Logf("maxChars=300 len=%d", len(out))
	// truncate() appends "...(truncated; N chars total)" so allow headroom over 300.
	if len(out) > 400 {
		t.Errorf("maxChars=300 should cap well under 400, got len=%d:\n%s", len(out), safeHead(out, 200))
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("a capped long article should carry the truncation marker, got:\n%s", safeHead(out, 200))
	}
}

// TestRealWorldExtractSelectorMiss: a selector that matches nothing returns an
// isError with a pointer (not a crash, not empty success).
func TestRealWorldExtractSelectorMiss(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})
	res := callToolResult(t, sess, ctx, "extract", map[string]any{"kind": "table", "selector": ".does-not-exist"})
	text := contentText(res)
	if !res.IsError {
		t.Errorf("extract with a non-matching selector should be isError, got:\n%s", text)
	}
	if !strings.Contains(text, "no table") {
		t.Errorf("should say 'no table found ... under selector', got:\n%s", text)
	}
}

// TestRealWorldSelectSelector: an unlabeled <select> (no a11y name, only a
// class) is operable by CSS selector in one call - the saucedemo sort dropdown,
// which the live benchmark showed cost extra find+select calls because act by
// intent can't reach it (no label) and select needed a ref first.
func TestRealWorldSelectSelector(t *testing.T) {
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

	// Sort by price high-to-low via the class-only <select> (no a11y name).
	out := callTool(t, sess, ctx, "select", map[string]any{"selector": ".product_sort_container", "value": "Price (high to low)"})
	t.Logf("select selector verdict:\n%s", out)

	// After sorting high->low, the most expensive item (Sauce Labs Fleece Jacket,
	// $49.99) is first, so it must appear BEFORE the cheapest (Sauce Labs Onesie,
	// $7.99) in the inventory text.
	body := callTool(t, sess, ctx, "read", map[string]any{})
	high := strings.Index(body, "Sauce Labs Fleece Jacket")
	low := strings.Index(body, "Sauce Labs Onesie")
	if high < 0 || low < 0 {
		t.Fatalf("expected both the priciest and cheapest items in the body, got high=%d low=%d:\n%s", high, low, safeHead(body, 600))
	}
	if high > low {
		t.Errorf("after Price (high to low) the Fleece Jacket (priciest) should come before the Onesie (cheapest); got high=%d low=%d", high, low)
	}
}

// TestRealWorldClear: clear wipes cookies + web storage and reloads, giving a
// one-call clean slate. On saucedemo (login held in session storage), logging
// in then calling clear must log the agent out - the fix for the benchmark's
// 6-extra-clicks dirty-cart tax.
func TestRealWorldClear(t *testing.T) {
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

	// Confirm we're logged in (inventory reached).
	body := callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "inventory") && !strings.Contains(body, "Sauce Labs") {
		t.Fatalf("login did not reach inventory before clear:\n%s", safeHead(body, 400))
	}

	// One-call clean slate.
	clearOut := callTool(t, sess, ctx, "clear", map[string]any{})
	t.Logf("clear result:\n%s", clearOut)

	// After clear + reload, saucedemo's session is gone -> redirected to the
	// login page. The login form's Username/Password controls must reappear.
	after := callTool(t, sess, ctx, "read", map[string]any{})
	t.Logf("after clear body:\n%s", safeHead(after, 400))
	if !strings.Contains(after, "Password") && !strings.Contains(after, "Username") {
		t.Errorf("clear should have logged us out (login form reappears), got:\n%s", safeHead(after, 400))
	}
}

// TestRealWorldClearNavigate: clear with a url wipes state AND opens a fresh
// page in one call (clean + go).
func TestRealWorldClearNavigate(t *testing.T) {
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

	out := callTool(t, sess, ctx, "clear", map[string]any{"url": "https://example.com"})
	t.Logf("clear+navigate result:\n%s", safeHead(out, 200))
	body := callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "Example Domain") {
		t.Errorf("clear url= should land on the new page, got:\n%s", safeHead(body, 300))
	}
}
