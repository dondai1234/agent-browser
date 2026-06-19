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
	Counts    map[string]int
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
	total := 0
	roles := make([]string, 0, len(t.Counts))
	for r, c := range t.Counts {
		total += c
		roles = append(roles, fmt.Sprintf("%s:%d", r, c))
	}
	sort.Strings(roles)
	fmt.Fprintf(&b, "interactive: %d (%s)\n", total, strings.Join(roles, ", "))
	return strings.TrimRight(b.String(), "\n")
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
