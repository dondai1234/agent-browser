package mcpserver

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
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

// callTool calls a tool, fails on transport/protocol errors or isError results,
// and returns the text content.
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

// callToolResult returns the raw result (including isError) so a test can assert
// an error path without failing the harness.
func callToolResult(t *testing.T, sess *mcp.ClientSession, ctx context.Context, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// realWorldSetup builds the binary and connects an MCP client. Gated by
// AGENT_BROWSER_INTEGRATION=1 (needs Chrome + network).
func realWorldSetup(t *testing.T) (*mcp.ClientSession, context.Context, func()) {
	return realWorldSetupWithFlags(t)
}

// realWorldSetupWithFlags builds the binary + connects an MCP client, passing
// extra args to the `mcp` subcommand (e.g. --allow-insecure-schemes for
// data/localhost fixtures, --no-cookie-dismiss to test the opt-out). Gated by
// AGENT_BROWSER_INTEGRATION=1.
func realWorldSetupWithFlags(t *testing.T, extraArgs ...string) (*mcp.ClientSession, context.Context, func()) {
	t.Helper()
	if os.Getenv("AGENT_BROWSER_INTEGRATION") != "1" {
		t.Skip("set AGENT_BROWSER_INTEGRATION=1 to run (needs Chrome + network)")
	}
	root := findModuleRoot(t)
	bin := filepath.Join(t.TempDir(), "goshawk-v3.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/goshawk")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	client := mcp.NewClient(&mcp.Implementation{Name: "v3-client", Version: "v0.0.1"}, nil)
	args := append([]string{"mcp", "--no-persist"}, extraArgs...)
	cmd := exec.Command(bin, args...)
	transport := &mcp.CommandTransport{Command: cmd}
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect: %v", err)
	}
	return sess, ctx, func() {
		sess.Close()
		cancel()
		killProcTree(cmd)
	}
}

// killProcTree kills the server + its Chrome so processes don't accumulate.
func killProcTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := strconv.Itoa(cmd.Process.Pid)
	if runtime.GOOS == "windows" {
		_ = exec.Command("taskkill", "/PID", pid, "/T", "/F").Run()
	} else {
		_ = cmd.Process.Kill()
	}
}

// refFor parses a find/see-refs result and returns the ref (rN) of the first line
// whose name contains nameContains.
func refFor(t *testing.T, out, nameContains string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, nameContains) {
			continue
		}
		start := strings.Index(line, "[r")
		if start < 0 {
			continue
		}
		end := strings.IndexByte(line[start+1:], ']')
		if end < 0 {
			continue
		}
		return line[start+1 : start+1+end]
	}
	t.Fatalf("ref for %q not found in:\n%s", nameContains, out)
	return ""
}
