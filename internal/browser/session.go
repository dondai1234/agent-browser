// Package browser holds the persistent browser session: a long-lived chromedp
// browser with one or more tabs. The current tab holds the cached accessibility
// tree so find/filter is free (no new CDP round-trip). A mutex serializes all
// operations; each public method is atomic.
package browser

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/fnv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/dondai1234/agent-browser/v2/internal/snapshot"
)

// ErrNoSnapshot is returned when the current tab has no cached page tree.
var ErrNoSnapshot = errors.New("no page snapshot yet on the current tab; call navigate first")

// tab is one browser tab: its own chromedp context and cached tree.
type tab struct {
	id     string // "t1", "t2", ...
	label  string
	ctx    context.Context
	cancel context.CancelFunc
	tree   *snapshot.Tree

	netMu     sync.Mutex
	netEvents []netEvt // ring of recent XHR/Fetch responses, for the verdict's "did it hit the API" signal
}

// netEvt is one captured network response, kept only for XHR/Fetch (the API
// calls an action triggers), not static assets. Used by the verdict to report
// "net: /api/cart 200" so the agent knows a click reached the backend even
// when the DOM change is subtle or role-less.
type netEvt struct {
	url    string
	status int64
	ts     time.Time
}

// Session is a persistent Chrome browser with a current tab and optional
// additional tabs.
type Session struct {
	allocCancel   context.CancelFunc
	browserCtx    context.Context
	browserCancel context.CancelFunc

	mu      sync.Mutex
	tabs    []*tab
	cur     int
	counter int

	// history is the rolling action log (offloaded from the agent's context so
	// long tasks don't bloat it). Appended under s.mu by every action + navigate;
	// capped to the last maxHistory entries. histStep keeps incrementing across
	// trims so step numbers stay monotonic + unique.
	history  []historyEntry
	histStep int

	// AllowInsecureSchemes opts in to file/javascript/data/about/blob URLs.
	AllowInsecureSchemes bool
	// AllowEval controls the eval tool (arbitrary page JS). On by default;
	// the operator can disable it with the --no-eval flag.
	AllowEval bool

	// stealth holds the cfg.Stealth choice (table-stakes anti-detection flags +
	// init script + jittered mouse paths). Default true.
	stealth bool

	// opTimeout bounds a single CDP operation (see run/runTimeout). Default
	// opTimeoutDefault; the #1 reliability fix: without it a hung page wedges
	// the session mutex and every tool blocks on the lock until the MCP client
	// times out (the "session hung, all tools timed out" failure mode).
	opTimeout time.Duration

	// dead is non-nil after a fatal browser error (chrome failed to start,
	// the browser process crashed, the websocket dropped). Once set, run/runTimeout
	// short-circuit: they return this error WITHOUT calling chromedp.Run, so a
	// dead session is never retried. This is what prevents the chromedp panic:\t// Allocate double-closes c.allocated when a second Run retries after the
	// first failed to start Chrome. Cleared by Reset (which relaunches the browser).
	dead error

	// cfg is the launch config, kept so Reset can relaunch the browser with the
	// same flags (stealth/headless/profile/viewport/...).
	cfg Config

	// persistFallback is true when New fell back to a throwaway temp profile
	// because the persistent profile was locked/corrupted (so logins won't survive
	// a restart until the profile is freed/recreated).
	persistFallback bool

	// per-tab listener state.
	dialogListening map[*tab]bool
}

// opTimeoutDefault bounds a single CDP operation (chromedp.Run). A hung page
// (renderer crash, mid-navigation execution-context teardown, a challenge that
// never resolves) can make a CDP call never return; without a per-op bound that
// call holds the session mutex forever and EVERY tool then blocks on the lock
// until the MCP client times out - the "session hung, all tools timed out"
// failure. The bound turns a wedge into a normal error the agent can reset from.
const opTimeoutDefault = 30 * time.Second

// axPollTimeout bounds each accessibility-tree pull (GetFullAXTree + the iframe
// merge). AX pulls are fast on a live page; a pull that takes longer is almost
// always a wedging page, so fail it well under the op timeout (the build-tree
// retry then gets a second attempt within the overall op budget).
const axPollTimeout = 8 * time.Second

// launchTimeout bounds the Chrome launch (the first CDP op on a fresh tab -
// network.Enable in setupTabListeners - which is what actually starts Chrome).
// It's separate from + longer than the op timeout so a slow Chrome cold-start
// (antivirus scan, first-run profile setup, a heavy persistent profile) doesn't
// fail New under a tight --op-timeout. The launch is one-time; regular ops use
// op-timeout.
const launchTimeout = 60 * time.Second

// run executes CDP actions on the tab bounded by the per-operation timeout.
// The timeout is enforced with a goroutine + select, NOT context.WithTimeout:
// chromedp.Navigate registers a navigation listener tied to the chromedp
// context, and a derived timeout context makes it return "context canceled"
// (a real chromedp quirk that broke every navigate). So we run on the real tab
// ctx (navigate works as before) and abandon the op if it exceeds the budget -
// the caller (a public method holding s.mu) returns a timeout error + releases
// the mutex, so the session can't wedge. A genuinely wedged op leaks a goroutine
// until the tab is reset/closed (reset cancels t.ctx, which unblocks it). The
// caller already holds s.mu; run itself does not take the lock.
func (s *Session) run(t *tab, acts ...chromedp.Action) error {
	return s.runTimeout(t, s.opTimeout, acts...)
}

// runTimeout is run with an explicit timeout (used by the AX pulls, which want
// a tighter bound than the op timeout so the build-tree retry fits in budget).
func (s *Session) runTimeout(t *tab, d time.Duration, acts ...chromedp.Action) error {
	// Never retry a dead session: a second chromedp.Run after the browser died
	// re-enters Allocate, whose cmd.Wait goroutine double-closes c.allocated and
	// panics (crashing the whole MCP process). Short-circuit instead, so the
	// agent sees "use reset" instead of a crashed server.
	if s.dead != nil {
		return fmt.Errorf("browser session is dead (%v); use reset to relaunch it (or restart the MCP server)", s.dead)
	}
	if d <= 0 {
		err := chromedp.Run(t.ctx, acts...)
		if isFatalBrowserErr(err) {
			s.dead = err
		}
		return err
	}
	type result struct{ err error }
	done := make(chan result, 1)
	go func() {
		// Recover from an unlikely panic so the select never waits a full
		// timeout for a dead goroutine; a panic here would be a chromedp bug.
		defer func() {
			if r := recover(); r != nil {
				select {
				case done <- result{fmt.Errorf("cdp op panicked: %v", r)}:
				default:
				}
			}
		}()
		done <- result{chromedp.Run(t.ctx, acts...)}
	}()
	select {
	case r := <-done:
		if isFatalBrowserErr(r.err) {
			s.dead = r.err // mark dead so the next op short-circuits (no retry -> no panic)
		}
		return r.err
	case <-time.After(d):
		return fmt.Errorf("operation timed out after %s (the page may be wedged; use reset to recover, or raise --op-timeout for slow pages)", d)
	}
}

// isFatalBrowserErr reports whether an error means the browser itself is gone
// (not just a bad page or a timeout). Used to mark the session dead so a later
// op doesn't retry chromedp.Run (which double-closes c.allocated and panics).
// The op-timeout message is intentionally NOT fatal: a wedged page isn't a dead
// browser, and the next op may still succeed (or the agent resets).
func isFatalBrowserErr(err error) bool {
	if err == nil {
		return false
	}
	m := err.Error()
	return strings.Contains(m, "chrome failed to start") ||
		strings.Contains(m, "context canceled") ||
		strings.Contains(m, "connection refused") ||
		strings.Contains(m, "websocket is not connected") ||
		strings.Contains(m, "not connected to browser") ||
		strings.Contains(m, "target closed") ||
		strings.Contains(m, "was not reached")
}

// historyEntry is one row of the session action log: what was done, the
// verdict it produced, and the page URL at the time. Queried via the history
// tool so the agent can re-orient after a long flow without re-snapshotting.
type historyEntry struct {
	Step    int
	Time    time.Time
	Action  string
	Verdict string
	URL     string
}

// maxHistory bounds the in-memory action log (and thus the history tool's worst
// case). 200 steps covers very long flows; older entries drop off the front.
const maxHistory = 200

// Config configures a Session.
type Config struct {
	Headless    bool
	Timeout     time.Duration // >0 bounds the first tab (debug CLI); 0 = long-lived (MCP server)
	UserDataDir string        // persistent profile dir; "" = a throwaway temp profile (the MCP server defaults this to <os config dir>/agent-browser for persistence unless --no-persist)
	Proxy       string        // proxy server URL (e.g. http://user:pass@host:port); "" = none
	UserAgent   string        // override the User-Agent; "" = Chrome default
	ViewportW   int           // window width; 0 = 1366
	ViewportH   int           // window height; 0 = 768
	Stealth     bool          // apply anti-detection flags + init script + jittered mouse (default true)
	OpTimeout   time.Duration // per-CDP-operation timeout (default 30s); bounds any single chromedp.Run so a hung page can't wedge the session mutex + deadlock every tool. Raise for very slow pages.
}

// New launches Chrome and returns a Session with one initial tab. If Chrome
// can't start with the requested (persistent) profile - the profile is locked
// by an orphaned Chrome from a prior run, or corrupted - it falls back to a
// throwaway temp profile so the server still works (no persistence, but alive).
// Without this fallback, a locked profile makes every tool fail with "chrome
// failed to start" and the server is useless until the orphan is killed.
func New(cfg Config) (*Session, error) {
	s := &Session{cfg: cfg, stealth: cfg.Stealth, opTimeout: cfg.OpTimeout, dialogListening: map[*tab]bool{}}
	if s.opTimeout <= 0 {
		s.opTimeout = opTimeoutDefault
	}
	if err := s.launchBrowserLocked(); err != nil {
		if cfg.UserDataDir != "" {
			// The requested (persistent) profile wouldn't start Chrome - locked by
			// an orphaned Chrome from a prior run, corrupted, or otherwise unusable
			// (any launch error: "chrome failed to start", "websocket url timeout
			// reached", ...). Fall back to a throwaway temp profile so the server
			// still works (no persistence, but alive). Without this, a locked profile
			// makes every tool fail + the chromedp double-close panic crashes the
			// server once a later op retries Allocate.
			s.teardownBrowserLocked()
			s.cfg.UserDataDir = ""
			s.dead = nil
			if err2 := s.launchBrowserLocked(); err2 != nil {
				return nil, fmt.Errorf("chrome failed to start (persistent profile: %v; temp profile fallback also failed: %w)", err, err2)
			}
			s.persistFallback = true
		} else {
			return nil, fmt.Errorf("launch browser: %w", err)
		}
	}
	return s, nil
}

// launchBrowserLocked builds the Chrome allocator + browser session + first tab
// and runs the first CDP op (network.Enable in setupTabListeners), which is what
// actually launches Chrome. Returns an error if Chrome fails to start. Called by
// New and by Reset (after teardownBrowserLocked). Caller must hold s.mu.
func (s *Session) launchBrowserLocked() error {
	cfg := s.cfg
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
	)
	if cfg.Stealth {
		opts = append(opts,
			chromedp.Flag("enable-automation", false),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
		)
	}
	if cfg.Headless {
		opts = append(opts, chromedp.Flag("headless", "new"))
	} else {
		opts = append(opts, chromedp.Flag("headless", false))
	}
	if cfg.UserDataDir != "" {
		opts = append(opts, chromedp.UserDataDir(cfg.UserDataDir))
	}
	if cfg.Proxy != "" {
		opts = append(opts, chromedp.ProxyServer(cfg.Proxy))
	}
	if cfg.UserAgent != "" {
		opts = append(opts, chromedp.Flag("user-agent", cfg.UserAgent))
	}
	w, h := cfg.ViewportW, cfg.ViewportH
	if w == 0 {
		w = 1366
	}
	if h == 0 {
		h = 768
	}
	opts = append(opts, chromedp.WindowSize(w, h))
	allocCtx, allocCancel := chromedp.NewExecAllocator(context.Background(), opts...)
	browserCtx, browserCancel := chromedp.NewContext(allocCtx)
	s.allocCancel = allocCancel
	s.browserCtx = browserCtx
	s.browserCancel = browserCancel

	// The first tab gets its own chromedp target (a child of the browser session),
	// so cancelling it closes ONLY that tab - not the browser. (Reusing browserCtx
	// as the first tab's ctx made t1.cancel == browserCancel, so close/reset of t1
	// killed the whole browser.) Its first CDP op (network.Enable in
	// setupTabListeners) is what actually launches Chrome. NewTab later derives
	// new tabs from an existing tab's ctx (which carries the allocated Browser),
	// NOT from browserCtx - browserCtx's Browser stays nil because the launch runs
	// on the tab, so NewContext(browserCtx) would wrongly launch a second Chrome.
	// An optional Timeout wraps the tab root (debug CLI); the MCP server leaves it
	// long-lived.
	tabRootCtx := browserCtx
	if cfg.Timeout > 0 {
		var tcancel context.CancelFunc
		tabRootCtx, tcancel = context.WithTimeout(browserCtx, cfg.Timeout)
		_ = tcancel // fires on timeout; browserCancel in Close also cancels it
	}
	firstCtx, firstCancel := chromedp.NewContext(tabRootCtx)
	s.counter = 1
	s.tabs = []*tab{{id: "t1", ctx: firstCtx, cancel: firstCancel}}
	s.cur = 0
	s.dialogListening = map[*tab]bool{}
	return s.setupTabListenersLocked(s.tabs[0])
}

// teardownBrowserLocked cancels the allocator + browser session + all tabs, so a
// failed launch (or a Reset) releases the Chrome process + chromedp goroutines
// before a fresh launch. Caller must hold s.mu.
func (s *Session) teardownBrowserLocked() {
	for _, t := range s.tabs {
		if t.cancel != nil {
			t.cancel()
		}
	}
	s.tabs = nil
	if s.browserCancel != nil {
		s.browserCancel()
	}
	if s.allocCancel != nil {
		s.allocCancel()
	}
}

// PersistFallback reports whether New fell back to a throwaway temp profile
// because the requested persistent profile was locked/corrupted. The operator
// can log it so the user knows persistence is off until the profile is freed.
func (s *Session) PersistFallback() bool { return s.persistFallback }

// Close shuts down the browser.
func (s *Session) Close() {
	s.mu.Lock()
	for _, t := range s.tabs {
		if t.cancel != nil {
			t.cancel()
		}
	}
	s.mu.Unlock()
	if s.browserCancel != nil {
		s.browserCancel()
	}
	if s.allocCancel != nil {
		s.allocCancel()
	}
}

// curTabLocked returns the current tab. Caller must hold s.mu.
func (s *Session) curTabLocked() *tab {
	if len(s.tabs) == 0 {
		return nil
	}
	if s.cur >= len(s.tabs) {
		s.cur = len(s.tabs) - 1
	}
	return s.tabs[s.cur]
}

// Navigate opens a URL on the current tab and waits for the body. Invalidates
// the cached tree.
func (s *Session) Navigate(raw string) error {
	clean, err := ValidateURL(raw, s.AllowInsecureSchemes)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return errors.New("no tab")
	}
	t.tree = nil
	return s.run(t,
		chromedp.Navigate(clean),
		chromedp.WaitReady("body", chromedp.ByQuery),
	)
}

// BuildTree pulls the accessibility tree for the current tab and caches it.
func (s *Session) BuildTree() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buildTreeLocked()
}

// buildTreeLocked rebuilds the current tab's tree. Caller must hold s.mu.
// Crash-recovery: if the first AX pull fails, wait briefly and retry once
// (handles renderer crashes / mid-load states).
func (s *Session) buildTreeLocked() error {
	t := s.curTabLocked()
	if t == nil {
		return errors.New("no tab")
	}
	err := s.pullAXLocked(t)
	if err == nil {
		return nil
	}
	// Retry once after a settle - a crashed/still-loading tab may recover.
	time.Sleep(400 * time.Millisecond)
	retry := s.pullAXLocked(t)
	if retry != nil {
		return fmt.Errorf("build tree (page may have crashed or be unreachable): %w", err)
	}
	return nil
}

// axSig returns a stable hash of the AX tree's shape + content (node count +
// each node's role/name/value as raw JSON bytes), so the stable-poll detects
// both structural and content changes - not just node count. (A button label
// changing "Add to cart" -> "Remove" keeps the count but changes the signature.)
func axSig(nodes []*accessibility.Node) uint64 {
	h := fnv.New64a()
	var buf [8]byte
	binary.LittleEndian.PutUint64(buf[:], uint64(len(nodes)))
	h.Write(buf[:])
	for _, n := range nodes {
		if n == nil {
			h.Write([]byte{0})
			continue
		}
		if n.Role != nil {
			h.Write([]byte(n.Role.Value))
		}
		h.Write([]byte{0})
		if n.Name != nil {
			h.Write([]byte(n.Name.Value))
		}
		h.Write([]byte{0})
		if n.Value != nil {
			h.Write([]byte(n.Value.Value))
		}
		h.Write([]byte{1})
	}
	return h.Sum64()
}

func (s *Session) pullAXLocked(t *tab) error {
	// Poll until the AX-tree SIGNATURE stabilizes across two consecutive pulls.
	// The signature (count + every node's role/name/value) catches BOTH
	// structural changes (count) AND content changes (e.g. "Add to cart" ->
	// "Remove": same count, different content) - count-only polling misses the
	// latter. Returns as soon as the tree actually settles, so actions are fast
	// on quick pages and only wait on genuinely slow ones. Capped at 1s.
	var (
		nodes   []*accessibility.Node
		title   string
		loc     string
		lastSig uint64
		haveSig bool
	)
	deadline := time.Now().Add(1000 * time.Millisecond)
	for {
		var (
			n  []*accessibility.Node
			ti string
			lo string
		)
		err := s.runTimeout(t, axPollTimeout,
			chromedp.Title(&ti),
			chromedp.Location(&lo),
			chromedp.ActionFunc(func(ctx context.Context) error {
				ns, e := accessibility.GetFullAXTree().Do(ctx)
				if e != nil {
					return e
				}
				n = ns
				return nil
			}),
		)
		if err != nil {
			return err
		}
		nodes, title, loc = n, ti, lo
		sig := axSig(n)
		if haveSig && sig == lastSig {
			break // stable across two pulls
		}
		lastSig = sig
		haveSig = true
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(60 * time.Millisecond)
	}
	// getFullAXTree does not traverse into iframes. Gather same-origin iframe
	// AX trees separately (via GetPartialAXTree on each iframe's content
	// document) and merge, so in-iframe elements get refs and are
	// clickable/fillable across the frame boundary. Cross-origin iframes have
	// no ContentDocument and are skipped (opaque to any tool).
	extra, frameOf := s.gatherIframeAXLocked(t)
	all := nodes
	if len(extra) > 0 {
		all = append(append([]*accessibility.Node(nil), nodes...), extra...)
	}
	tree := snapshot.BuildTree(all)
	tree.URL = loc
	tree.Title = title
	// Detect a bot-check interstitial on every snapshot (cheap: just title/url
	// strings), not only on navigate. A click that lands on a Cloudflare wall
	// then surfaces CHALLENGE: in its verdict, so the agent knows the action
	// was blocked instead of seeing an opaque tree. DOM-based captcha probing
	// stays navigate-only (it's a CDP evaluate; kept off the hot action path).
	tree.Challenge = detectChallengeTitleURL(loc, title)
	tree.SetFrames(frameOf)
	t.tree = tree
	return nil
}

// gatherIframeAXLocked pulls the AX trees of same-origin iframes on the current
// tab (recursively, for nested iframes) and returns their nodes plus a map of
// backendNodeID -> iframe title for ref-line annotation. Caller must hold s.mu.
// getFullAXTree returns only the root frame; per-frame GetFullAXTree(frameId)
// forces a full build of each same-origin iframe's AX tree and we merge it in.
// Cross-origin iframes (no contentDocument) are skipped - opaque to any tool.
func (s *Session) gatherIframeAXLocked(t *tab) (extra []*accessibility.Node, frameOf map[int64]string) {
	frameOf = map[int64]string{}
	_ = s.runTimeout(t, axPollTimeout, chromedp.ActionFunc(func(ctx context.Context) error {
		root, err := dom.GetDocument().WithDepth(0).Do(ctx)
		if err != nil || root == nil {
			return nil
		}
		s.gatherIframeAXFromLocked(ctx, root.NodeID, "", &extra, frameOf)
		return nil
	}))
	return
}

func (s *Session) gatherIframeAXFromLocked(ctx context.Context, docNodeID cdp.NodeID, parentTitle string, extra *[]*accessibility.Node, frameOf map[int64]string) {
	iframeIDs, err := dom.QuerySelectorAll(docNodeID, "iframe").Do(ctx)
	if err != nil {
		return
	}
	for _, nid := range iframeIDs {
		// Pierce+depth so the iframe's contentDocument + frameId are populated.
		desc, err := dom.DescribeNode().WithNodeID(nid).WithDepth(1).WithPierce(true).Do(ctx)
		if err != nil || desc == nil || desc.ContentDocument == nil || desc.FrameID == "" {
			continue // cross-origin iframe (no contentDocument) or not loaded
		}
		title := iframeTitle(desc, parentTitle)
		axNodes, err := accessibility.GetFullAXTree().WithFrameID(desc.FrameID).Do(ctx)
		if err != nil {
			continue
		}
		for _, n := range axNodes {
			if n == nil {
				continue
			}
			if n.BackendDOMNodeID != 0 {
				frameOf[int64(n.BackendDOMNodeID)] = title
			}
			*extra = append(*extra, n)
		}
		// Recurse into the iframe's content document for nested iframes.
		s.gatherIframeAXFromLocked(ctx, desc.ContentDocument.NodeID, title, extra, frameOf)
	}
}

// iframeTitle picks a label for an iframe from its title/aria-label/name
// attribute (Attributes is a flat [key, value, ...] slice); falls back to the
// parent title + "> iframe" or just "iframe".
func iframeTitle(n *cdp.Node, parent string) string {
	for i := 0; i+1 < len(n.Attributes); i += 2 {
		if k, v := n.Attributes[i], n.Attributes[i+1]; (k == "title" || k == "aria-label" || k == "name") && v != "" {
			return v
		}
	}
	if parent != "" {
		return parent + " > iframe"
	}
	return "iframe"
}

// Tree returns the current tab's cached page tree (nil if none).
func (s *Session) Tree() *snapshot.Tree {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return nil
	}
	return t.tree
}

// FillText fetches the current tab's visible text (walking same-origin iframes)
// and attaches it to the cached tree so a full-level Render includes it. Use
// before Render(LevelFull). Returns ErrNoSnapshot if there is no cached tree.
func (s *Session) FillText() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return ErrNoSnapshot
	}
	var body string
	if err := s.run(t, chromedp.Evaluate(readBodyJS, &body)); err != nil {
		return fmt.Errorf("see full: %w", err)
	}
	t.tree.Text = truncate(body, 8000)
	return nil
}

// resolveRefLocked resolves a ref to a remote object ID on the current tab.
// Caller must hold s.mu.
func (s *Session) resolveRefLocked(ctx context.Context, ref string) (runtime.RemoteObjectID, error) {
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return "", ErrNoSnapshot
	}
	el, ok := t.tree.ByRef(ref)
	if !ok {
		return "", fmt.Errorf("ref %q not found; refs may be stale after navigation - call see again", ref)
	}
	if el.Backend == 0 {
		return "", fmt.Errorf("ref %q has no backing DOM node (virtual a11y node); cannot act on it", ref)
	}
	return s.resolveBackendLocked(ctx, el.Backend, ref)
}

// setupTabListenersLocked installs per-tab event listeners: JS dialog
// auto-accept AND a read-only network listener (XHR/Fetch responses) that
// feeds the verdict's "net:" signal. Idempotent per tab. Returns the error from
// the network.Enable op (the first CDP call on a fresh tab - this is what
// actually launches Chrome, so its error tells New whether Chrome started).
// Caller must hold s.mu.
//
// The network listener is read-only (it observes ResponseReceived events) - it
// does NOT pause requests (the Fetch-domain pausing that deadlocked the v1
// intercept feature). It only appends to a per-tab ring under the tab's own
// netMu, so the listener goroutine never re-enters chromedp.Run and cannot
// deadlock the action path.
func (s *Session) setupTabListenersLocked(t *tab) error {
	if t == nil || s.dialogListening[t] {
		return nil
	}
	s.dialogListening[t] = true
	chromedp.ListenTarget(t.ctx, func(ev any) {
		switch e := ev.(type) {
		case *page.EventJavascriptDialogOpening:
			// Dismiss the dialog in a goroutine. This does NOT use s.run: the
			// listener goroutine doesn't hold s.mu, so touching s.dead from here
			// would race, and a failed dismiss on a dying tab must NOT mark the
			// whole session dead. WithTimeout is safe here (HandleJavaScriptDialog
			// isn't a Navigate, so the chromedp Navigate-context quirk doesn't
			// apply).
			go func() {
				ctx, cancel := context.WithTimeout(t.ctx, 10*time.Second)
				defer cancel()
				_ = chromedp.Run(ctx, chromedp.ActionFunc(func(ctx context.Context) error {
					return page.HandleJavaScriptDialog(true).Do(ctx)
				}))
			}()
		case *network.EventResponseReceived:
			// Only API calls (XHR/Fetch), never static assets - those would flood
			// the verdict with JS/CSS/image noise on every page load.
			if e.Type != network.ResourceTypeXHR && e.Type != network.ResourceTypeFetch {
				return
			}
			if e.Response == nil {
				return
			}
			t.netMu.Lock()
			t.netEvents = append(t.netEvents, netEvt{url: e.Response.URL, status: e.Response.Status, ts: time.Now()})
			if len(t.netEvents) > 64 {
				t.netEvents = t.netEvents[len(t.netEvents)-64:]
			}
			t.netMu.Unlock()
		}
	})
	// Enable the Network domain so ResponseReceived events fire. This is the
	// first CDP op on the tab - i.e. the moment Chrome actually launches - so its
	// error (e.g. "chrome failed to start" on a locked profile) propagates to New.
	// Use the generous launch timeout, not the per-op timeout, so a slow Chrome
	// cold-start doesn't fail New under a tight --op-timeout.
	return s.runTimeout(t, launchTimeout, network.Enable())
}

// recentNetLocked returns the XHR/Fetch responses received on this tab since
// `since`, for the verdict's net: summary. Caller must hold s.mu (or be fine
// racing the listener - we only read under netMu). The tab's netMu is a
// separate lock from s.mu, so this never deadlocks with the listener goroutine.
func (s *Session) recentNetLocked(t *tab, since time.Time) []netEvt {
	if t == nil {
		return nil
	}
	t.netMu.Lock()
	defer t.netMu.Unlock()
	var out []netEvt
	for _, e := range t.netEvents {
		if !e.ts.Before(since) {
			out = append(out, e)
		}
	}
	return out
}

// summarizeNet renders the action-window XHR/Fetch responses as a compact
// "N requests (last: /path status, ...)" string for the verdict. Shows up to 3
// most recent (the action's own request is usually last); a count covers the
// rest. URLs are shortened to path+query so the line stays dense.
func summarizeNet(evts []netEvt) string {
	n := len(evts)
	var parts []string
	start := 0
	if n > 3 {
		start = n - 3
	}
	for _, e := range evts[start:] {
		parts = append(parts, fmt.Sprintf("%s %d", shortURL(e.url), e.status))
	}
	if n <= 3 {
		return strings.Join(parts, ", ")
	}
	return fmt.Sprintf("%d requests (last: %s)", n, strings.Join(parts, ", "))
}

// shortURL reduces a URL to its path+query (drops scheme+host) and truncates,
// so a net: line stays short: "https://api.site.com/v1/cart?x=1" -> "/v1/cart?x=1".
func shortURL(u string) string {
	if i := strings.Index(u, "://"); i >= 0 {
		u = u[i+3:]
		if s := strings.IndexByte(u, '/'); s >= 0 {
			u = u[s:]
		} else {
			u = "/"
		}
	}
	if len(u) > 60 {
		u = u[:60] + "..."
	}
	return u
}

// recordHistoryLocked appends one action-log entry. Caller must hold s.mu.
// step numbers stay monotonic across trims (histStep keeps counting) so the
// agent can reference "since step N" stably across a long session.
func (s *Session) recordHistoryLocked(action, verdict, url string) {
	s.histStep++
	s.history = append(s.history, historyEntry{
		Step:    s.histStep,
		Time:    time.Now(),
		Action:  action,
		Verdict: verdict,
		URL:     url,
	})
	if len(s.history) > maxHistory {
		s.history = s.history[len(s.history)-maxHistory:]
	}
}

// History returns the action log as compact text for the history tool. last>0
// limits to the most recent N (after any error filter); errorsOnly filters to
// entries where the action was blocked (a CHALLENGE verdict). Step numbers are
// preserved (not renumbered) so the agent can track progress across calls.
func (s *Session) History(last int, errorsOnly bool) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	entries := s.history
	if errorsOnly {
		filt := make([]historyEntry, 0, len(entries))
		for _, e := range entries {
			if strings.Contains(e.Verdict, "CHALLENGE") {
				filt = append(filt, e)
			}
		}
		entries = filt
	}
	shownAll := len(entries)
	if last > 0 && len(entries) > last {
		entries = entries[len(entries)-last:]
	}
	var b strings.Builder
	if len(entries) == 0 {
		b.WriteString("history: (empty - no actions recorded yet)")
		return b.String()
	}
	fmt.Fprintf(&b, "history (%d entries", shownAll)
	if errorsOnly {
		b.WriteString(", errors only")
	}
	if last > 0 && shownAll > last {
		fmt.Fprintf(&b, ", showing last %d", last)
	}
	b.WriteString("):\n")
	for _, e := range entries {
		fmt.Fprintf(&b, "#%d %s %s | %s | %s\n", e.Step, e.Time.Format("15:04:05"), e.Action, e.Verdict, shortURL(e.URL))
	}
	return strings.TrimRight(b.String(), "\n")
}

// NavigateAction performs a browser navigation that isn't a URL open: back,
// forward, or reload. Each rebuilds the tree and returns the new orientation
// (like NavigateAndSee). back/forward with no history to traverse is a no-op
// (returns the current page). Records the action in the session log.
func (s *Session) NavigateAction(action string) (*snapshot.Tree, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	var js string
	switch action {
	case "back":
		js = "window.history.back()"
	case "forward":
		js = "window.history.forward()"
	case "reload":
		js = "location.reload()"
	default:
		return nil, fmt.Errorf("unknown navigate action %q (open|back|forward|reload)", action)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return nil, errors.New("no tab")
	}
	t.tree = nil
	// history.back/forward/reload tear down the execution context as the page
	// navigates, so the Evaluate itself may error - ignore it; WaitReady on the
	// new page is the real signal.
	_ = s.run(t, chromedp.Evaluate(js, nil))
	if err := s.run(t, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
		return nil, fmt.Errorf("navigate %s: %w", action, err)
	}
	if err := s.buildTreeLocked(); err != nil {
		return nil, err
	}
	cur := s.curTabLocked()
	cur.tree.Challenge = detectChallengeTitleURL(cur.tree.URL, cur.tree.Title)
	if cur.tree.Challenge != "" {
		if s.waitForChallengeClearLocked(cur, 8*time.Second) {
			if err := s.buildTreeLocked(); err == nil {
				cur = s.curTabLocked()
				cur.tree.Challenge = detectChallengeTitleURL(cur.tree.URL, cur.tree.Title)
			}
		}
	}
	verdict := fmt.Sprintf("%s -> navigated to %s", action, cur.tree.URL)
	if cur.tree.Title != "" {
		verdict += fmt.Sprintf(" %q", cur.tree.Title)
	}
	if cur.tree.Challenge != "" {
		verdict = action + " -> CHALLENGE: " + cur.tree.Challenge
	}
	s.recordHistoryLocked(action, verdict, cur.tree.URL)
	return cur.tree, nil
}

// Reset is the recovery path: it tears down the whole browser (every tab + the
// Chrome process + the chromedp session) and relaunches a fresh one, navigating
// to url if non-empty. Use it when a tool returned an op-timeout or "browser
// session is dead" error, or a page is an unresponsive SPA. A full relaunch
// (not just a new tab) is what makes reset bulletproof: it recovers from a
// wedged TAB and from a crashed BROWSER (a plain new-tab reset can't, because the
// dead browser session can't accept a new target). The cost is that other tabs
// are lost - acceptable for a recovery scenario (if your browser crashed, those
// tabs were gone anyway). Bounded by the op timeout like every action.
// Returns the new tab's orientation, or an error if Chrome itself can't start
// (restart the MCP server in that case).
func (s *Session) Reset(url string) (*snapshot.Tree, error) {
	if url != "" {
		clean, err := ValidateURL(url, s.AllowInsecureSchemes)
		if err != nil {
			return nil, err
		}
		url = clean
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Tear down the old browser entirely (tabs + session + allocator) so the
	// Chrome process exits + chromedp goroutines stop, then relaunch fresh.
	s.teardownBrowserLocked()
	s.dead = nil
	if err := s.launchBrowserLocked(); err != nil {
		s.dead = err
		s.recordHistoryLocked("reset", "reset failed: "+err.Error(), url)
		return nil, fmt.Errorf("reset: launch browser: %w", err)
	}
	t := s.curTabLocked()
	action := "reset (blank tab)"
	if url != "" {
		action = "reset -> " + url
		if err := s.run(t, chromedp.Navigate(url), chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
			s.recordHistoryLocked("reset", "reset failed: "+err.Error(), url)
			return nil, fmt.Errorf("reset: navigate %s: %w", url, err)
		}
		if err := s.buildTreeLocked(); err != nil {
			s.recordHistoryLocked("reset", "reset failed: "+err.Error(), url)
			return nil, fmt.Errorf("reset: %w", err)
		}
		c := s.curTabLocked()
		if c.tree != nil {
			c.tree.Challenge = detectChallengeTitleURL(c.tree.URL, c.tree.Title)
			if c.tree.Challenge != "" {
				if s.waitForChallengeClearLocked(c, 8*time.Second) {
					if err := s.buildTreeLocked(); err == nil {
						c = s.curTabLocked()
						c.tree.Challenge = detectChallengeTitleURL(c.tree.URL, c.tree.Title)
					}
				}
			}
		}
	}
	s.recordHistoryLocked(action, "reset: browser relaunched", url)
	return s.curTabLocked().tree, nil
}

// Where returns a ~30-token "you are here" re-orientation: current URL, page
// type, auth state, the last action's verdict, and scroll position (more-below
// / at-bottom). For recovering context after a long flow or a compaction
// without a full see + history. scrollInfoLocked does one CDP eval; we hold
// s.mu here, so it's consistent with the scroll action's pattern.
func (s *Session) Where() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return "no page snapshot yet (call navigate first)"
	}
	tr := t.tree
	var b strings.Builder
	fmt.Fprintf(&b, "url: %s\n", tr.URL)
	if tr.Challenge != "" {
		fmt.Fprintf(&b, "CHALLENGE: %s\n", tr.Challenge)
	}
	fmt.Fprintf(&b, "page: %s | auth: %s\n", tr.PageType(), tr.AuthState())
	if len(s.history) > 0 {
		last := s.history[len(s.history)-1]
		fmt.Fprintf(&b, "last: #%d %s | %s\n", last.Step, last.Action, last.Verdict)
	}
	fmt.Fprintf(&b, "scroll: %s\n", s.scrollInfoLocked(t))
	return strings.TrimRight(b.String(), "\n")
}
