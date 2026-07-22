// Command goshawk is the entry point.
//
// Debug CLI (proving the engine):
//
//	go run ./cmd/goshawk --url https://example.com                  # minimal orientation
//	go run ./cmd/goshawk --url https://news.ycombinator.com --level summary
//	go run ./cmd/goshawk --url https://news.ycombinator.com --find-role link --find-text More --find-exact
//	go run ./cmd/goshawk --url https://example.com --click r2        # click ref, show the delta
//
// MCP server (the actual tool, for Cursor/Copilot/Claude Code):
//
//	goshawk mcp                          # eval on by default; add --no-eval to disable
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

	"github.com/dondai1234/goshawk/v3/internal/browser"
	"github.com/dondai1234/goshawk/v3/internal/mcpserver"
	"github.com/dondai1234/goshawk/v3/internal/snapshot"
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
		fmt.Println("goshawk", versionString())
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
	userDataDirFlag := fs.String("user-data-dir", "", "persistent Chrome profile dir (overrides the default location); by default a profile is kept at <os config dir>/goshawk so logins/cookies survive restarts")
	noPersist := fs.Bool("no-persist", false, "use a throwaway temp profile (no saved logins); by default a persistent profile is kept so the agent doesn't re-login every run")
	proxy := fs.String("proxy-server", "", "proxy URL (e.g. http://user:pass@host:port); a residential proxy is the #1 fix for IP-reputation bot blocks")
	userAgent := fs.String("user-agent", "", "override the User-Agent; empty = Chrome default")
	viewport := fs.String("viewport", "1366,768", "window size W,H")
	noStealth := fs.Bool("no-stealth", false, "disable anti-detection (stealth flags + init script + jittered mouse); on by default")
	noCookieDismiss := fs.Bool("no-cookie-dismiss", false, "disable the cookie/consent banner auto-dismiss on navigate (on by default; frees the AX tree + clicks on real sites)")
	allowInsecure := fs.Bool("allow-insecure-schemes", false, "allow file/javascript/data/about/blob URLs")
	noEval := fs.Bool("no-eval", false, "disable the eval tool (arbitrary page JS; enabled by default)")
	opTimeout := fs.Duration("op-timeout", 30*time.Second, "per-CDP-operation timeout: bounds any single browser call so a hung page returns an error instead of wedging the session (the 'all tools timed out' failure). Raise for very slow pages.")
	idleTimeout := fs.Duration("idle-timeout", 10*time.Minute, "auto-close Chrome after this long with no browser activity, so a one-shot use doesn't leave Chrome running for the whole session; the next navigate re-launches it (page state is lost - re-navigate). 0 disables auto-close.")
	versionFlag := fs.Bool("version", false, "print version and exit")
	_ = fs.Parse(args)
	if *versionFlag {
		fmt.Println("goshawk", versionString())
		return
	}

	// Resolve the profile dir: an explicit --user-data-dir wins; otherwise, by
	// default, persist to <os config dir>/goshawk so logins/cookies/local
	// storage survive server restarts (the agent doesn't re-login each run).
	// --no-persist falls back to a throwaway temp profile (the old default).
	// Note: one persistent profile can only be used by one goshawk process
	// at a time (Chrome locks it); run concurrent clients with separate
	// --user-data-dir paths.
	userDataDir := *userDataDirFlag
	if userDataDir == "" && !*noPersist {
		if d, err := os.UserConfigDir(); err == nil {
			userDataDir = filepath.Join(d, "goshawk")
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
	sess, err := browser.New(browser.Config{Headless: *headless, UserDataDir: userDataDir, Proxy: *proxy, UserAgent: *userAgent, ViewportW: w, ViewportH: h, Stealth: !*noStealth, OpTimeout: *opTimeout, IdleTimeout: *idleTimeout, NoOverlayDismiss: *noCookieDismiss}) // 0 = long-lived session
	if err != nil {
		log.Fatalf("new session: %v", err)
	}
	defer sess.Close()
	if sess.PersistFallback() {
		log.Printf("goshawk: the persistent profile (%s) was locked or corrupted (likely a leftover Chrome from a prior run), so this session is using a throwaway temp profile. Logins/cookies will NOT survive a restart until the profile is freed. Kill any leftover goshawk Chrome processes (Task Manager -> chrome.exe owned by goshawk) to restore persistence.", userDataDir)
	}
	sess.AllowInsecureSchemes = *allowInsecure
	sess.AllowEval = !*noEval

	opts := &mcp.ServerOptions{
		Instructions: "goshawk v4: 9 tools for agent browser automation. Dense a11y-tree snapshots (ref-lines, not aria dumps), intent-first actions, a confidence-scored verdict on every action, batch form filling, named profiles, and a JS helper API for structured data. No per-call LLM. DECISION TREE: (1) Enter a page -> nav url= (returns an orientation: page type, auth, the top primary actions WITH refs, regions, counts - act from here). nav action=back|forward|reload; nav newTab=true url= opens a new tab. Cookie/consent banners are auto-dismissed on nav. (2) Look around -> see level=brief (re-orient, ~50 tok) | refs (interactive list with refs) | text (visible body) | outline (semantic skeleton with WORKING css selectors for js) | full (refs+text) | shot (screenshot). (3) Do something -> act. Name a control (intent=\"Sign in\") OR give a ref/selector. act clicks buttons/links, fills inputs (value=), selects dropdowns (value=), hovers (hover=true), presses keys (key=Enter), uploads (files=[..]). For BATCH FORM FILL: act fields={\"Username\":\"john\",\"Password\":\"hunter2\",\"Remember me\":\"true\",\"Country\":\"US\"} fills a whole form in ONE call - each label is resolved to a field, the type is auto-detected (text/checkbox/radio/select/slider/file), and the right action is performed; then re-snapshots once + reports validation errors. Optional waitUrl=/waitText=/waitGone= fuses a wait into the action. Returns a [confidence] VERDICT + DELTA - confidence is confirmed/likely/uncertain based on DOM changes + XHR responses. You usually do NOT re-see after - the verdict tells you what happened. For LOG IN: login username= password= url= does the whole form dance in ONE call - handles single-step AND multi-step, reports a STATE-VERIFIED verdict (logged in | 2FA needed | CHALLENGE | error | SSO redirect | still on login page), detects remember-me checkboxes + forgot-password links + SSO redirects, never auto-clicks OAuth (use act for SSO). (4) Get data -> js. Run JS with helpers, return JSON: return {stars: text('#stars'), items: $$('li').map(text)}. Helpers: $(sel), $$(sel), text(x), attr(x,name), table(sel)->rows, links(sel), frame(title), wait(fn,ms). await=sel waits first. One call, clean JSON. (5) Find a control -> find role=/text= -> refs; find selector=\"css\" -> [css] lines. (6) Tabs -> tabs action=list|switch|close id= label=. (7) Lost your place -> history (last=N, errors=true) or see brief. (8) Stuck -> session mode=reset (relaunch) or mode=clear (wipe cookies+storage+reload). (9) Switch identity -> session mode=profile action=create|switch|list|delete|current|export|import - named browser profiles with isolated cookies/auth/storage; switch in one call. RULES: refs are STABLE across re-renders (auto-healed if the element is re-created with the same name/role); refs are per-tab, cleared on navigation. Verdicts include a confidence tag: [confirmed] = DOM changed or XHR 2xx fired; [likely] = content shifted or XHR fired; [uncertain] = no visible effect (call see to verify). DO NOT: re-see after act (the verdict is your result); use press_key for text (use fill/act value=); call act without a target or mode; use session reset for a simple page refresh (use nav reload). Every op is bounded by an op timeout (default 30s). js disabled if started with --no-eval. For hard anti-bot: --headless=false + --proxy-server.",
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
		fmt.Println("goshawk", versionString())
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
