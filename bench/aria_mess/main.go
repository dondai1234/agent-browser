// Package main is the "ugly ARIA" benchmark: it runs the agent-browser snapshot
// against a set of pages that model the worst real-world ARIA pathologies
// (generic soup, decorative div[role=button], duplicate main landmarks,
// mislabeled controls, nameless icon buttons, link soup, landmark soup, a
// composite messy SPA) and reports, per page, what the role whitelist actually
// keeps:
//
//	tokens         - chars in the summary render (/4 ~ tokens the agent pays)
//	total refs     - interactive + heading elements the whitelist kept
//	named refs     - refs with a non-empty a11y name (a "useful" proxy)
//	non-focusable  - kept interactive refs that are NOT focusable (decorative
//	                 div[role=button] with no tabindex - the junk a static
//	                 whitelist can't otherwise drop)
//	landmarks      - landmark count, with duplicate "main" called out
//
// The point: snapshot size on a clean docs page is the easy number. The honest
// test is what the whitelist keeps on the ugly end, where junk moves up from
// dead markup into the semantic tree. Synthetic pathologies (not a live crawl)
// so it's reproducible; each models a named real-world failure mode.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/chromedp/cdproto/accessibility"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
	"github.com/dondai1234/agent-browser/v2/internal/snapshot"
)

// pathology is one test page: a name + its HTML.
type pathology struct {
	name string
	html string
}

// genJunk returns n nested div[role=generic] wrapping inner.
func genJunk(n int, inner string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(`<div role="generic">`)
	}
	b.WriteString(inner)
	for i := 0; i < n; i++ {
		b.WriteString(`</div>`)
	}
	return b.String()
}

func many(n int, tag string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		b.WriteString(tag)
	}
	return b.String()
}

var pathologies = []pathology{
	{"clean-docs", `<!doctype html><html><head><title>Clean</title></head><body>
		<main><h1>Docs</h1><p>Intro.</p>
		<nav><a href="/a">Install</a><a href="/b">Guide</a><a href="/c">API</a></nav>
		<input type="search" placeholder="Search docs" aria-label="Search docs">
		</main></body></html>`},
	// generic soup: 10 nested role=generic wrappers around 3 real buttons. The
	// whitelist drops generic, so refs should NOT inflate.
	{"generic-soup", `<!doctype html><html><head><title>Soup</title></head><body>` +
		genJunk(10, `<button>Save</button><button>Cancel</button><button>Delete</button>`) +
		`</body></html>`},
	// decorative role=button: div[role=button] with no tabindex/name/handler
	// (decorative) + 3 real buttons. The decorative ones are non-focusable junk
	// the whitelist keeps (it can't tell they're decorative).
	{"decorative-role-button", `<!doctype html><html><head><title>Deco</title></head><body>
		<div role="button"></div><div role="button"></div><div role="button"></div>
		<div role="button"></div><div role="button"></div>
		<button>Save</button><button>Open</button><button>Share</button>
		</body></html>`},
	// duplicate main landmarks (3 claim main) - the commenter's named case.
	{"duplicate-main", `<!doctype html><html><head><title>Dup</title></head><body>
		<main><h1>A</h1></main><main><h1>B</h1></main><main><h1>C</h1></main>
		</body></html>`},
	// mislabeled controls: 10 buttons all aria-label="Click here" - named but
	// low-signal (the name carries no disambiguating info).
	{"mislabeled-controls", `<!doctype html><html><head><title>Mis</title></head><body>` +
		many(10, `<button aria-label="Click here"></button>`) +
		`</body></html>`},
	// nameless icon buttons: 5 buttons with only an svg (no text/aria-label) +
	// 3 real named buttons. The icon ones are unnamed refs.
	{"nameless-icon-buttons", `<!doctype html><html><head><title>Icon</title></head><body>` +
		many(5, `<button><svg><circle r="5"/></svg></button>`) +
		`<button>Reply</button><button>Forward</button><button>Archive</button></body></html>`},
	// link soup footer: 60 links.
	{"link-soup-footer", `<!doctype html><html><head><title>Links</title></head><body><main><h1>Site</h1></main><footer>` +
		many(60, `<a href="/x">Link</a>`) + `</footer></body></html>`},
	// aria on non-interactive: span[role=button] x10 (no tabindex, no handler).
	{"aria-on-noninteractive", `<!doctype html><html><head><title>Aria</title></head><body>` +
		many(10, `<span role="button">x</span>`) +
		`</body></html>`},
	// landmark soup: 5 nav + 3 main + 4 complementary.
	{"landmark-soup", `<!doctype html><html><head><title>LM</title></head><body>` +
		many(5, `<nav><a href="/x">n</a></nav>`) +
		many(3, `<main><h1>m</h1></main>`) +
		many(4, `<aside>side</aside>`) +
		`</body></html>`},
	// ad-slot divs: div[role=button] ad placeholders (no name/tabindex) x8.
	{"ad-slot-divs", `<!doctype html><html><head><title>Ads</title></head><body><main><h1>Article</h1></main>` +
		many(8, `<div role="button" class="ad-slot"></div>`) +
		`</body></html>`},
	// composite messy SPA: the "ugly end" - generic soup + duplicate main + 20
	// nav links + decorative role=button ads + a few real buttons/inputs + nameless
	// icon buttons.
	{"messy-spa", `<!doctype html><html><head><title>Messy SPA</title></head><body>` +
		genJunk(6, `<nav>`+many(20, `<a href="/x">Nav link</a>`)+`</nav>`) +
		`<main><h1>Dashboard</h1></main><main><h1>Widgets</h1></main>` +
		many(6, `<div role="button" class="ad"></div>`) +
		many(5, `<button><svg><circle r="3"/></svg></button>`) +
		`<button>Save</button><input type="text" placeholder="Filter" aria-label="Filter"><button>Export</button>` +
		`</body></html>`},
}

type pageResult struct {
	Page        string
	Tokens      float64
	TotalRefs   int
	NamedRefs   int
	NonFocus    int
	Landmarks   int
	DupMain     int
}

func measure(s *browser.Session, name, html string, serve func(string) string) pageResult {
	page := serve(html)
	if err := s.Navigate(page); err != nil {
		fmt.Fprintf(os.Stderr, "  navigate %s: %v\n", name, err)
		return pageResult{Page: name}
	}
	if err := s.BuildTree(); err != nil {
		fmt.Fprintf(os.Stderr, "  buildtree %s: %v\n", name, err)
		return pageResult{Page: name}
	}
	tr := s.Tree()
	if tr == nil {
		return pageResult{Page: name}
	}
	tok := float64(len(tr.Render(snapshot.LevelSummary))) / 4
	total, named, nonfocus := 0, 0, 0
	for _, e := range tr.Elems {
		total++
		if strings.TrimSpace(e.Name) != "" {
			named++
		}
		if e.Props[accessibility.PropertyNameFocusable] != "true" {
			nonfocus++
		}
	}
	lm := len(tr.Landmarks)
	dupMain := 0
	seen := map[string]int{}
	for _, l := range tr.Landmarks {
		seen[l.Role]++
	}
	dupMain = seen["main"]
	return pageResult{name, tok, total, named, nonfocus, lm, dupMain}
}

func main() {
	real := flag.Bool("real", false, "also snapshot a few real live sites (best-effort, needs network)")
	timeout := flag.Duration("timeout", 3*time.Minute, "session wall-clock budget")
	flag.Parse()

	// Local fixture server for the synthetic pathologies.
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	serve := func(html string) string {
		// Serve each page at a unique path so navigation is clean.
		path := "/" + fmt.Sprintf("p%d", time.Now().UnixNano())
		mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, html)
		})
		return srv.URL + path
	}

	s, err := browser.New(browser.Config{Headless: true, Stealth: true, OpTimeout: 30 * time.Second})
	if err != nil {
		fmt.Fprintf(os.Stderr, "launch: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	_ = ctx

	var rows []pageResult
	for _, p := range pathologies {
		r := measure(s, p.name, p.html, serve)
		rows = append(rows, r)
		fmt.Fprintf(os.Stderr, "  %-22s tok=%.0f refs=%d named=%d nonfocus=%d lm=%d dupmain=%d\n",
			r.Page, r.Tokens, r.TotalRefs, r.NamedRefs, r.NonFocus, r.Landmarks, r.DupMain)
	}

	if *real {
		for _, u := range []string{
			"https://example.com",
			"https://news.ycombinator.com",
			"https://en.wikipedia.org/wiki/Accessibility",
		} {
			if err := s.Navigate(u); err != nil {
				fmt.Fprintf(os.Stderr, "  real navigate %s: %v (skipped)\n", u, err)
				continue
			}
			if err := s.BuildTree(); err != nil {
				fmt.Fprintf(os.Stderr, "  real buildtree %s: %v (skipped)\n", u, err)
				continue
			}
			tr := s.Tree()
			tok := float64(len(tr.Render(snapshot.LevelSummary))) / 4
			total, named, nonfocus := 0, 0, 0
			for _, e := range tr.Elems {
				total++
				if strings.TrimSpace(e.Name) != "" {
					named++
				}
				if e.Props[accessibility.PropertyNameFocusable] != "true" {
					nonfocus++
				}
			}
			rows = append(rows, pageResult{u, tok, total, named, nonfocus, len(tr.Landmarks), 0})
		}
	}

	// Sort so the table reads clean-docs -> ugly, then real.
	report(rows)
}

func report(rows []pageResult) {
	// Sort synthetic first (by a rough "messiness" = nonfocus+unnamed), then real.
	sort.SliceStable(rows, func(i, j int) bool {
		return messiness(rows[i]) < messiness(rows[j])
	})
	var b strings.Builder
	b.WriteString("\n=== ugly ARIA: what the whitelist keeps ===\n")
	b.WriteString(fmt.Sprintf("%-26s %7s %6s %6s %8s %5s %7s\n", "page", "tokens", "refs", "named", "nonfocus", "lms", "dupmain"))
	b.WriteString(strings.Repeat("-", 70) + "\n")
	for _, r := range rows {
		b.WriteString(fmt.Sprintf("%-26s %7.0f %6d %6d %8d %5d %7d\n", r.Page, r.Tokens, r.TotalRefs, r.NamedRefs, r.NonFocus, r.Landmarks, r.DupMain))
	}
	fmt.Print(b.String())
}

func messiness(r pageResult) int {
	// higher = messier. Real URLs sort last (Page starts with http).
	if strings.HasPrefix(r.Page, "http") {
		return 1000
	}
	return r.NonFocus + (r.TotalRefs - r.NamedRefs) + r.DupMain
}
