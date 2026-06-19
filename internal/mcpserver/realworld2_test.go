package mcpserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRealWorldIframe: does read surface same-origin iframe content?
// (Fixed: read now walks same-origin iframes.)
func TestRealWorldIframe(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://the-internet.herokuapp.com/iframe"})
	body := callTool(t, sess, ctx, "read", map[string]any{})
	t.Logf("iframe body:\n%s", body)
	// The fix walks same-origin iframes; we expect the iframe to be surfaced
	// (the-internet's TinyMCE is currently read-only/empty, so we check the
	// iframe prefix, not specific content).
	if !strings.Contains(body, "Rich Text Area") {
		t.Errorf("iframe not surfaced in read: %q", body)
	}
}

// TestRealWorldUpload: auto-find a file input, set a file, submit, verify.
func TestRealWorldUpload(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://the-internet.herokuapp.com/upload"})

	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello world\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// No ref: auto-find the first file input.
	callTool(t, sess, ctx, "upload", map[string]any{"paths": []string{path}})

	buttons := callTool(t, sess, ctx, "find", map[string]any{"role": "button"})
	upRef := refFor(t, buttons, "Upload")
	callTool(t, sess, ctx, "click", map[string]any{"ref": upRef})
	body := callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "File Uploaded") {
		t.Errorf("upload did not complete: %q", body)
	}
}

// TestRealWorldHeavySPA: a real Angular e-commerce SPA. Measure token cost of
// the minimal orientation + summary, and verify find works on a heavy page.
func TestRealWorldHeavySPA(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://practicesoftwaretesting.com"})
	min := callTool(t, sess, ctx, "see", map[string]any{"level": "minimal"})
	t.Logf("heavy SPA minimal (%d chars, ~%d tok):\n%s", len(min), len(min)/4, min)
	sum := callTool(t, sess, ctx, "see", map[string]any{"level": "summary"})
	t.Logf("heavy SPA summary: %d chars, ~%d tok (capped at 150 + overflow hint)", len(sum), len(sum)/4)
	links := callTool(t, sess, ctx, "find", map[string]any{"role": "link"})
	if !strings.Contains(links, "link") {
		t.Errorf("no links found on heavy SPA: %q", links)
	}
}

// TestRealWorldPersistence: a login survives within the browser session across
// navigation away and back.
func TestRealWorldPersistence(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	tb := callTool(t, sess, ctx, "find", map[string]any{"role": "textbox"})
	callTool(t, sess, ctx, "fill", map[string]any{"ref": refFor(t, tb, "Username"), "value": "standard_user"})
	callTool(t, sess, ctx, "fill", map[string]any{"ref": refFor(t, tb, "Password"), "value": "secret_sauce"})
	btn := callTool(t, sess, ctx, "find", map[string]any{"role": "button"})
	callTool(t, sess, ctx, "click", map[string]any{"ref": refFor(t, btn, "Login")})
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com/inventory.html"})
	body := callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "Sauce Labs") && !strings.Contains(body, "inventory") {
		t.Errorf("login did not persist across navigation: %q", body)
	}
}
