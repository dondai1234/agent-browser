// Package snapshot builds token-efficient page snapshots from the CDP
// accessibility tree: the browser's own a11y tree converted to a dense
// ref-line format the agent can act on. Not raw HTML, not injected JS.
//
// Levels: minimal (orientation: landmarks + headings + interactive counts),
// summary (full interactive + heading list, capped at MaxSummaryElements with
// an overflow hint to use find), full (summary + visible text).
//
// Find is intent-first: locate by role and/or name without paying for the
// whole tree. Find = substring (case-insensitive); FindExact = exact name.
//
// Delta (see delta.go) reports only what changed between two trees, so an
// action can return act-and-see state without a separate snapshot call.
package snapshot

import (
	"fmt"
	"sort"
	"strings"

	"github.com/chromedp/cdproto/accessibility"
)

// Level controls how much of the page a snapshot returns.
type Level string

const (
	LevelMinimal Level = "minimal" // orientation: landmarks + headings + interactive counts
	LevelSummary Level = "summary" // working set: full interactive + heading list with refs
	LevelFull    Level = "full"    // summary + visible text content
	LevelBrief   Level = "brief"   // comprehension: page type + auth + primary actions + regions + counts (~50 tok)
)

// MaxSummaryElements caps a summary render. Beyond this, the render stops and
// appends an overflow hint nudging the agent to use find (intent-first) rather
// than paying for a huge dump.
const MaxSummaryElements = 150

// Element is one kept node (interactive or heading) with its ref and state.
type Element struct {
	Ref     string
	Role    string
	Name    string
	Value   string
	Frame   string // owning iframe title (set by Session.SetFrames for in-iframe elements)
	Backend int64  // cdp.BackendNodeID; 0 if the a11y node has no backing DOM node
	Props   map[accessibility.PropertyName]string
}

// Tree is the structured page representation every level + find + delta
// derives from. Built once from the AX nodes; rendered at any level.
type Tree struct {
	URL       string
	Title     string
	Challenge string // bot-check interstitial detected (Cloudflare/captcha); surfaced in minimal
	Text      string // visible page text (set by Session.FillText for the full level)
	Nodes     []*accessibility.Node
	Elems     []Element // interactive + heading elements, in tree order
	Landmarks []Element // banner/main/nav/footer/region/form/search
	Headings  []Element // heading elements only
	Signals   []Element // live-region/modal nodes (alert/status/dialog) kept for verdict detection only - never rendered, never get a ref
	Counts    map[string]int
}

// signalRoles are live-region/modal roles that carry an outcome signal (a
// toast, a status update, a modal opening/closing). They are NOT interactive,
// so they don't get a ref or a snapshot line - but they're exactly what a
// verdict needs to say "it worked" / "a dialog opened". Kept in Tree.Signals
// and diffed separately so the verdict can detect outcomes the element delta
// misses (a success toast, a modal appearing).
var signalRoles = map[string]bool{
	"alert": true, "status": true, "alertdialog": true, "dialog": true,
}

// interactiveRoles: roles an agent can act on. Structural noise (generic,
// paragraph, list, group) is dropped unless it's a heading or landmark.
var interactiveRoles = map[string]bool{
	"button": true, "link": true, "textbox": true, "searchbox": true,
	"combobox": true, "checkbox": true, "radio": true,
	"menu": true, "menuitem": true, "menuitemcheckbox": true, "menuitemradio": true,
	"tab": true, "listbox": true, "option": true, "spinbutton": true,
	"slider": true, "switch": true, "treeitem": true, "textfield": true,
}

var landmarkRoles = map[string]bool{
	"banner": true, "main": true, "navigation": true, "complementary": true,
	"contentinfo": true, "region": true, "form": true, "search": true,
}

// BuildTree classifies AX nodes into a Tree.
func BuildTree(nodes []*accessibility.Node) *Tree {
	t := &Tree{Nodes: nodes, Counts: map[string]int{}}
	for _, n := range nodes {
		if n == nil || n.Ignored {
			continue
		}
		role := axString(n.Role)
		name := axString(n.Name)
		props := propsOf(n)
		val := axString(n.Value)
		backend := int64(n.BackendDOMNodeID)

		switch {
		case interactiveRoles[role]:
			t.addElement(role, name, val, backend, props)
			t.Counts[role]++
		case role == "heading":
			t.Headings = append(t.Headings, t.addElement(role, name, val, backend, props))
		case landmarkRoles[role]:
			// Landmarks are orientation-only (shown in minimal); they are NOT
			// actionable and don't get a ref or an entry in Elems.
			t.Landmarks = append(t.Landmarks, Element{Role: role, Name: name, Backend: backend, Props: props})
		case role == "image" && name != "":
			t.addElement(role, name, val, backend, props)
		case signalRoles[role]:
			// Outcome-signal nodes (toasts/modals). No ref, not rendered - kept only
			// for verdict detection (see delta.go InferVerdict).
			t.Signals = append(t.Signals, Element{Role: role, Name: name, Value: val, Backend: backend, Props: props})
		}
	}
	return t
}

func (t *Tree) addElement(role, name, val string, backend int64, props map[accessibility.PropertyName]string) Element {
	ref := fmt.Sprintf("r%d", len(t.Elems)+1)
	el := Element{Ref: ref, Role: role, Name: name, Value: val, Backend: backend, Props: props}
	t.Elems = append(t.Elems, el)
	return el
}

// SetFrames attaches the owning iframe title to elements whose backend node
// lives inside a same-origin iframe (from Session.gatherIframeAX). Lets the
// ref-line annotation show `in "..."` so the agent knows an element is in an
// iframe (and disambiguates same-named elements across frames).
func (t *Tree) SetFrames(frameOf map[int64]string) {
	if len(frameOf) == 0 {
		return
	}
	for i := range t.Elems {
		if t.Elems[i].Backend == 0 {
			continue
		}
		if f, ok := frameOf[t.Elems[i].Backend]; ok {
			t.Elems[i].Frame = f
		}
	}
}

// Render returns the snapshot text for the given level. Unknown levels fall
// back to summary.
func (t *Tree) Render(level Level) string {
	switch level {
	case LevelMinimal:
		return t.renderMinimal()
	case LevelBrief:
		return t.renderBrief()
	case LevelFull:
		return t.renderSummary() + t.renderText()
	default:
		return t.renderSummary()
	}
}

// renderMinimal: orientation. Landmarks + headings + interactive counts only.
func (t *Tree) renderMinimal() string {
	var b strings.Builder
	if t.URL != "" {
		fmt.Fprintf(&b, "url: %s\n", t.URL)
	}
	if t.Title != "" {
		fmt.Fprintf(&b, "title: %q\n", t.Title)
	}
	if t.Challenge != "" {
		fmt.Fprintf(&b, "CHALLENGE: %s\n", t.Challenge)
	}
	if len(t.Landmarks) > 0 {
		roles := make([]string, 0, len(t.Landmarks))
		for _, l := range t.Landmarks {
			s := l.Role
			if l.Name != "" {
				s += fmt.Sprintf(" %q", l.Name)
			}
			roles = append(roles, s)
		}
		fmt.Fprintf(&b, "landmarks: %s\n", strings.Join(roles, ", "))
	}
	if len(t.Headings) > 0 {
		b.WriteString("headings:\n")
		for _, h := range t.Headings {
			lvl := h.Props[accessibility.PropertyNameLevel]
			fmt.Fprintf(&b, "  h%s %q\n", lvl, h.Name)
		}
	}
	b.WriteString(t.countsLine() + "\n")
	return strings.TrimRight(b.String(), "\n")
}

// countsLine returns "interactive: N (role:count, ...)" sorted, shared by the
// minimal and brief levels so the count format can't drift between them.
func (t *Tree) countsLine() string {
	total := 0
	roles := make([]string, 0, len(t.Counts))
	for r, c := range t.Counts {
		total += c
		roles = append(roles, fmt.Sprintf("%s:%d", r, c))
	}
	sort.Strings(roles)
	return fmt.Sprintf("interactive: %d (%s)", total, strings.Join(roles, ", "))
}

// renderSummary: full interactive + heading element list with refs, capped.
func (t *Tree) renderSummary() string {
	n := len(t.Elems)
	if n <= MaxSummaryElements {
		lines := make([]string, n)
		for i, el := range t.Elems {
			lines[i] = formatElement(el)
		}
		return strings.Join(lines, "\n")
	}
	lines := make([]string, MaxSummaryElements)
	for i := 0; i < MaxSummaryElements; i++ {
		lines[i] = formatElement(t.Elems[i])
	}
	return strings.Join(lines, "\n") +
		fmt.Sprintf("\n... and %d more (use find to locate by role/text)", n-MaxSummaryElements)
}

// renderText appends the page's visible text for the full level. The text is
// attached by the Session (browser layer) via FillText, since the snapshot Tree
// itself has no page/ctx access. Empty unless the full level was requested.
func (t *Tree) renderText() string {
	if t.Text == "" {
		return ""
	}
	return "\ntext:\n" + t.Text
}

// renderBrief is the v2 comprehension layer: a dense ~6-line page brief so the
// agent lands oriented (what IS this page, am I logged in, what can I do here,
// where are the regions) without scanning refs. Pure heuristics over the AX
// tree - no DOM probe, no LLM, no extra CDP round-trip. Honest: pageType/auth
// are best-effort guesses from the tree, not ground truth; the agent can always
// drop to summary for the raw refs.
func (t *Tree) renderBrief() string {
	var b strings.Builder
	if t.URL != "" {
		fmt.Fprintf(&b, "url: %s\n", t.URL)
	}
	if t.Title != "" {
		fmt.Fprintf(&b, "title: %q\n", t.Title)
	}
	if t.Challenge != "" {
		fmt.Fprintf(&b, "CHALLENGE: %s\n", t.Challenge)
	}
	fmt.Fprintf(&b, "page: %s\n", t.PageType())
	fmt.Fprintf(&b, "auth: %s\n", t.AuthState())
	if acts := t.primaryActions(); len(acts) > 0 {
		parts := make([]string, 0, len(acts))
		for _, a := range acts {
			parts = append(parts, fmt.Sprintf("%s %s %q", a.Ref, a.Role, a.Name))
		}
		fmt.Fprintf(&b, "actions: %s\n", strings.Join(parts, " | "))
	}
	if len(t.Landmarks) > 0 {
		fmt.Fprintf(&b, "regions: %s\n", strings.Join(uniqueLandmarkRoles(t.Landmarks), " "))
	}
	b.WriteString(t.countsLine() + "\n")
	return strings.TrimRight(b.String(), "\n")
}

// pageType returns a one/two-word classification of what the page IS, so the
// agent can pick a strategy without reading every ref. Heuristic over the AX
// tree only - honest: falls back to "page" when unsure. Priority: challenge >
// open dialog > login/signup (password field) > list/search results > article >
// form > nav/landing > generic page.
func (t *Tree) PageType() string {
	if t.Challenge != "" {
		return "challenge interstitial"
	}
	for _, s := range t.Signals {
		if s.Role == "dialog" || s.Role == "alertdialog" {
			name := s.Name
			if name == "" {
				name = s.Role
			}
			return "dialog: " + name
		}
	}
	// A textbox named "password" is the strongest login/signup signal.
	hasPwd := false
	for _, e := range t.Elems {
		if e.Role == "textbox" && containsLower(e.Name, "password") {
			hasPwd = true
			break
		}
	}
	if hasPwd {
		for _, e := range t.Elems {
			if (e.Role == "button" || e.Role == "link") && (containsLower(e.Name, "sign up") || containsLower(e.Name, "register") || containsLower(e.Name, "create account")) {
				return "signup form"
			}
		}
		return "login form"
	}
	if t.Counts["link"] >= 15 || t.Counts["option"] >= 8 {
		return "list / search results"
	}
	if len(t.Headings) >= 3 && t.Counts["link"] < 10 {
		return "article / content"
	}
	for _, l := range t.Landmarks {
		if l.Role == "form" && t.Counts["textbox"] > 0 {
			return "form"
		}
	}
	if t.Counts["link"] >= 10 {
		return "nav / landing page"
	}
	return "page"
}

// authState returns a best-effort auth signal: "logged in" if a logout/sign-out
// control is present, "anonymous" if a login/sign-in control is, "blocked" on
// a challenge, else "unknown". Honest: a guess from visible controls, not a
// read of the actual session - a site with neither control reads "unknown".
func (t *Tree) AuthState() string {
	if t.Challenge != "" {
		return "blocked"
	}
	for _, e := range t.Elems {
		if !isClickable(e.Role) {
			continue
		}
		n := strings.ToLower(e.Name)
		if strings.Contains(n, "log out") || strings.Contains(n, "sign out") || strings.Contains(n, "logout") || strings.Contains(n, "signout") {
			return "logged in"
		}
	}
	for _, e := range t.Elems {
		if !isClickable(e.Role) {
			continue
		}
		n := strings.ToLower(e.Name)
		if strings.Contains(n, "log in") || strings.Contains(n, "sign in") || strings.Contains(n, "login") || strings.Contains(n, "signin") {
			return "anonymous"
		}
	}
	return "unknown"
}

// actionVerbs names that mark a control as a primary action. Order matters
// only for readability (we collect all matches, capped at 5).
var actionVerbs = []string{
	"sign in", "log in", "sign up", "register", "submit", "search", "buy",
	"add to cart", "checkout", "continue", "next", "save", "delete", "edit",
	"cancel", "confirm", "send", "create", "download", "play", "start", "stop",
	"back", "close", "add", "login", "signin", "signup",
}

// primaryActions returns up to 5 elements that look like the page's main
// actions (clickable + an action-verb name), topped up with the first few
// clickables when the page has few verb-named controls. Gives the agent a
// starting set of refs + intents without a find round-trip.
func (t *Tree) primaryActions() []Element {
	var matched []Element
	seen := map[string]bool{}
	for _, e := range t.Elems {
		if !isClickable(e.Role) {
			continue
		}
		n := strings.ToLower(e.Name)
		for _, v := range actionVerbs {
			if strings.Contains(n, v) {
				if !seen[e.Ref] {
					matched = append(matched, e)
					seen[e.Ref] = true
				}
				break
			}
		}
		if len(matched) >= 5 {
			return matched
		}
	}
	for _, e := range t.Elems {
		if len(matched) >= 5 {
			break
		}
		if !isClickable(e.Role) || seen[e.Ref] {
			continue
		}
		matched = append(matched, e)
		seen[e.Ref] = true
	}
	return matched
}

func isClickable(role string) bool {
	return role == "button" || role == "link" || role == "menuitem" || role == "tab"
}

func uniqueLandmarkRoles(ls []Element) []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range ls {
		if !seen[l.Role] {
			seen[l.Role] = true
			out = append(out, l.Role)
		}
	}
	return out
}

func containsLower(s, sub string) bool {
	return strings.Contains(strings.ToLower(s), sub)
}
