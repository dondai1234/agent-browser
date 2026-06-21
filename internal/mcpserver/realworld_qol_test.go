package mcpserver

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func imageBytes(t *testing.T, res *mcp.CallToolResult) []byte {
	t.Helper()
	for _, c := range res.Content {
		if img, ok := c.(*mcp.ImageContent); ok {
			return img.Data
		}
	}
	return nil
}

// TestRealWorldNavigateBackForwardReload: browser history + refresh via
// navigate action=back|forward|reload - the QoL gap that was eval-only before.
// Each must return the new page orientation.
func TestRealWorldNavigateBackForwardReload(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	a := callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://go.dev"})
	if !strings.Contains(a, "go.dev") {
		t.Fatalf("nav A: %q", a)
	}
	b := callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.python.org"})
	if !strings.Contains(b, "python.org") {
		t.Fatalf("nav B: %q", b)
	}
	back := callTool(t, sess, ctx, "navigate", map[string]any{"action": "back"})
	t.Logf("back:\n%s", back)
	if !strings.Contains(back, "go.dev") {
		t.Errorf("back should return to go.dev, got:\n%s", back)
	}
	fwd := callTool(t, sess, ctx, "navigate", map[string]any{"action": "forward"})
	t.Logf("forward:\n%s", fwd)
	if !strings.Contains(fwd, "python.org") {
		t.Errorf("forward should return to python.org, got:\n%s", fwd)
	}
	reload := callTool(t, sess, ctx, "navigate", map[string]any{"action": "reload"})
	t.Logf("reload:\n%s", reload)
	if !strings.Contains(reload, "python.org") {
		t.Errorf("reload should stay on python.org, got:\n%s", reload)
	}
}

// TestRealWorldScrollRef: scroll an element by ref into view (not pixel-guessing)
// and report the position.
func TestRealWorldScrollRef(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Software_agent"})
	links := callTool(t, sess, ctx, "find", map[string]any{"role": "link"})
	ref := refFor(t, links, "agent")
	out := callTool(t, sess, ctx, "scroll", map[string]any{"ref": ref})
	t.Logf("scroll ref=%s:\n%s", ref, out)
	if !strings.HasPrefix(strings.TrimSpace(out), "verdict:") {
		t.Fatalf("scroll ref should return a verdict, got:\n%s", out)
	}
	if !strings.Contains(out, "scroll ") || !strings.Contains(out, "px") {
		t.Errorf("scroll ref verdict should report a scroll position in px, got:\n%s", out)
	}
}

// TestRealWorldReadHref: reading a link ref must include the href so the agent
// knows where the link goes without clicking it.
func TestRealWorldReadHref(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})
	links := callTool(t, sess, ctx, "find", map[string]any{"role": "link"})
	ref := refFor(t, links, "Learn more")
	out := callTool(t, sess, ctx, "read", map[string]any{"ref": ref})
	t.Logf("read link:\n%s", out)
	if !strings.Contains(out, "href:") {
		t.Errorf("read on a link ref should include href:, got:\n%s", out)
	}
	if !strings.Contains(out, "iana.org") {
		t.Errorf("example.com's link href should point to iana.org, got:\n%s", out)
	}
}

// TestRealWorldScreenshotFullPageAndRef: fullPage + element screenshots must
// return real PNG bytes (not error, not empty).
func TestRealWorldScreenshotFullPageAndRef(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})

	full := callToolResult(t, sess, ctx, "screenshot", map[string]any{"fullPage": true})
	if full.IsError {
		t.Fatalf("fullPage screenshot errored: %s", contentText(full))
	}
	if fb := imageBytes(t, full); len(fb) < 2000 {
		t.Errorf("fullPage screenshot too small (%d bytes), expected a real PNG", len(fb))
	}

	links := callTool(t, sess, ctx, "find", map[string]any{"role": "link"})
	ref := refFor(t, links, "Learn more")
	elem := callToolResult(t, sess, ctx, "screenshot", map[string]any{"ref": ref})
	if elem.IsError {
		t.Fatalf("element screenshot errored: %s", contentText(elem))
	}
	if eb := imageBytes(t, elem); len(eb) < 100 {
		t.Errorf("element screenshot too small (%d bytes), expected a real PNG", len(eb))
	}
}

// TestRealWorldWhere: a one-shot re-orientation after a multi-step flow must
// carry url, page type, auth, the last action's verdict, and scroll position.
func TestRealWorldWhere(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Username", "value": "standard_user"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Password", "value": "secret_sauce"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Login"})

	out := callTool(t, sess, ctx, "where", map[string]any{})
	t.Logf("where:\n%s", out)
	for _, want := range []string{"url:", "page:", "auth:", "last:", "scroll:", "inventory"} {
		if !strings.Contains(out, want) {
			t.Errorf("where missing %q, got:\n%s", want, out)
		}
	}
	// the last action's verdict should be in the re-orientation
	if !strings.Contains(out, "navigated to") {
		t.Errorf("where should carry the last action's verdict, got:\n%s", out)
	}
}
