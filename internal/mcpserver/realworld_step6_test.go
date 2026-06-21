package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldWaitConditions: the three semantic wait conditions + the timeout
// path, on a real page. url= matches after a login redirect; text= matches text
// in the body; gone= matches when text is absent; a never-met text times out.
func TestRealWorldWaitConditions(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})

	// text appears -> "present"
	wText := callTool(t, sess, ctx, "wait", map[string]any{"seconds": 3, "text": "Example Domain"})
	t.Logf("wait text: %q", wText)
	if !strings.Contains(wText, "present") {
		t.Errorf("wait text should report present, got %q", wText)
	}

	// gone: text that isn't on the page -> matches instantly ("gone")
	wGone := callTool(t, sess, ctx, "wait", map[string]any{"seconds": 2, "gone": "LoadingSpinnerNoSuchText"})
	t.Logf("wait gone: %q", wGone)
	if !strings.Contains(wGone, "gone") {
		t.Errorf("wait gone should report gone, got %q", wGone)
	}

	// timeout: text that will never appear -> isError
	res := callToolResult(t, sess, ctx, "wait", map[string]any{"seconds": 1.5, "text": "zzzneverpresent"})
	if !res.IsError {
		t.Errorf("wait for never-present text should time out (isError), got %q", contentText(res))
	}
}

// TestRealWorldWaitURL: after a login that redirects, wait url= matches the new
// URL - the "did the redirect land" loop without polling see.
func TestRealWorldWaitURL(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Username", "value": "standard_user"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Password", "value": "secret_sauce"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Login"})
	wURL := callTool(t, sess, ctx, "wait", map[string]any{"seconds": 5, "url": "inventory"})
	t.Logf("wait url: %q", wURL)
	if !strings.Contains(wURL, "url matched") || !strings.Contains(wURL, "inventory") {
		t.Errorf("wait url=inventory should match the post-login URL, got %q", wURL)
	}
}

// TestRealWorldFillMap: fill a whole form in one call via fields={ref:value},
// then submit - proving the multi-field fill set every field correctly (the
// login succeeds only if BOTH username + password were filled).
func TestRealWorldFillMap(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})

	textboxes := callTool(t, sess, ctx, "find", map[string]any{"role": "textbox"})
	userRef := refFor(t, textboxes, "Username")
	passRef := refFor(t, textboxes, "Password")

	out := callTool(t, sess, ctx, "fill", map[string]any{
		"fields": map[string]any{userRef: "standard_user", passRef: "secret_sauce"},
	})
	t.Logf("fill map:\n%s", out)
	if !strings.HasPrefix(strings.TrimSpace(out), "verdict:") {
		t.Errorf("fill map should return a verdict line, got:\n%s", out)
	}
	// both fields changed -> the delta should report ~2 changed
	if !strings.Contains(out, "~") {
		t.Errorf("fill map should show changed fields, got:\n%s", out)
	}

	// submit; the login succeeds only if both fields were actually filled.
	login := callTool(t, sess, ctx, "act", map[string]any{"intent": "Login"})
	if !strings.Contains(login, "inventory") {
		t.Errorf("login after fill map should reach inventory (proves both fields filled), got:\n%s", login)
	}
}
