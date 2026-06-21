// Command agent-browser is the entry point.
//
// Debug CLI (proving the engine):
//
//	go run ./cmd/agent-browser --url https://example.com                  # minimal orientation
//	go run ./cmd/agent-browser --url https://news.ycombinator.com --level summary
//	go run ./cmd/agent-browser --url https://news.ycombinator.com --find-role link --find-text More --find-exact
//	go run ./cmd/agent-browser --url https://example.com --click r2        # click ref, show the delta
//
// MCP server (the actual tool, for Cursor/Copilot/Claude Code):
//
//	agent-browser mcp                          # eval on by default; add --no-eval to disable
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime/debug"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
	"github.com/dondai1234/agent-browser/v2/internal/mcpserver"
	"github.com/dondai1234/agent-browser/v2/internal/snapshot"
)

// version is the build version. Overridden at release-build time via
// -ldflags "-X main.version=<tag>" (see .github/workflows/release.yml). For a
// plain `go install ...@vX.Y.Z` (no ldflags) it stays "dev" and versionString()
// falls back to the module version embedded in the build info, so go-install
// users still see the real version.
var version = "dev"

// versionString returns the version to report: the ldflags-injected version if
// set, else the module version from the build info (set by `go install ...@ver`),
// else "dev" for a local `go build`.
func versionString() string {
	if version != "" && version != "dev" {
		return version
	}
	if info, ok := debug.ReadBuildInfo(); ok {
		if v := info.Main.Version; v != "" && v != "(devel)" {
			return v
		}
	}
	return "dev"
}

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "--version" || os.Args[1] == "-version" || os.Args[1] == "version") {
		fmt.Println("agent-browser", versionString())
		return
	}
	if len(os.Args) > 1 && os.Args[1] == "mcp" {
		runMCP(os.Args[2:])
		return
	}
	runDebug()
}

// runMCP starts the MCP server over stdio.
func runMCP(args []string) {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	headless := fs.Bool("headless", true, "headless Chrome (uses --headless=new; --headless=false = headed, real GPU fingerprint - best for hard anti-bot targets)")
	userDataDirFlag := fs.String("user-data-dir", "", "persistent Chrome profile dir (overrides the default location); by default a profile is kept at <os config dir>/agent-browser so logins/cookies survive restarts")
	noPersist := fs.Bool("no-persist", false, "use a throwaway temp profile (no saved logins); by default a persistent profile is kept so the agent doesn't re-login every run")
	proxy := fs.String("proxy-server", "", "proxy URL (e.g. http://user:pass@host:port); a residential proxy is the #1 fix for IP-reputation bot blocks")
	userAgent := fs.String("user-agent", "", "override the User-Agent; empty = Chrome default")
	viewport := fs.String("viewport", "1366,768", "window size W,H")
	noStealth := fs.Bool("no-stealth", false, "disable anti-detection (stealth flags + init script + jittered mouse); on by default")
	allowInsecure := fs.Bool("allow-insecure-schemes", false, "allow file/javascript/data/about/blob URLs")
	noEval := fs.Bool("no-eval", false, "disable the eval tool (arbitrary page JS; enabled by default)")
	opTimeout := fs.Duration("op-timeout", 30*time.Second, "per-CDP-operation timeout: bounds any single browser call so a hung page returns an error instead of wedging the session (the 'all tools timed out' failure). Raise for very slow pages.")
	versionFlag := fs.Bool("version", false, "print version and exit")
	_ = fs.Parse(args)
	if *versionFlag {
		fmt.Println("agent-browser", versionString())
		return
	}

	// Resolve the profile dir: an explicit --user-data-dir wins; otherwise, by
	// default, persist to <os config dir>/agent-browser so logins/cookies/local
	// storage survive server restarts (the agent doesn't re-login each run).
	// --no-persist falls back to a throwaway temp profile (the old default).
	// Note: one persistent profile can only be used by one agent-browser process
	// at a time (Chrome locks it); run concurrent clients with separate
	// --user-data-dir paths.
	userDataDir := *userDataDirFlag
	if userDataDir == "" && !*noPersist {
		if d, err := os.UserConfigDir(); err == nil {
			userDataDir = filepath.Join(d, "agent-browser")
			if err := os.MkdirAll(userDataDir, 0o700); err != nil {
				log.Fatalf("create profile dir %s: %v", userDataDir, err)
			}
		}
	}

	w, h := 1366, 768
	if parts := strings.Split(*viewport, ","); len(parts) == 2 {
		if a, e1 := strconv.Atoi(strings.TrimSpace(parts[0])); e1 == nil {
			w = a
		}
		if b, e2 := strconv.Atoi(strings.TrimSpace(parts[1])); e2 == nil {
			h = b
		}
	}
	sess, err := browser.New(browser.Config{Headless: *headless, UserDataDir: userDataDir, Proxy: *proxy, UserAgent: *userAgent, ViewportW: w, ViewportH: h, Stealth: !*noStealth, OpTimeout: *opTimeout}) // 0 = long-lived session
	if err != nil {
		log.Fatalf("new session: %v", err)
	}
	defer sess.Close()
	if sess.PersistFallback() {
		log.Printf("agent-browser: the persistent profile (%s) was locked or corrupted (likely a leftover Chrome from a prior run), so this session is using a throwaway temp profile. Logins/cookies will NOT survive a restart until the profile is freed. Kill any leftover agent-browser Chrome processes (Task Manager -> chrome.exe owned by agent-browser) to restore persistence.", userDataDir)
	}
	sess.AllowInsecureSchemes = *allowInsecure
	sess.AllowEval = !*noEval

	opts := &mcp.ServerOptions{
		Instructions: "agent-browser v2: token-efficient browser automation built for the agent. ORIENT: navigate/see level=brief -> a page brief (type, auth, primary actions with refs, regions); level=summary -> the ref list. ACT BY INTENT: act \"Sign in\" or act \"Username\" value=x resolves a control by name (local heuristics, no LLM) and clicks/fills it in one call; several matches -> it returns candidates (disambiguate with nth/role). Every action returns a VERDICT (navigated to / dialog opened / status / changed / no visible effect / CHALLENGE) + a DELTA (what changed, fresh refs) so you rarely re-see; non-nav actions also fold in the XHRs that fired (net:). EXTRACT: extract table/links/list/form/article -> JSON (form gives {ref,role,name,value} to feed act/fill). RECALL: history -> the session action log, offloaded from your context. WAIT: wait url=/text=/gone=. FILL: fill fields={ref:value} for a whole form. By-ref tools (click/fill/select/hover/press_key) work when you have a ref. press_key takes ONE named key or ONE char (Enter, Escape, a) - to type text use fill/act, not press_key. Refs are per-tab, reset on navigation. QoL: navigate action=back/forward/reload; scroll ref=r12 to bring an off-screen element into view; read on a link ref also returns its href; screenshot fullPage=true or ref=r12; where for a 30-token re-orientation. RECOVER: reset (optional url) drops a wedged tab + opens a fresh one when a tool times out or a page is unresponsive; other tabs are kept. Every browser op is bounded by an op timeout (default 30s) so a hung page returns an error instead of wedging the whole session. eval covers the rest.",
	}
	srv, err := mcpserver.New(sess, opts)
	if err != nil {
		log.Fatalf("mcp server: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := mcpserver.Run(ctx, srv); err != nil && ctx.Err() == nil {
		log.Fatalf("mcp run: %v", err)
	}
}

// runDebug is the engine-proving CLI.
func runDebug() {
	urlFlag := flag.String("url", "https://example.com", "url to navigate to (http/https only by default)")
	headless := flag.Bool("headless", true, "headless mode")
	level := flag.String("level", string(snapshot.LevelMinimal), "snapshot level: minimal|summary|full")
	findRole := flag.String("find-role", "", "if set, run find mode: filter elements by role")
	findText := flag.String("find-text", "", "filter elements by name (substring unless --find-exact)")
	findExact := flag.Bool("find-exact", false, "match name exactly (case-insensitive) instead of substring")
	clickRef := flag.String("click", "", "if set, click the given ref (e.g. r2) then show the delta")
	allowInsecure := flag.Bool("allow-insecure-schemes", false, "allow file/javascript/data/about/blob URLs")
	timeout := flag.Duration("timeout", 40*time.Second, "overall timeout")
	versionFlag := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *versionFlag {
		fmt.Println("agent-browser", versionString())
		return
	}

	sess, err := browser.New(browser.Config{Headless: *headless, Timeout: *timeout})
	if err != nil {
		log.Fatalf("new session: %v", err)
	}
	defer sess.Close()
	sess.AllowInsecureSchemes = *allowInsecure

	if err := sess.Navigate(*urlFlag); err != nil {
		log.Fatalf("navigate: %v", err)
	}
	if err := sess.BuildTree(); err != nil {
		log.Fatalf("build tree: %v", err)
	}

	switch {
	case *clickRef != "":
		delta, after, err := sess.ClickAndSee(*clickRef, 900*time.Millisecond)
		if err != nil {
			log.Fatalf("click: %v", err)
		}
		var out string
		if delta.Verdict != "" {
			out = "verdict: " + delta.Verdict + "\n"
		}
		if delta.Navigated {
			out += after.Render(snapshot.LevelMinimal)
		} else {
			out += delta.Render()
		}
		fmt.Print(ensureNL(out))
		fmt.Fprintf(os.Stderr, "--- click %s: verdict=%q %s | chars=%d ~tokens=%d ---\n",
			*clickRef, delta.Verdict, delta.Summary(), len(out), len(out)/4)

	case *findRole != "" || *findText != "":
		var els []snapshot.Element
		if *findExact {
			els = sess.Tree().FindExact(*findRole, *findText)
		} else {
			els = sess.Tree().Find(*findRole, *findText)
		}
		out := snapshot.RenderElements(els)
		fmt.Print(ensureNL(out))
		fmt.Fprintf(os.Stderr, "--- find(role=%q text=%q exact=%v): %d matches, chars=%d ~tokens=%d ---\n",
			*findRole, *findText, *findExact, len(els), len(out), len(out)/4)

	default:
		if snapshot.Level(*level) == snapshot.LevelFull {
			if err := sess.FillText(); err != nil {
				log.Fatalf("see full: %v", err)
			}
		}
		out := sess.Tree().Render(snapshot.Level(*level))
		fmt.Print(ensureNL(out))
		fmt.Fprintf(os.Stderr, "--- level=%s: axNodes=%d elems=%d chars=%d ~tokens=%d ---\n",
			*level, len(sess.Tree().Nodes), len(sess.Tree().Elems), len(out), len(out)/4)
	}
}

func ensureNL(s string) string {
	if !strings.HasSuffix(s, "\n") {
		return s + "\n"
	}
	return s
}
