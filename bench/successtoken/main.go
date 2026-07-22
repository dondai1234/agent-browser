package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callFunc invokes one tool on the connected MCP server. The harness wraps it to
// count I/O chars (sent args JSON + returned text) so the script doesn't have to.
type callFunc func(tool string, args map[string]any) (string, error)

// task is one benchmark task: a name + a per-tool script. A script returns nil
// on success (assertion passed); any error means failure (Success=false).
type task struct {
	name    string
	scripts map[string]func(run callFunc, base string) error
}

var tasks []task

func init() {
	tasks = []task{
		{name: "login", scripts: map[string]func(callFunc, string) error{
			"goshawk":        taskLoginAB,
			"playwright-mcp": taskLoginPW,
		}},
		{name: "search-extract", scripts: map[string]func(callFunc, string) error{
			"goshawk":        taskSearchAB,
			"playwright-mcp": taskSearchPW,
		}},
		{name: "form-submit", scripts: map[string]func(callFunc, string) error{
			"goshawk":        taskFormAB,
			"playwright-mcp": taskFormPW,
		}},
		{name: "multi-page-nav", scripts: map[string]func(callFunc, string) error{
			"goshawk":        taskNavAB,
			"playwright-mcp": taskNavPW,
		}},
		{name: "lazy-list-scroll", scripts: map[string]func(callFunc, string) error{
			"goshawk":        taskListAB,
			"playwright-mcp": taskListPW,
		}},
	}
}

// errFail wraps an assertion failure (distinct from a tool/transport error so the
// report can tell "the task didn't achieve its goal" from "the tool blew up").
type errFail string

func (e errFail) Error() string { return string(e) }

// assert is a tiny helper for scripts: returns an errFail if cond is false.
func assert(cond bool, msg string) error {
	if !cond {
		return errFail("assertion: " + msg)
	}
	return nil
}

// --- goshawk task scripts (the v2 efficient path: act + read + scroll) ---
// These deliberately use the intent-first act tool (one call = resolve + act +
// verdict) + read, the path the v2 thesis claims is token-cheap. No find/see
// round-trips unless needed.

func taskLoginAB(run callFunc, base string) error {
	if _, err := run("navigate", map[string]any{"url": base + "/login"}); err != nil {
		return err
	}
	if _, err := run("act", map[string]any{"intent": "Username", "value": "alice"}); err != nil {
		return err
	}
	if _, err := run("act", map[string]any{"intent": "Password", "value": "pw123"}); err != nil {
		return err
	}
	if _, err := run("act", map[string]any{"intent": "Sign in"}); err != nil {
		return err
	}
	out, err := run("read", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "Welcome alice"), "expected 'Welcome alice' in read, got: "+oneLine(out))
}

func taskSearchAB(run callFunc, base string) error {
	if _, err := run("navigate", map[string]any{"url": base + "/search"}); err != nil {
		return err
	}
	if _, err := run("act", map[string]any{"intent": "Search", "value": "shoes"}); err != nil {
		return err
	}
	if _, err := run("press_key", map[string]any{"key": "Enter"}); err != nil {
		return err
	}
	out, err := run("read", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "7 results"), "expected '7 results', got: "+oneLine(out))
}

func taskFormAB(run callFunc, base string) error {
	if _, err := run("navigate", map[string]any{"url": base + "/form"}); err != nil {
		return err
	}
	for _, f := range []struct{ intent, val string }{
		{"Full name", "Bishesh"},
		{"Email", "b@x.com"},
		{"Address", "Kathmandu"},
	} {
		if _, err := run("act", map[string]any{"intent": f.intent, "value": f.val}); err != nil {
			return err
		}
	}
	if _, err := run("act", map[string]any{"intent": "Plan", "value": "Pro"}); err != nil {
		return err
	}
	if _, err := run("act", map[string]any{"intent": "Place order"}); err != nil {
		return err
	}
	out, err := run("read", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "Bishesh / b@x.com / Kathmandu / pro"), "expected submitted summary, got: "+oneLine(out))
}

func taskNavAB(run callFunc, base string) error {
	if _, err := run("navigate", map[string]any{"url": base + "/page1"}); err != nil {
		return err
	}
	if _, err := run("act", map[string]any{"intent": "Next"}); err != nil {
		return err
	}
	if _, err := run("act", map[string]any{"intent": "Next"}); err != nil {
		return err
	}
	out, err := run("read", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "Step 3 - done"), "expected 'Step 3 - done', got: "+oneLine(out))
}

func taskListAB(run callFunc, base string) error {
	if _, err := run("navigate", map[string]any{"url": base + "/list"}); err != nil {
		return err
	}
	for i := 0; i < 6; i++ {
		if _, err := run("scroll", map[string]any{"dy": 1500}); err != nil {
			return err
		}
	}
	out, err := run("read", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "Item 20"), "expected 'Item 20' after scrolling, got: "+oneLine(out))
}

// --- playwright-mcp task scripts (placeholder until the runner is verified) ---
// These use the playwright-mcp tool surface (browser_navigate, browser_snapshot,
// browser_type, browser_click, browser_select_option, browser_press_key,
// browser_evaluate). Refs are parsed from the snapshot text ([ref=eNN] next to a
// label). Filled in once the runner is verified against the real server.

func pwRef(snap, label string) string {
	// playwright-mcp snapshot lines look like: - textbox "Username" [ref=e3].
	// Headings/generic containers ALSO get refs in pw (unlike goshawk), so a
	// label substring like "Search" can match the heading "Search products"
	// before the real control. Skip non-interactive roles so we always land on a
	// typeable/clickable element.
	for _, line := range strings.Split(snap, "\n") {
		lt := strings.TrimSpace(line)
		if strings.HasPrefix(lt, "- heading") || strings.HasPrefix(lt, "- generic") || strings.HasPrefix(lt, "- text:") {
			continue
		}
		if strings.Contains(line, label) {
			i := strings.Index(line, "[ref=")
			if i < 0 {
				continue
			}
			j := strings.IndexByte(line[i:], ']')
			if j < 0 {
				continue
			}
			return line[i+5 : i+j]
		}
	}
	return ""
}

func taskLoginPW(run callFunc, base string) error {
	if _, err := run("browser_navigate", map[string]any{"url": base + "/login"}); err != nil {
		return err
	}
	snap, err := run("browser_snapshot", map[string]any{})
	if err != nil {
		return err
	}
	u := pwRef(snap, "Username")
	p := pwRef(snap, "Password")
	si := pwRef(snap, "Sign in")
	if u == "" || p == "" || si == "" {
		return errFail("pw: could not locate login refs in snapshot")
	}
	if _, err := run("browser_type", map[string]any{"target": u, "text": "alice"}); err != nil {
		return err
	}
	if _, err := run("browser_type", map[string]any{"target": p, "text": "pw123"}); err != nil {
		return err
	}
	if _, err := run("browser_click", map[string]any{"target": si}); err != nil {
		return err
	}
	out, err := run("browser_snapshot", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "Welcome alice"), "expected 'Welcome alice', got: "+oneLine(out))
}

func taskSearchPW(run callFunc, base string) error {
	if _, err := run("browser_navigate", map[string]any{"url": base + "/search"}); err != nil {
		return err
	}
	snap, err := run("browser_snapshot", map[string]any{})
	if err != nil {
		return err
	}
	q := pwRef(snap, "Search")
	if q == "" {
		return errFail("pw: no Search ref")
	}
	// browser_type submit:true types + presses Enter, which the fixture's keydown
	// handler renders results on (one call vs goshawk's act + press_key).
	if _, err := run("browser_type", map[string]any{"target": q, "text": "shoes", "submit": true}); err != nil {
		return err
	}
	out, err := run("browser_snapshot", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "7 results"), "expected '7 results', got: "+oneLine(out))
}

func taskFormPW(run callFunc, base string) error {
	if _, err := run("browser_navigate", map[string]any{"url": base + "/form"}); err != nil {
		return err
	}
	snap, err := run("browser_snapshot", map[string]any{})
	if err != nil {
		return err
	}
	type fld struct{ label, val string }
	fields := []fld{{"Full name", "Bishesh"}, {"Email", "b@x.com"}, {"Address", "Kathmandu"}}
	for _, f := range fields {
		r := pwRef(snap, f.label)
		if r == "" {
			return errFail("pw: no ref for " + f.label)
		}
		if _, err := run("browser_type", map[string]any{"target": r, "text": f.val}); err != nil {
			return err
		}
	}
	pr := pwRef(snap, "Plan")
	if pr == "" {
		return errFail("pw: no Plan ref")
	}
	if _, err := run("browser_select_option", map[string]any{"target": pr, "values": []string{"Pro"}}); err != nil {
		return err
	}
	po := pwRef(snap, "Place order")
	if po == "" {
		return errFail("pw: no Place order ref")
	}
	if _, err := run("browser_click", map[string]any{"target": po}); err != nil {
		return err
	}
	out, err := run("browser_snapshot", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "Bishesh / b@x.com / Kathmandu / pro"), "expected submitted summary, got: "+oneLine(out))
}

func taskNavPW(run callFunc, base string) error {
	if _, err := run("browser_navigate", map[string]any{"url": base + "/page1"}); err != nil {
		return err
	}
	for i := 0; i < 2; i++ {
		snap, err := run("browser_snapshot", map[string]any{})
		if err != nil {
			return err
		}
		r := pwRef(snap, "Next")
		if r == "" {
			return errFail("pw: no Next ref")
		}
		if _, err := run("browser_click", map[string]any{"target": r}); err != nil {
			return err
		}
	}
	out, err := run("browser_snapshot", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "Step 3 - done"), "expected 'Step 3 - done', got: "+oneLine(out))
}

func taskListPW(run callFunc, base string) error {
	if _, err := run("browser_navigate", map[string]any{"url": base + "/list"}); err != nil {
		return err
	}
	for i := 0; i < 6; i++ {
		if _, err := run("browser_evaluate", map[string]any{"function": "() => { window.scrollBy(0, 1500); }"}); err != nil {
			return err
		}
	}
	out, err := run("browser_snapshot", map[string]any{})
	if err != nil {
		return err
	}
	return assert(strings.Contains(out, "Item 20"), "expected 'Item 20' after scrolling, got: "+oneLine(out))
}

// --- runners: spawn an MCP server + wrap CallTool with char counting ---

type runner struct {
	name    string
	command []string // argv to spawn the MCP server (stdio)
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

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 120 {
		s = s[:120] + "..."
	}
	return s
}

func main() {
	compare := flag.Bool("compare", false, "also run the playwright-mcp head-to-head (needs npx + @playwright/mcp; auto-installs on first run)")
	bin := flag.String("bin", "", "path to a prebuilt goshawk binary; if empty, builds one to a temp path")
	timeout := flag.Duration("timeout", 5*time.Minute, "per-runner wall-clock budget")
	list := flag.Bool("list", false, "list each runner's tools + their input schema properties, then exit (no tasks run)")
	flag.Parse()

	fx := newFixtures()
	defer fx.Close()

	// Build (or locate) the goshawk binary.
	abBin := *bin
	if abBin == "" {
		tmp, err := buildBinary()
		if err != nil {
			fmt.Fprintf(os.Stderr, "build goshawk: %v\n", err)
			os.Exit(1)
		}
		abBin = tmp
		defer os.Remove(abBin)
	}

	runners := []runner{
		{name: "goshawk", command: []string{abBin, "mcp", "--no-persist"}},
	}
	if *compare {
		runners = append(runners, runner{name: "playwright-mcp", command: []string{"npx", "-y", "@playwright/mcp@latest", "--headless"}})
		toolOrder = append(toolOrder, "playwright-mcp")
	}
	toolOrder = append([]string{"goshawk"}, toolOrder...)

	if *list {
		for _, r := range runners {
			listTools(r)
		}
		return
	}

	var results []taskResult
	for _, r := range runners {
		rctx, cancel := context.WithTimeout(context.Background(), *timeout)
		// charCount is shared with the call closure so we can read + reset per task.
		charCount := new(int)
		call, cleanup, err := connectRunnerCounted(rctx, r, charCount)
		if err != nil {
			fmt.Fprintf(os.Stderr, "runner %s: %v\n", r.name, err)
			cancel()
			continue
		}
		for _, tk := range tasks {
			script, ok := tk.scripts[r.name]
			if !ok {
				continue
			}
			*charCount = 0
			steps := 0
			countingCall := func(tool string, args map[string]any) (string, error) {
				steps++
				return call(tool, args)
			}
			err := script(countingCall, fx.base)
			res := taskResult{Task: tk.name, Tool: r.name, Chars: *charCount, Tokens: float64(*charCount) / 4, Steps: steps}
			if err == nil {
				res.Success = true
			} else {
				if ef, is := err.(errFail); is {
					res.Err = string(ef)
				} else {
					res.Err = oneLine(err.Error())
				}
			}
			results = append(results, res)
			fmt.Fprintln(os.Stderr, res)
		}
		cleanup()
		cancel()
	}

	report(results)
}

// connectRunnerCounted spawns the MCP server, connects an SDK client, and
// returns a callFunc that counts I/O chars per call into the externally-owned
// counter (so the main loop can reset it per task + read the total after). The
// caller closes the session via the returned cleanup.
func connectRunnerCounted(ctx context.Context, r runner, chars *int) (callFunc, func(), error) {
	if len(r.command) == 0 {
		return nil, nil, fmt.Errorf("runner %q has no command", r.name)
	}
	cmd := exec.CommandContext(ctx, r.command[0], r.command[1:]...)
	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{Name: "st-bench", Version: "0.1"}, nil)
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, nil, fmt.Errorf("connect %s: %w", r.name, err)
	}
	cleanup := func() { _ = sess.Close() }
	call := func(tool string, args map[string]any) (string, error) {
		argJSON, _ := json.Marshal(args)
		*chars += len(argJSON)
		res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: tool, Arguments: args})
		if err != nil {
			return "", err
		}
		out := contentText(res)
		*chars += len(out)
		if res.IsError {
			return out, fmt.Errorf("tool %s isError: %s", tool, oneLine(out))
		}
		return out, nil
	}
	return call, cleanup, nil
}

func buildBinary() (string, error) {
	tmp, err := os.CreateTemp("", "goshawk-bench-*.exe")
	if err != nil {
		return "", err
	}
	tmp.Close()
	cmd := exec.Command("go", "build", "-o", tmp.Name(), "./cmd/goshawk")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		os.Remove(tmp.Name())
		return "", err
	}
	return tmp.Name(), nil
}

// listTools connects to a runner's MCP server, lists its tools, and prints each
// tool's required + optional input properties (the JSON schema fields), so we
// can verify the exact arg names/shape before writing task scripts for a tool
// whose surface differs from goshawk's.
func listTools(r runner) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, r.command[0], r.command[1:]...)
	transport := &mcp.CommandTransport{Command: cmd}
	client := mcp.NewClient(&mcp.Implementation{Name: "st-bench-list", Version: "0.1"}, nil)
	sess, err := client.Connect(ctx, transport, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "list %s: connect: %v\n", r.name, err)
		return
	}
	defer sess.Close()
	res, err := sess.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "list %s: ListTools: %v\n", r.name, err)
		return
	}
	fmt.Printf("\n=== %s: %d tools ===\n", r.name, len(res.Tools))
	for _, t := range res.Tools {
		// Only print the interaction tools we care about (keep output short).
		name := t.Name
		switch name {
		case "browser_navigate", "browser_snapshot", "browser_click", "browser_type",
			"browser_select_option", "browser_press_key", "browser_evaluate", "browser_fill_form":
		default:
			continue
		}
		var req, opt []string
		if sch, ok := t.InputSchema.(map[string]any); ok {
			if props, ok := sch["properties"].(map[string]any); ok {
				for prop := range props {
					req = append(req, prop)
				}
				sort.Strings(req)
			}
			if r2, ok := sch["required"].([]any); ok {
				for _, p := range r2 {
					if s, ok := p.(string); ok {
						opt = append(opt, s)
					}
				}
			}
		}
		fmt.Printf("  %-22s props=%v required=%v\n", name, req, opt)
	}
}
