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

// TestPersistentProfileCrossRestart: a login on one server process survives a
// full server restart when using --user-data-dir (cookies persist in the Chrome
// profile). The true "login persistence across sessions" gap.
func TestPersistentProfileCrossRestart(t *testing.T) {
	if os.Getenv("AGENT_BROWSER_INTEGRATION") != "1" {
		t.Skip("set AGENT_BROWSER_INTEGRATION=1 to run (needs Chrome + network)")
	}
	root := findModuleRoot(t)
	bin := filepath.Join(t.TempDir(), "agent-browser-persist.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/agent-browser")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	profileDir := t.TempDir()
	args := []string{"mcp", "--user-data-dir", profileDir}

	// --- Server 1: log in to saucedemo ---
	c1 := mcp.NewClient(&mcp.Implementation{Name: "persist-1", Version: "v0.0.1"}, nil)
	ctx1, cancel1 := context.WithTimeout(context.Background(), 60*time.Second)
	sess1, err := c1.Connect(ctx1, &mcp.CommandTransport{Command: exec.Command(bin, args...)}, nil)
	if err != nil {
		cancel1()
		t.Fatalf("connect server1: %v", err)
	}
	callTool(t, sess1, ctx1, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	tb := callTool(t, sess1, ctx1, "find", map[string]any{"role": "textbox"})
	callTool(t, sess1, ctx1, "fill", map[string]any{"ref": refFor(t, tb, "Username"), "value": "standard_user"})
	callTool(t, sess1, ctx1, "fill", map[string]any{"ref": refFor(t, tb, "Password"), "value": "secret_sauce"})
	btn := callTool(t, sess1, ctx1, "find", map[string]any{"role": "button"})
	callTool(t, sess1, ctx1, "click", map[string]any{"ref": refFor(t, btn, "Login")})
	sess1.Close()
	cancel1()
	// Let Chrome exit and flush the profile to disk.
	time.Sleep(2500 * time.Millisecond)

	// --- Server 2: fresh process, same profile dir. Navigate to inventory →
	// should still be authenticated (cookies persisted). ---
	c2 := mcp.NewClient(&mcp.Implementation{Name: "persist-2", Version: "v0.0.1"}, nil)
	ctx2, cancel2 := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel2()
	sess2, err := c2.Connect(ctx2, &mcp.CommandTransport{Command: exec.Command(bin, args...)}, nil)
	if err != nil {
		t.Fatalf("connect server2: %v", err)
	}
	defer sess2.Close()
	callTool(t, sess2, ctx2, "navigate", map[string]any{"url": "https://www.saucedemo.com/inventory.html"})
	body := callTool(t, sess2, ctx2, "read", map[string]any{})
	if !strings.Contains(body, "Sauce Labs") && !strings.Contains(body, "inventory") {
		t.Errorf("login did NOT persist across server restart (user-data-dir): %q", body)
	}
}
