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

// realWorldSetup builds the binary and connects an MCP client.
// Gated by AGENT_BROWSER_INTEGRATION=1 (needs Chrome + network).
func realWorldSetup(t *testing.T) (*mcp.ClientSession, context.Context, func()) {
	t.Helper()
	if os.Getenv("AGENT_BROWSER_INTEGRATION") != "1" {
		t.Skip("set AGENT_BROWSER_INTEGRATION=1 to run (needs Chrome + network)")
	}
	root := findModuleRoot(t)
	bin := filepath.Join(t.TempDir(), "agent-browser-rw.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/agent-browser")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	client := mcp.NewClient(&mcp.Implementation{Name: "rw-client", Version: "v0.0.1"}, nil)
	transport := &mcp.CommandTransport{Command: exec.Command(bin, "mcp")}
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect: %v", err)
	}
	return sess, ctx, func() { sess.Close(); cancel() }
}

// refFor parses a find result and returns the ref (rN) of the first line whose
// name contains nameContains.
func refFor(t *testing.T, findOut, nameContains string) string {
	t.Helper()
	for _, line := range strings.Split(findOut, "\n") {
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
	t.Fatalf("ref for %q not found in find output:\n%s", nameContains, findOut)
	return ""
}

// TestRealWorldSaucedemoLogin: a real React-ish SPA. Log in (fill username +
// password, click Login), verify the inventory page loads. Exercises fill on
// real inputs, click on a real submit, act-and-see navigation, and ref survival
// across a multi-step flow.
func TestRealWorldSaucedemoLogin(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	textboxes := callTool(t, sess, ctx, "find", map[string]any{"role": "textbox"})
	t.Logf("textboxes:\n%s", textboxes)
	userRef := refFor(t, textboxes, "Username")
	passRef := refFor(t, textboxes, "Password")

	buttons := callTool(t, sess, ctx, "find", map[string]any{"role": "button"})
	t.Logf("buttons:\n%s", buttons)
	loginRef := refFor(t, buttons, "Login")

	callTool(t, sess, ctx, "fill", map[string]any{"ref": userRef, "value": "standard_user"})
	callTool(t, sess, ctx, "fill", map[string]any{"ref": passRef, "value": "secret_sauce"})
	clickOut := callTool(t, sess, ctx, "click", map[string]any{"ref": loginRef})
	t.Logf("login click delta:\n%s", clickOut)

	// After login the URL becomes .../inventory.html. Confirm via read body.
	body := callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "inventory") && !strings.Contains(body, "Sauce Labs") {
		t.Errorf("login did not reach inventory; click=%q body=%q", clickOut, body)
	}
}

// TestRealWorldCheckboxes: toggle a checkbox on a real page and verify state.
func TestRealWorldCheckboxes(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://the-internet.herokuapp.com/checkboxes"})
	boxes := callTool(t, sess, ctx, "find", map[string]any{"role": "checkbox"})
	t.Logf("checkboxes:\n%s", boxes)
	if !strings.Contains(boxes, "checkbox") {
		t.Fatalf("no checkboxes found: %q", boxes)
	}
	// click the first checkbox ref
	first := refFor(t, boxes, "checkbox")
	callTool(t, sess, ctx, "click", map[string]any{"ref": first})
	after := callTool(t, sess, ctx, "find", map[string]any{"role": "checkbox"})
	if !strings.Contains(after, "checked") {
		t.Errorf("checkbox did not report checked after click: %q", after)
	}
}

// TestRealWorldDropdown: select an option in a real <select>.
func TestRealWorldDropdown(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://the-internet.herokuapp.com/dropdown"})
	combos := callTool(t, sess, ctx, "find", map[string]any{"role": "combobox"})
	t.Logf("comboboxes:\n%s", combos)
	ref := refFor(t, combos, "combobox")
	callTool(t, sess, ctx, "select", map[string]any{"ref": ref, "value": "Option 1"})
	body := callTool(t, sess, ctx, "read", map[string]any{})
	// We can't easily read the selected value via body; just assert no error path
	// (callTool already fails on isError). Log for manual inspection.
	t.Logf("after select, body:\n%s", body)
}

// TestRealWorldJavascriptAlerts: click a button that fires alert(). Expected
// to expose the dialog-handling gap (we don't auto-handle JS dialogs). If the
// click hangs because the alert blocks the JS thread, this test will time out
// - which is the signal we're looking for.
func TestRealWorldJavascriptAlerts(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://the-internet.herokuapp.com/javascript_alerts"})
	buttons := callTool(t, sess, ctx, "find", map[string]any{"role": "button"})
	t.Logf("alert buttons:\n%s", buttons)
	alertRef := refFor(t, buttons, "Click for JS Alert")

	clickOut := callTool(t, sess, ctx, "click", map[string]any{"ref": alertRef})
	t.Logf("alert click delta:\n%s", clickOut)
	body := callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "clicked") {
		t.Errorf("alert result not visible in body: %q", body)
	}
}
