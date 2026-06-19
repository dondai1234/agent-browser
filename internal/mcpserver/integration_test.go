package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// findModuleRoot walks up from the test's working directory to find go.mod.
func findModuleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found above test directory")
		}
		dir = parent
	}
}

func contentText(res *mcp.CallToolResult) string {
	var sb strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			sb.WriteString(tc.Text)
		}
	}
	return sb.String()
}

// callTool calls a tool, fails the test on transport/protocol errors or
// isError results, and returns the text content.
func callTool(t *testing.T, sess *mcp.ClientSession, ctx context.Context, name string, args map[string]any) string {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := contentText(res)
	if res.IsError {
		t.Fatalf("%s returned tool error: %s", name, text)
	}
	return text
}

// TestIntegrationEndToEnd builds the binary, starts the MCP server via the SDK
// client, and exercises navigate/see/find/click/tabs over
// the real protocol + Chrome.
//
// Skipped unless AGENT_BROWSER_INTEGRATION=1 (needs Chrome). Run locally:
//
//	AGENT_BROWSER_INTEGRATION=1 go test ./internal/mcpserver/
func TestIntegrationEndToEnd(t *testing.T) {
	if os.Getenv("AGENT_BROWSER_INTEGRATION") != "1" {
		t.Skip("set AGENT_BROWSER_INTEGRATION=1 to run (needs Chrome)")
	}
	root := findModuleRoot(t)
	bin := filepath.Join(t.TempDir(), "agent-browser-test.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/agent-browser")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build server: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	client := mcp.NewClient(&mcp.Implementation{Name: "smoke-client", Version: "v0.0.1"}, nil)
	transport := &mcp.CommandTransport{Command: exec.Command(bin, "mcp")}
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer sess.Close()

	// navigate → see → find → click (act-and-see) on example.com.
	nav := callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})
	if !strings.Contains(nav, "example.com") {
		t.Errorf("navigate: %q", nav)
	}
	see := callTool(t, sess, ctx, "see", map[string]any{})
	if !strings.Contains(see, "Example Domain") {
		t.Errorf("see: %q", see)
	}
	find := callTool(t, sess, ctx, "find", map[string]any{"text": "Learn more", "exact": true})
	if !strings.Contains(find, "Learn more") {
		t.Errorf("find: %q", find)
	}
	click := callTool(t, sess, ctx, "click", map[string]any{"ref": "r2"})
	if !strings.Contains(click, "navigated:") || !strings.Contains(click, "iana.org") {
		t.Errorf("click act-and-see: %q", click)
	}

	// tabs: list (t1), new (example.com), list (t1+t2), switch t1, close t2, list (t1).
	if list1 := callTool(t, sess, ctx, "tabs", map[string]any{"action": "list"}); !strings.Contains(list1, "t1") {
		t.Errorf("tabs list: %q", list1)
	}
	newTab := callTool(t, sess, ctx, "tabs", map[string]any{"action": "new", "url": "https://example.com"})
	if !strings.Contains(newTab, "Example Domain") {
		t.Errorf("tabs new orientation: %q", newTab)
	}
	if list2 := callTool(t, sess, ctx, "tabs", map[string]any{"action": "list"}); !strings.Contains(list2, "t1") || !strings.Contains(list2, "t2") {
		t.Errorf("tabs list after new: %q", list2)
	}
	callTool(t, sess, ctx, "tabs", map[string]any{"action": "switch", "id": "t1"})
	callTool(t, sess, ctx, "tabs", map[string]any{"action": "close", "id": "t2"})
	if list3 := callTool(t, sess, ctx, "tabs", map[string]any{"action": "list"}); strings.Contains(list3, "t2") {
		t.Errorf("t2 should be closed: %q", list3)
	}
}
