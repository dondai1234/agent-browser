package mcpserver

import (
	"strings"
	"testing"
)

// TestRealWorldExtractTable: a real <table> with a header row must come back as
// JSON objects keyed by the headers (First Name, Last Name, ...), not a ref dump.
func TestRealWorldExtractTable(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://the-internet.herokuapp.com/tables"})
	out := callTool(t, sess, ctx, "extract", map[string]any{"kind": "table"})
	t.Logf("table:\n%s", out)
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("extract table should return a JSON array, got:\n%s", out)
	}
	// the-internet/tables has a header row (First Name, Last Name, Email, ...).
	// With header detection, rows are objects keyed by those headers.
	if !strings.Contains(out, "Last Name") {
		t.Errorf("table JSON should be keyed by the header 'Last Name', got:\n%s", out)
	}
}

// TestRealWorldExtractLinks: HN's 30 story links must come back as [{text,href}]
// JSON, so the agent gets the link map without scanning ref-lines.
func TestRealWorldExtractLinks(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://news.ycombinator.com"})
	out := callTool(t, sess, ctx, "extract", map[string]any{"kind": "links"})
	t.Logf("links (truncated for log): %s", safeHead(out, 400))
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("extract links should return a JSON array, got:\n%s", safeHead(out, 200))
	}
	if !strings.Contains(out, "\"href\"") || !strings.Contains(out, "\"text\"") {
		t.Errorf("links JSON should have text + href fields, got:\n%s", safeHead(out, 300))
	}
	// HN has ~30 story links; a real extraction is non-trivially large.
	if len(out) < 300 {
		t.Errorf("links extraction looks too small for HN, len=%d:\n%s", len(out), out)
	}
}

// TestRealWorldExtractForm: the saucedemo login form must come back as
// [{ref,role,name}] JSON for its controls (Username, Password textboxes + refs),
// from the cached tree (no CDP). The agent can then fill via act/click by ref.
func TestRealWorldExtractForm(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	out := callTool(t, sess, ctx, "extract", map[string]any{"kind": "form"})
	t.Logf("form:\n%s", out)
	if !strings.Contains(out, "Username") || !strings.Contains(out, "Password") {
		t.Errorf("form JSON should include the Username + Password controls, got:\n%s", out)
	}
	if !strings.Contains(out, "textbox") {
		t.Errorf("form JSON should show the textbox role, got:\n%s", out)
	}
	if !strings.Contains(out, "\"ref\"") {
		t.Errorf("form JSON should carry refs the agent can act on, got:\n%s", out)
	}
}

// TestRealWorldExtractArticle: a Wikipedia article's <main> must come back as
// readable text (not JSON-quoted), so the agent reads content not ref-lines.
func TestRealWorldExtractArticle(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Software_agent"})
	out := callTool(t, sess, ctx, "extract", map[string]any{"kind": "article"})
	t.Logf("article (truncated): %s", safeHead(out, 300))
	if len(out) < 200 {
		t.Fatalf("article text too short, len=%d:\n%s", len(out), out)
	}
	// Should be plain text (a JSON-encoded string would start with a quote).
	if strings.HasPrefix(strings.TrimSpace(out), `"`) {
		t.Errorf("article should be plain text, not JSON-quoted, got:\n%s", safeHead(out, 100))
	}
	if !strings.Contains(strings.ToLower(out), "agent") {
		t.Errorf("article on 'Software agent' should mention agent, got:\n%s", safeHead(out, 200))
	}
}

// TestRealWorldExtractList: the-internet's homepage is a <ul> of example links;
// extract list must return the item texts as a JSON array.
func TestRealWorldExtractList(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://the-internet.herokuapp.com/"})
	out := callTool(t, sess, ctx, "extract", map[string]any{"kind": "list"})
	t.Logf("list (truncated): %s", safeHead(out, 400))
	if !strings.HasPrefix(strings.TrimSpace(out), "[") {
		t.Fatalf("extract list should return a JSON array, got:\n%s", safeHead(out, 200))
	}
	// The homepage lists examples like "Checkboxes", "Dropdown", "Frames", ...
	if !strings.Contains(out, "Dropdown") && !strings.Contains(out, "Checkboxes") {
		t.Errorf("list should contain known the-internet example items, got:\n%s", safeHead(out, 400))
	}
}

// TestRealWorldExtractNotFound: extracting a kind the page doesn't have must
// return isError with a helpful pointer (not a crash, not empty success).
func TestRealWorldExtractNotFound(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})
	res := callToolResult(t, sess, ctx, "extract", map[string]any{"kind": "table"})
	text := contentText(res)
	if !res.IsError {
		t.Errorf("extract table on a page with no table should be isError, got:\n%s", text)
	}
	if !strings.Contains(text, "no table") {
		t.Errorf("should say 'no table found', got:\n%s", text)
	}
}

func safeHead(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
