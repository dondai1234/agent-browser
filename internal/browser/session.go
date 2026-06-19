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
	"sync"
	"time"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/dom"
	"github.com/chromedp/cdproto/page"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"

	"github.com/dondai1234/agent-browser/internal/snapshot"
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
		// the engine level — more robust than a JS override). Table-stakes stealth.
		opts = append(opts,
			chromedp.Flag("enable-automation", false),
			chromedp.Flag("disable-blink-features", "AutomationControlled"),
		)
	}
	// Modern "new" headless has a near-real fingerprint; headed uses the real
	// GPU (best render/timing fingerprint) — prefer --headless=false for hard
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

// setupTabListenersLocked installs per-tab event listeners (JS dialog
// auto-accept). Idempotent per tab. Caller must hold s.mu.
func (s *Session) setupTabListenersLocked(t *tab) {
	if t == nil || s.dialogListening[t] {
		return
	}
	s.dialogListening[t] = true
	chromedp.ListenTarget(t.ctx, func(ev any) {
		if _, ok := ev.(*page.EventJavascriptDialogOpening); !ok {
			return
		}
		go func() {
			_ = chromedp.Run(t.ctx, chromedp.ActionFunc(func(ctx context.Context) error {
				return page.HandleJavaScriptDialog(true).Do(ctx)
			}))
		}()
	})
}
