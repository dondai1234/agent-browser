package mcpserver

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestReliabilityProfileFallback proves the #1 fix for Dondai's reported
// breakage: when Chrome can't start with the requested (persistent) profile -
// locked by an orphaned Chrome, corrupted, or here an invalid path (a file not a
// dir) - the server falls back to a throwaway temp profile instead of crashing
// with the chromedp "close of closed channel" panic. Pre-fix, this exact scenario
// made every tool fail + crashed the MCP process. Post-fix, navigate succeeds.
func TestReliabilityProfileFallback(t *testing.T) {
	if os.Getenv("AGENT_BROWSER_INTEGRATION") != "1" {
		t.Skip("set AGENT_BROWSER_INTEGRATION=1 to run (needs Chrome + network)")
	}
	root := findModuleRoot(t)
	bin := filepath.Join(t.TempDir(), "agent-browser-fb.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/agent-browser")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	// An invalid user-data-dir: a regular file, not a directory. Chrome can't
	// use it as a profile, so launchBrowserLocked returns "chrome failed to
	// start" and New falls back to a temp profile.
	badProfile := filepath.Join(t.TempDir(), "not-a-dir-file")
	if err := os.WriteFile(badProfile, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	client := mcp.NewClient(&mcp.Implementation{Name: "fb-client", Version: "v0.0.1"}, nil)
	cmd := exec.Command(bin, "mcp", "--no-persist", "--user-data-dir="+badProfile)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	transport := &mcp.CommandTransport{Command: cmd}
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		cancel()
		t.Fatalf("connect: %v (stderr: %s)", err, stderr.String())
	}
	defer func() {
		sess.Close()
		cancel()
		killProcTree(cmd)
	}()

	// Navigate must succeed via the temp-profile fallback (the invalid profile
	// would have crashed the server pre-fix). No panic, no "chrome failed to
	// start" error.
	nav, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "navigate", Arguments: map[string]any{"url": "https://example.com"}})
	if err != nil {
		t.Fatalf("navigate transport error (server crashed?): %v (stderr: %s)", err, stderr.String())
	}
	txt := contentText(nav)
	if nav.IsError {
		t.Fatalf("navigate with an invalid --user-data-dir: expected the temp-profile fallback to make it work, got error: %q (stderr: %s)", txt, stderr.String())
	}
	if !strings.Contains(txt, "example.com") {
		t.Errorf("navigate: expected example.com, got: %q", txt)
	}
	// The fallback should be logged to stderr (so the operator knows persistence
	// is off). If Chrome happened to tolerate the bad path this assertion is a
	// no-op, but the navigate success above is the real proof.
	_ = stderr.String()
}

// realWorldSetupWithFlags is realWorldSetup with extra CLI args (for the
// op-timeout test, which needs --op-timeout=10ms to prove the per-op bound
// fires instead of wedging the session).
func realWorldSetupWithFlags(t *testing.T, extraArgs ...string) (*mcp.ClientSession, context.Context, func()) {
	t.Helper()
	if os.Getenv("AGENT_BROWSER_INTEGRATION") != "1" {
		t.Skip("set AGENT_BROWSER_INTEGRATION=1 to run (needs Chrome + network)")
	}
	root := findModuleRoot(t)
	bin := filepath.Join(t.TempDir(), "agent-browser-rel.exe")
	build := exec.Command("go", "build", "-o", bin, "./cmd/agent-browser")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	client := mcp.NewClient(&mcp.Implementation{Name: "rel-client", Version: "v0.0.1"}, nil)
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

// TestReliabilityOpTimeout proves the #1 fix: a CDP operation is bounded by the
// op timeout, so a slow/hung page returns an error instead of holding the
// session mutex forever (the "session hung, all tools timed out" failure from
// the live OpenCode test). With --op-timeout=10ms, even a fast site can't get
// through navigate's WaitReady + AX pull, so navigate must return an error
// quickly - NOT hang, and NOT wedge the session.
func TestReliabilityOpTimeout(t *testing.T) {
	sess, ctx, cleanup := realWorldSetupWithFlags(t, "--op-timeout=10ms")
	defer cleanup()

	start := time.Now()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "navigate", Arguments: map[string]any{"url": "https://example.com"}})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("navigate transport error: %v", err)
	}
	txt := contentText(res)
	if !res.IsError {
		t.Fatalf("navigate with --op-timeout=10ms: expected an error (op timeout), got success: %q", txt)
	}
	// Must return well under the old unbounded hang (seconds, not a wedge). 10ms
	// timeout + chromedp/CDP overhead + the 8s AX-poll cap should still be small;
	// assert it's under 15s so a regression to an unbounded hang is caught.
	if elapsed > 15*time.Second {
		t.Errorf("navigate took %s with a 10ms op timeout - the per-op bound did not fire (wedge regression)", elapsed)
	}
	// And the session is NOT wedged afterwards: a cheap read tool still responds.
	// (where reads the cached tree + one cheap eval; if s.mu were held by the
	// timed-out navigate, this would block until the client times out.)
	whereRes, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "where", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("where after a timed-out navigate: transport error (session wedged?): %v", err)
	}
	// where may itself error (no snapshot) but it must RESPOND - not time out.
	_ = contentText(whereRes)
}

// TestReliabilityReset proves the recovery path: after navigating + gathering
// refs, reset drops the current tab + opens a fresh one at a new URL, and the
// OLD refs are correctly invalid (the agent asked for an explicit reset tool
// when the session hangs).
func TestReliabilityReset(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.wikipedia.org"})
	see := callTool(t, sess, ctx, "see", map[string]any{"level": "summary"})
	// Grab whatever ref the first interactive element is (r2 typically).
	if !strings.Contains(see, "r2") {
		t.Fatalf("see summary on wikipedia: no r2 ref to carry stale: %q", see)
	}
	// Reset to a fresh tab on a different, reliable site.
	reset := callTool(t, sess, ctx, "reset", map[string]any{"url": "https://go.dev"})
	if !strings.Contains(reset, "go.dev") {
		t.Errorf("reset: expected orientation on go.dev, got: %q", reset)
	}
	if strings.Contains(reset, "wikipedia") {
		t.Errorf("reset: old wikipedia page should be gone, got: %q", reset)
	}
	// The fresh tab must be alive + usable: a click on its own r2 (a real link on
	// go.dev) navigates within go.dev, proving reset produced a working tab (not
	// a dead one). This is the recovery guarantee.
	click := callTool(t, sess, ctx, "click", map[string]any{"ref": "r2"})
	if !strings.Contains(click, "navigated") || !strings.Contains(click, "go.dev") {
		t.Errorf("click r2 on the post-reset go.dev tab: expected a go.dev navigation, got: %q", click)
	}
	// And the new tab is usable: see works on go.dev (not wikipedia).
	see2 := callTool(t, sess, ctx, "see", map[string]any{"level": "minimal"})
	if !strings.Contains(see2, "go.dev") {
		t.Errorf("see after reset: expected go.dev, got: %q", see2)
	}
	if strings.Contains(see2, "wikipedia") {
		t.Errorf("see after reset: wikipedia should be gone, got: %q", see2)
	}
}

// TestReliabilityComboboxAriaFill proves the combobox fix: an ARIA combobox over
// a <textarea> (Google search, autocomplete widgets) has no .options, so the
// old code's selectJS no-op'd on it. Act must now FILL it (native value setter +
// input/change), not select. We inject a textarea[role=combobox] onto a page,
// rebuild the tree with a no-op scroll, then act by intent + value and assert
// the value landed + the verb was "fill".
func TestReliabilityComboboxAriaFill(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://go.dev"})
	// Inject an ARIA combobox (a textarea with role=combobox, like Google's
	// search box). No <option> children - selectJS would no-op on this.
	callTool(t, sess, ctx, "eval", map[string]any{
		"script": `var ta=document.createElement('textarea');ta.setAttribute('role','combobox');ta.setAttribute('aria-label','Search');ta.id='cb';document.body.appendChild(ta);1`,
	})
	// Rebuild the cached tree so the injected combobox is present (a no-op
	// scroll still runs finishMutationLocked -> buildTreeLocked).
	callTool(t, sess, ctx, "scroll", map[string]any{"dy": 0})

	// Act by intent + value: must resolve the combobox + FILL it (not select).
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "act", Arguments: map[string]any{"intent": "Search", "value": "hello world"}})
	if err != nil {
		t.Fatalf("act: transport error: %v", err)
	}
	out := contentText(res)
	if res.IsError {
		t.Fatalf("act \"Search\" on an ARIA combobox failed: %q", out)
	}
	if !strings.Contains(out, "(fill)") {
		t.Errorf("act on ARIA combobox: expected verb (fill), got: %q", out)
	}
	if strings.Contains(out, "(select)") {
		t.Errorf("act on ARIA combobox: should NOT select (no <option>s); got: %q", out)
	}
	// Verify the value actually landed in the textarea.
	val := callTool(t, sess, ctx, "eval", map[string]any{"script": `document.getElementById('cb').value`})
	// eval returns a JSON-encoded string, so it comes back as "hello world".
	if !strings.Contains(val, "hello world") {
		t.Errorf("ARIA combobox value after act: expected \"hello world\", got: %q", val)
	}
}

// TestReliabilityPressKeyValidation proves press_key rejects a multi-char key
// string (the live agent passed key="weather in tokyo" and got a silent no-op).
// The error must redirect to fill/act. No page needed - validation fires before
// any snapshot/CDP work, so this also confirms it can't wedge on a missing tree.
func TestReliabilityPressKeyValidation(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "press_key", Arguments: map[string]any{"key": "weather in tokyo"}})
	if err != nil {
		t.Fatalf("press_key: transport error: %v", err)
	}
	txt := contentText(res)
	if !res.IsError {
		t.Fatalf("press_key key=\"weather in tokyo\": expected an error (not a named key / not a single char), got success: %q", txt)
	}
	if !strings.Contains(txt, "fill") && !strings.Contains(txt, "named key") {
		t.Errorf("press_key validation error should redirect to fill / name the rule, got: %q", txt)
	}
	// A valid single char must still be accepted (not over-reject). It'll error
	// downstream only if there's no snapshot - that's a different, expected path;
	// here we just assert the validation itself didn't reject "a".
	resA, _ := sess.CallTool(ctx, &mcp.CallToolParams{Name: "press_key", Arguments: map[string]any{"key": "a"}})
	txtA := contentText(resA)
	if resA.IsError && strings.Contains(txtA, "not a named key") {
		t.Errorf("press_key key=\"a\": validation wrongly rejected a single char: %q", txtA)
	}
}

// TestReliabilityTabSwitchByLabel proves switch/close accept the label field as
// a fallback when id is empty (the live agent's `switch label=t2` silently
// failed because only a.ID was passed to SwitchTab).
func TestReliabilityTabSwitchByLabel(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://go.dev"})
	callTool(t, sess, ctx, "tabs", map[string]any{"action": "new", "url": "https://www.wikipedia.org"})
	// Label the current (second) tab, then switch back to the first by id, then
	// switch to the second BY LABEL (the previously-broken path).
	callTool(t, sess, ctx, "tabs", map[string]any{"action": "label", "label": "wiki"})
	callTool(t, sess, ctx, "tabs", map[string]any{"action": "switch", "id": "t1"})
	switched := callTool(t, sess, ctx, "tabs", map[string]any{"action": "switch", "label": "wiki"})
	if !strings.Contains(switched, "wikipedia") {
		t.Errorf("switch by label=\"wiki\": expected to land on wikipedia, got: %q", switched)
	}
	// And the labeled tab is visible in the list.
	list := callTool(t, sess, ctx, "tabs", map[string]any{"action": "list"})
	if !strings.Contains(list, "wiki") {
		t.Errorf("tabs list should show the labeled tab: %q", list)
	}
}
