package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldCollect: the multi-value pull in one call. On example.com,
// collect the h1 title + the link's href together - {label:selector} in,
// {label:value} JSON out, no JS. This is the tool that flips the benchmark's
// Task C (one call for paragraph + infobox instead of N extract calls) and
// Task A (one call for stars + language + issues + ...).
func TestRealWorldCollect(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})
	out := callTool(t, sess, ctx, "collect", map[string]any{
		"fields": map[string]any{
			"title": "h1",
			"link":  "a",
		},
		"attrs": map[string]any{"link": "href"},
	})
	t.Logf("collect = %s", out)
	trimmed := strings.TrimSpace(out)
	if !strings.HasPrefix(trimmed, "{") {
		t.Fatalf("collect should return a JSON object, got:\n%s", out)
	}
	if !strings.Contains(out, "Example Domain") {
		t.Errorf("title label should contain 'Example Domain', got:\n%s", out)
	}
	// The link label was given attrs link=href; example.com's link is an absolute URL.
	if !strings.Contains(out, "http") {
		t.Errorf("the link field (attrs=href) should be a URL, got:\n%s", out)
	}
}

// TestRealWorldCollectMiss: a selector that doesn't match returns null for that
// label (not an error), so the agent gets a partial result it can branch on.
func TestRealWorldCollectMiss(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Software_agent"})
	out := callTool(t, sess, ctx, "collect", map[string]any{
		"fields": map[string]any{
			"real":     "#firstHeading",
			"gone":     ".no-such-class-xyz-123",
			"alsogone": ".another-missing-selector",
		},
	})
	t.Logf("collect miss = %s", safeHead(out, 400))
	if !strings.Contains(out, "\"real\"") {
		t.Errorf("real label should be present, got:\n%s", out)
	}
	if !strings.Contains(out, "\"gone\": null") && !strings.Contains(out, "\"gone\":null") {
		t.Errorf("missing selector should yield null for that label, got:\n%s", out)
	}
}

// TestRealWorldCollectRequiresFields: empty fields is a usage error (isError),
// not a silent empty success.
func TestRealWorldCollectRequiresFields(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Software_agent"})
	res := callToolResult(t, sess, ctx, "collect", map[string]any{})
	text := contentText(res)
	if !res.IsError {
		t.Errorf("collect with no fields should be isError, got:\n%s", text)
	}
	if !strings.Contains(text, "fields") {
		t.Errorf("error should point to the missing fields map, got:\n%s", text)
	}
}

// TestRealWorldCollectFormFlow: the Task-B shape - find the form refs + ONE
// fill fields={} call (batch) + act login, proving the batched fill path the
// benchmark missed. Login reaches inventory in fewer calls than per-field fill.
func TestRealWorldCollectFormFlow(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	textboxes := callTool(t, sess, ctx, "find", map[string]any{"role": "textbox"})
	t.Logf("textboxes:\n%s", textboxes)
	userRef := refFor(t, textboxes, "Username")
	passRef := refFor(t, textboxes, "Password")
	// ONE batched fill call for both fields (the path the benchmark missed).
	callTool(t, sess, ctx, "fill", map[string]any{"fields": map[string]any{userRef: "standard_user", passRef: "secret_sauce"}})
	// intent-first login (one call, verdict confirms navigation).
	loginOut := callTool(t, sess, ctx, "act", map[string]any{"intent": "Login"})
	t.Logf("login act: %s", safeHead(loginOut, 200))
	body := callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "inventory") && !strings.Contains(body, "Sauce Labs") {
		t.Errorf("batched fill + act login should reach inventory, got:\n%s", safeHead(body, 300))
	}
}
