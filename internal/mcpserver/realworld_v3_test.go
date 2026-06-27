package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestV3NavOrientation: nav returns an orientation (page/auth + counts), not a
// bare url - the v3 "land oriented" win.
func TestV3NavOrientation(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	out := callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	t.Logf("nav:\n%s", out)
	for _, want := range []string{"url:", "page:", "auth:"} {
		if !strings.Contains(out, want) {
			t.Errorf("nav orientation missing %q in:\n%s", want, out)
		}
	}
}

// TestV3NavLevelRefs: nav level=refs returns the interactive list (the a11y
// ref-line format), not the brief.
func TestV3NavLevelRefs(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	out := callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com", "level": "refs"})
	t.Logf("nav refs:\n%s", out)
	if !strings.Contains(out, "[r") {
		t.Errorf("nav level=refs should list ref-lines ([rN] role \"name\"), got:\n%s", out)
	}
}

// TestV3JSExpression: a bare expression is wrapped + unquoted to plain text.
func TestV3JSExpression(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "js", map[string]any{"script": "document.title"})
	t.Logf("js expr: %q", out)
	if !strings.Contains(out, "Example Domain") {
		t.Errorf("js document.title should return 'Example Domain', got %q", out)
	}
}

// TestV3JSHelperObject: the hero - several fields in one call via helpers.
func TestV3JSHelperObject(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "js", map[string]any{
		"script": `return {title: document.title, h1: text('h1'), link: attr('a','href')}`,
	})
	t.Logf("js object: %s", out)
	for _, want := range []string{`"title"`, "Example Domain", "iana.org"} {
		if !strings.Contains(out, want) {
			t.Errorf("js object missing %q in: %s", want, out)
		}
	}
}

// TestV3JSLinksHelper: the links() helper returns [{text,href}].
func TestV3JSLinksHelper(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "js", map[string]any{"script": `return links('a')[0]`})
	t.Logf("links: %s", out)
	if !strings.Contains(out, "href") || !strings.Contains(out, "iana.org") {
		t.Errorf("links() should return {text,href} with the iana link, got: %s", out)
	}
}

// TestV3JSErrorCapture: a thrown error surfaces as a tool error with the message.
func TestV3JSErrorCapture(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	res := callToolResult(t, sess, ctx, "js", map[string]any{"script": `throw new Error('boom-zap-42')`})
	if !res.IsError {
		t.Fatalf("js throw should be a tool error, got: %q", contentText(res))
	}
	if !strings.Contains(contentText(res), "boom-zap-42") {
		t.Errorf("js error should carry the page-side message, got: %q", contentText(res))
	}
}

// TestV3JSAwait: await= waits for a selector before running; a missing selector
// times out as an error.
func TestV3JSAwait(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "js", map[string]any{"await": "h1", "script": "text('h1')"})
	if !strings.Contains(out, "Example Domain") {
		t.Errorf("await+scrape h1 should return the heading, got: %s", out)
	}
	res := callToolResult(t, sess, ctx, "js", map[string]any{"await": ".no-such-element", "awaitMs": 800, "script": "1"})
	if !res.IsError || !strings.Contains(contentText(res), "not found") {
		t.Errorf("await on a missing selector should error 'not found', got: %q", contentText(res))
	}
}

// TestV3SeeOutline: outline returns the semantic skeleton with working selectors.
func TestV3SeeOutline(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "see", map[string]any{"level": "outline"})
	t.Logf("outline:\n%s", out)
	if !strings.Contains(out, "h1") {
		t.Errorf("outline should list the h1, got:\n%s", out)
	}
	// every outline line carries a quoted selector
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" || strings.HasPrefix(line, "no semantic") {
			continue
		}
		if !strings.Contains(line, `"`) {
			t.Errorf("outline line should carry a quoted selector: %q", line)
		}
	}
}

// TestV3SeeLevels: brief/refs/text return the right shape.
func TestV3SeeLevels(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	brief := callTool(t, sess, ctx, "see", map[string]any{"level": "brief"})
	if !strings.Contains(brief, "page:") {
		t.Errorf("see brief should have page:, got: %s", brief)
	}
	refs := callTool(t, sess, ctx, "see", map[string]any{"level": "refs"})
	if !strings.Contains(refs, "[r") {
		t.Errorf("see refs should list ref-lines, got: %s", refs)
	}
	text := callTool(t, sess, ctx, "see", map[string]any{"level": "text"})
	if !strings.Contains(text, "Example Domain") {
		t.Errorf("see text should include body text, got: %s", text)
	}
}

// TestV3FindA11y: find role= returns ref-lines from the cached snapshot.
func TestV3FindA11y(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "find", map[string]any{"role": "link"})
	t.Logf("find link:\n%s", out)
	if !strings.Contains(out, "[r") || !strings.Contains(out, "link") {
		t.Errorf("find role=link should return a ref-line, got: %s", out)
	}
}

// TestV3FindSelector: find selector= returns [css] lines with a sel=.
func TestV3FindSelector(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "find", map[string]any{"selector": "h1"})
	t.Logf("find selector h1:\n%s", out)
	if !strings.Contains(out, "[css]") || !strings.Contains(out, "sel=") {
		t.Errorf("find selector should return [css] lines with sel=, got: %s", out)
	}
}

// TestV3FindSelectorsBridge: find selectors=true annotates a11y matches with a
// CSS selector so the agent can target the same element in js.
func TestV3FindSelectorsBridge(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "find", map[string]any{"role": "link", "selectors": true})
	t.Logf("find selectors:\n%s", out)
	if !strings.Contains(out, "sel=") {
		t.Errorf("find selectors=true should add sel= to a11y matches, got: %s", out)
	}
}

// TestV3ActIntentSaucedemo: the canonical act-by-intent flow with a fused wait.
func TestV3ActIntentSaucedemo(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://www.saucedemo.com"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Username", "value": "standard_user"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Password", "value": "secret_sauce"})
	out := callTool(t, sess, ctx, "act", map[string]any{"intent": "Login", "waitUrl": "/inventory.html", "waitMs": 15000})
	t.Logf("login act:\n%s", out)
	if !strings.Contains(out, "navigated") && !strings.Contains(out, "inventory") {
		t.Errorf("login should navigate to inventory, got: %s", out)
	}
	body := callTool(t, sess, ctx, "see", map[string]any{"level": "text"})
	if !strings.Contains(body, "Sauce Labs") && !strings.Contains(body, "inventory") {
		t.Errorf("post-login body should show inventory, got: %s", body)
	}
}

// TestV3ActByRef: act by ref fills + clicks (saucedemo), proving the ref path.
func TestV3ActByRef(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://www.saucedemo.com"})
	textboxes := callTool(t, sess, ctx, "find", map[string]any{"role": "textbox"})
	userRef := refFor(t, textboxes, "Username")
	callTool(t, sess, ctx, "act", map[string]any{"ref": userRef, "value": "standard_user"})
	passRef := refFor(t, textboxes, "Password")
	callTool(t, sess, ctx, "act", map[string]any{"ref": passRef, "value": "secret_sauce"})
	buttons := callTool(t, sess, ctx, "find", map[string]any{"role": "button"})
	loginRef := refFor(t, buttons, "Login")
	out := callTool(t, sess, ctx, "act", map[string]any{"ref": loginRef, "waitUrl": "/inventory.html", "waitMs": 15000})
	if !strings.Contains(out, "navigated") && !strings.Contains(out, "inventory") {
		t.Errorf("act by ref should reach inventory, got: %s", out)
	}
}

// TestV3ActBySelector: the selector escape hatch fills an a11y-invisible-ish input.
func TestV3ActBySelector(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://www.saucedemo.com"})
	callTool(t, sess, ctx, "act", map[string]any{"selector": "#user-name", "value": "standard_user"})
	callTool(t, sess, ctx, "act", map[string]any{"selector": "#password", "value": "secret_sauce"})
	out := callTool(t, sess, ctx, "act", map[string]any{"selector": "#login-button", "waitUrl": "/inventory.html", "waitMs": 15000})
	if !strings.Contains(out, "inventory") {
		t.Errorf("act by selector should reach inventory, got: %s", out)
	}
}

// TestV3ActAmbiguousCandidates: multiple "Add to cart" -> ambiguous, returns
// ranked candidates (act never guesses).
func TestV3ActAmbiguousCandidates(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://www.saucedemo.com"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Username", "value": "standard_user"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Password", "value": "secret_sauce"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Login", "waitUrl": "/inventory.html", "waitMs": 15000})
	res := callToolResult(t, sess, ctx, "act", map[string]any{"intent": "Add to cart"})
	if !res.IsError {
		t.Fatalf("ambiguous Add to cart should be an error, got: %q", contentText(res))
	}
	msg := contentText(res)
	if !strings.Contains(msg, "ambiguous") || !strings.Contains(msg, "matches") {
		t.Errorf("ambiguous act should surface candidates, got: %q", msg)
	}
}

// TestV3ActNth: nth disambiguates the identical Add-to-cart buttons (click the 1st).
func TestV3ActNth(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://www.saucedemo.com"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Username", "value": "standard_user"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Password", "value": "secret_sauce"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Login", "waitUrl": "/inventory.html", "waitMs": 15000})
	out := callTool(t, sess, ctx, "act", map[string]any{"intent": "Add to cart", "nth": 1})
	t.Logf("nth=1 add:\n%s", out)
	// The button toggles to "Remove" -> a changed verdict (not an ambiguous error).
	if strings.Contains(out, "ambiguous") {
		t.Errorf("nth should disambiguate, got ambiguous: %s", out)
	}
}

// TestV3ActHover: hover reveals a hover-only caption.
func TestV3ActHover(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://the-internet.herokuapp.com/hovers"})
	// the hover targets are figures; hover the first figure's image.
	imgs := callTool(t, sess, ctx, "find", map[string]any{"role": "image"})
	t.Logf("hovers imgs:\n%s", imgs)
	if !strings.Contains(imgs, "User Avatar") {
		t.Skip("no User Avatar images on hovers page; page may have changed")
	}
	first := refFor(t, imgs, "User Avatar")
	out := callTool(t, sess, ctx, "act", map[string]any{"ref": first, "hover": true})
	t.Logf("hover:\n%s", out)
	// hovering reveals a "View profile" link / "name: user1" caption.
	refs := callTool(t, sess, ctx, "see", map[string]any{"level": "refs"})
	if !strings.Contains(refs, "View profile") && !strings.Contains(refs, "user1") {
		t.Errorf("hover should reveal the caption/link, refs:\n%s", refs)
	}
}

// TestV3ActKey: a real CDP key press fires the native handler.
func TestV3ActKey(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://the-internet.herokuapp.com/key_presses"})
	out := callTool(t, sess, ctx, "act", map[string]any{"key": "Escape"})
	t.Logf("press Escape:\n%s", out)
	body := callTool(t, sess, ctx, "see", map[string]any{"level": "text"})
	if !strings.Contains(strings.ToUpper(body), "ESCAPE") {
		t.Errorf("press Escape should register on the page, body: %s", body)
	}
}

// TestV3ActUpload: files= uploads via auto-find + submit.
func TestV3ActUpload(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	dir := t.TempDir()
	f := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(f, []byte("v3 upload test\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://the-internet.herokuapp.com/upload"})
	callTool(t, sess, ctx, "act", map[string]any{"files": []any{f}})
	out := callTool(t, sess, ctx, "act", map[string]any{"intent": "Upload"})
	t.Logf("upload:\n%s", out)
	body := callTool(t, sess, ctx, "see", map[string]any{"level": "text"})
	if !strings.Contains(body, "File Uploaded") && !strings.Contains(body, "hello.txt") {
		t.Errorf("upload should report File Uploaded, body: %s", body)
	}
}

// TestV3Tabs: open a new labeled tab, list/switch/close.
func TestV3Tabs(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com", "newTab": true, "label": "second"})
	list := callTool(t, sess, ctx, "tabs", map[string]any{})
	t.Logf("tabs:\n%s", list)
	if !strings.Contains(list, "second") {
		t.Errorf("tabs list should show the labeled tab, got: %s", list)
	}
	callTool(t, sess, ctx, "tabs", map[string]any{"action": "switch", "id": "t1"})
	callTool(t, sess, ctx, "tabs", map[string]any{"action": "close", "id": "t2"})
	list2 := callTool(t, sess, ctx, "tabs", map[string]any{})
	if strings.Contains(list2, "second") {
		t.Errorf("closed tab should be gone, got: %s", list2)
	}
}

// TestV3History: the action log records nav/act steps.
func TestV3History(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	callTool(t, sess, ctx, "js", map[string]any{"script": "document.title"})
	out := callTool(t, sess, ctx, "history", map[string]any{"last": 5})
	t.Logf("history:\n%s", out)
	if !strings.Contains(out, "navigate") && !strings.Contains(out, "nav") {
		t.Errorf("history should list the navigate step, got: %s", out)
	}
}

// TestV3SessionClear: after login, clear wipes the session -> back to anonymous.
func TestV3SessionClear(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://www.saucedemo.com"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Username", "value": "standard_user"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Password", "value": "secret_sauce"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Login", "waitUrl": "/inventory.html", "waitMs": 15000})
	out := callTool(t, sess, ctx, "session", map[string]any{"mode": "clear"})
	t.Logf("clear:\n%s", out)
	// saucedemo redirects to login when the session is cleared.
	body := callTool(t, sess, ctx, "see", map[string]any{"level": "text"})
	if !strings.Contains(strings.ToLower(body), "login") && !strings.Contains(out, "login") {
		t.Errorf("clear should drop the session back to the login page, body: %s", body)
	}
}

// TestV3SessionReset: reset relaunches to a fresh page.
func TestV3SessionReset(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "session", map[string]any{"mode": "reset", "url": "https://example.com"})
	t.Logf("reset:\n%s", out)
	if !strings.Contains(out, "url:") {
		t.Errorf("reset should return a fresh orientation, got: %s", out)
	}
}

// TestV3ActNeedsTarget: act with no target/key/files is a clear error.
func TestV3ActNeedsTarget(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})
	res := callToolResult(t, sess, ctx, "act", map[string]any{})
	if !res.IsError {
		t.Fatalf("act with no target should be an error, got: %q", contentText(res))
	}
	if !strings.Contains(contentText(res), "target") {
		t.Errorf("act-no-target error should mention target, got: %q", contentText(res))
	}
}
