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

	// per-tab listener state.
	dialogListening map[*tab]bool
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
}

// New launches Chrome and returns a Session with one initial tab.
func New(cfg Config) (*Session, error) {
	opts := append(chromedp.DefaultExecAllocatorOptions[:],
		chromedp.NoFirstRun,
		chromedp.NoDefaultBrowserCheck,
	)
	if cfg.Stealth {
		// Drop --enable-automation (removes the "controlled by automated software"
		// banner + the automation blink signal) and disable the
		// AutomationControlled blink feature (sets navigator.webdriver=false at
		// the engine level - more robust than a JS override). Table-stakes stealth.
		opts = append(opts,
			chromedp.Flag("enable-automation", false),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
		)
	}
	// Modern "new" headless has a near-real fingerprint; headed uses the real
	// GPU (best render/timing fingerprint) - prefer --headless=false for hard
	// targets when a display is available.
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

	s := &Session{
		allocCancel:     allocCancel,
		browserCtx:      browserCtx,
		browserCancel:   browserCancel,
		stealth:         cfg.Stealth,
		dialogListening: map[*tab]bool{},
		counter:         1,
	}

	firstCtx := browserCtx
	firstCancel := browserCancel
	if cfg.Timeout > 0 {
		c, cancel := context.WithTimeout(browserCtx, cfg.Timeout)
		firstCtx = c
		firstCancel = func() { cancel(); browserCancel() }
	}
	s.tabs = []*tab{{id: "t1", ctx: firstCtx, cancel: firstCancel}}
	s.setupTabListenersLocked(s.tabs[0])
	return s, nil
}

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
	return chromedp.Run(t.ctx,
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
		err := chromedp.Run(t.ctx,
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
	_ = chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
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
	if err := chromedp.Run(t.ctx, chromedp.Evaluate(readBodyJS, &body)); err != nil {
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
// feeds the verdict's "net:" signal. Idempotent per tab. Caller must hold s.mu.
//
// The network listener is read-only (it observes ResponseReceived events) - it
// does NOT pause requests (the Fetch-domain pausing that deadlocked the v1
// intercept feature). It only appends to a per-tab ring under the tab's own
// netMu, so the listener goroutine never re-enters chromedp.Run and cannot
// deadlock the action path.
func (s *Session) setupTabListenersLocked(t *tab) {
	if t == nil || s.dialogListening[t] {
		return
	}
	s.dialogListening[t] = true
	chromedp.ListenTarget(t.ctx, func(ev any) {
		switch e := ev.(type) {
		case *page.EventJavascriptDialogOpening:
			go func() {
				_ = chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
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
	// Enable the Network domain so ResponseReceived events fire. Best-effort:
	// a failure here just means no net: signal, not a broken session.
	_ = chromedp.Run(t.ctx, network.Enable())
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
	_ = chromedp.Run(t.ctx, chromedp.Evaluate(js, nil))
	if err := chromedp.Run(t.ctx, chromedp.WaitReady("body", chromedp.ByQuery)); err != nil {
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
