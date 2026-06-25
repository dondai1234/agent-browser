package mcpserver

import (
	"context"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// countDebugChrome counts Chrome processes whose command line marks them as
// automation-launched (remote-debugging). Returns -1 if the platform check
// can't run, so the caller skips the process-count assertion and falls back to
// the behavioral one. Used to prove the MCP server does NOT launch Chrome
// eagerly on startup (only on the first navigate).
func countDebugChrome(t *testing.T) int {
	t.Helper()
	var out []byte
	var err error
	switch runtime.GOOS {
	case "windows":
		out, err = exec.Command("powershell", "-NoProfile", "-c",
			`Get-CimInstance Win32_Process -Filter "Name='chrome.exe'" | Where-Object { $_.CommandLine -like '*remote-debugging*' } | Measure-Object | ForEach-Object { $_.Count }`).Output()
	default:
		out, err = exec.Command("sh", "-c",
			`ps ax -o command= 2>/dev/null | grep -i chrome | grep remote-debugging | grep -v grep | wc -l`).Output()
	}
	if err != nil {
		return -1
	}
	n, e := strconv.Atoi(strings.TrimSpace(string(out)))
	if e != nil {
		return -1
	}
	return n
}

// TestLazyBrowserLaunch: the MCP server must NOT launch Chrome on startup. v2.2
// made New() lazy - Chrome spawns on the first page-opening op (navigate), not
// when the server boots. This test proves it three ways:
//  1. right after server connect, zero debug-Chrome processes (no eager launch);
//  2. a read-only op before navigate (where / find) does NOT launch Chrome - it
//     reports "no page snapshot yet" instead;
//  3. the first navigate launches Chrome + the page is reachable.
//
// The process-count assertion runs where the platform check works (Windows
// locally; CI skips live tests via the AGENT_BROWSER_INTEGRATION gate); on other
// platforms it falls back to the behavioral assertion alone.
func TestLazyBrowserLaunch(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	// 1. After server start, Chrome must NOT be running yet.
	if n := countDebugChrome(t); n >= 0 {
		if n != 0 {
			t.Fatalf("Chrome launched eagerly on server start; expected 0 debug-chrome processes, got %d", n)
		}
		t.Logf("OK: 0 debug-chrome processes right after server connect (no eager launch)")
	}

	// 2a. `where` before navigate reports no page (and must not launch Chrome).
	where := callTool(t, sess, ctx, "where", map[string]any{})
	if !strings.Contains(where, "no page snapshot yet") {
		t.Fatalf("where before navigate should say 'no page snapshot yet', got: %s", where)
	}
	// 2b. `find` before navigate errors no-snapshot (must not launch Chrome).
	res := callToolResult(t, sess, ctx, "find", map[string]any{"role": "button"})
	if !res.IsError || !strings.Contains(contentText(res), "no page snapshot yet") {
		t.Fatalf("find before navigate should error 'no page snapshot yet', got isError=%v: %s", res.IsError, contentText(res))
	}
	// 2c. The read-only ops did NOT launch Chrome.
	if n := countDebugChrome(t); n >= 0 && n != 0 {
		t.Fatalf("read-only ops launched Chrome; expected still 0, got %d", n)
	}

	// 3. The first navigate launches Chrome + works.
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})
	if n := countDebugChrome(t); n >= 0 && n < 1 {
		t.Fatalf("navigate should have launched Chrome, got %d debug-chrome processes", n)
	}
	out := callTool(t, sess, ctx, "where", map[string]any{})
	if !strings.Contains(out, "example.com") {
		t.Fatalf("where after navigate should show example.com, got: %s", out)
	}
}

var _ context.Context
var _ = mcp.CallToolResult{}

// TestIdleAutoClose: after a short idle timeout with no browser activity, Chrome
// is torn down (no orphan process for the rest of the session); the next
// navigate re-launches it seamlessly. Proves a one-shot browser use doesn't
// leave Chrome running for the whole chat. Uses a 6s test timeout (the real
// default is 10m) so the test runs fast.
func TestIdleAutoClose(t *testing.T) {
	sess, ctx, cleanup := realWorldSetupWithFlags(t, "--no-persist", "--idle-timeout=6s")
	defer cleanup()

	// Launch + confirm Chrome is up.
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})
	if n := countDebugChrome(t); n >= 0 && n < 1 {
		t.Fatalf("navigate should launch Chrome, got %d", n)
	}
	t.Logf("OK: Chrome up after navigate (%d debug-chrome processes)", countDebugChrome(t))

	// Idle wait: NO session tool calls (those would reset the idle timer). Only
	// sleep + OS-level process checks. After ~6s idle the reaper tears Chrome down.
	deadline := time.Now().Add(20 * time.Second)
	gone := false
	for time.Now().Before(deadline) {
		time.Sleep(2 * time.Second)
		if n := countDebugChrome(t); n == 0 {
			gone = true
			break
		}
	}
	if !gone {
		t.Fatalf("Chrome did not auto-close after idle timeout (still %d debug-chrome processes)", countDebugChrome(t))
	}
	t.Logf("OK: Chrome auto-closed after idle")

	// where now reports no page (browser was torn down) - without launching.
	where := callTool(t, sess, ctx, "where", map[string]any{})
	if !strings.Contains(where, "no page snapshot yet") {
		t.Fatalf("where after idle-close should say 'no page snapshot yet', got: %s", where)
	}

	// The next navigate re-launches Chrome (page state was lost - fresh nav).
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://example.com"})
	if n := countDebugChrome(t); n >= 0 && n < 1 {
		t.Fatalf("navigate after idle-close should re-launch Chrome, got %d", n)
	}
	out := callTool(t, sess, ctx, "where", map[string]any{})
	if !strings.Contains(out, "example.com") {
		t.Fatalf("where after re-launch should show example.com, got: %s", out)
	}
	t.Logf("OK: Chrome re-launched by next navigate, page reachable")
}
