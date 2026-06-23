package snapshot

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/accessibility"
	"github.com/chromedp/cdproto/cdp"
	"github.com/go-json-experiment/json/jsontext"
)

// axStr builds an *accessibility.Value holding a JSON string, for test fixtures.
func axStr(s string) *accessibility.Value {
	return &accessibility.Value{Value: jsontext.Value(`"` + s + `"`)}
}

// TestBuildTreeClassification: interactive→Elems+Counts, heading→Elems+Headings,
// landmark→Landmarks only (NOT Elems), generic→dropped, ignored→skipped.
func TestBuildTreeClassification(t *testing.T) {
	nodes := []*accessibility.Node{
		{Role: axStr("button"), Name: axStr("Submit"), BackendDOMNodeID: 1},
		{Role: axStr("heading"), Name: axStr("Welcome"), BackendDOMNodeID: 2},
		{Role: axStr("main"), Name: axStr(""), BackendDOMNodeID: 3},
		{Role: axStr("generic"), Name: axStr("noise"), BackendDOMNodeID: 4},
		{Ignored: true, Role: axStr("button"), Name: axStr("hidden")},
	}
	tree := BuildTree(nodes)
	if len(tree.Elems) != 2 {
		t.Errorf("Elems: want 2 (button+heading), got %d", len(tree.Elems))
	}
	if tree.Counts["button"] != 1 {
		t.Errorf("Counts[button] = %d, want 1", tree.Counts["button"])
	}
	if len(tree.Headings) != 1 {
		t.Errorf("Headings: got %d, want 1", len(tree.Headings))
	}
	if len(tree.Landmarks) != 1 {
		t.Errorf("Landmarks: got %d, want 1", len(tree.Landmarks))
	}
	// Landmark must NOT be in Elems (no ref assigned).
	for _, el := range tree.Elems {
		if el.Role == "main" {
			t.Error("landmark 'main' should not be in Elems")
		}
	}
	if tree.Elems[0].Ref != "r1" {
		t.Errorf("first ref = %q, want r1", tree.Elems[0].Ref)
	}
}

// TestRenderSummaryCap: beyond MaxSummaryElements, the render stops and hints.
func TestRenderSummaryCap(t *testing.T) {
	elems := make([]Element, MaxSummaryElements+50)
	for i := range elems {
		elems[i] = Element{Ref: fmt.Sprintf("r%d", i+1), Role: "link", Name: "x"}
	}
	tree := &Tree{Elems: elems}
	out := tree.Render(LevelSummary)
	if !strings.Contains(out, fmt.Sprintf("... and %d more", 50)) {
		t.Errorf("cap hint missing; tail = %q", tail(out, 100))
	}
}

// TestAssignRefsStable proves the core stability invariant: the same DOM node
// (same Backend id) keeps the same ref across re-renders, a new node gets a new
// ref, and a removed node's ref is never reused for a different node (monotonic
// counter) - so an agent holding an old ref can't silently retarget a different
// control after the page mutates. This is the fix for the positional-collision
// failure mode where r5 retargets to a different element after a re-render.
func TestAssignRefsStable(t *testing.T) {
	refMap := map[int64]string{}
	counter := 0

	// First build: page has a button (backend 10) + a link (backend 11).
	tree1 := BuildTree([]*accessibility.Node{
		{Role: axStr("button"), Name: axStr("Submit"), BackendDOMNodeID: 10},
		{Role: axStr("link"), Name: axStr("Learn more"), BackendDOMNodeID: 11},
	})
	tree1.AssignRefs(refMap, &counter)
	buttonRef := tree1.Elems[0].Ref
	linkRef := tree1.Elems[1].Ref
	if buttonRef != "r1" || linkRef != "r2" {
		t.Fatalf("first build: button=%q link=%q, want r1/r2", buttonRef, linkRef)
	}

	// Second build: page re-rendered. Same button + link (same backends) keep
	// their refs even though... a heading was inserted BEFORE them in tree order
	// (positional refs would have shifted: button r1->r2, link r2->r3). Stable
	// refs must NOT shift.
	tree2 := BuildTree([]*accessibility.Node{
		{Role: axStr("heading"), Name: axStr("New section"), BackendDOMNodeID: 12},
		{Role: axStr("button"), Name: axStr("Submit"), BackendDOMNodeID: 10},
		{Role: axStr("link"), Name: axStr("Learn more"), BackendDOMNodeID: 11},
	})
	tree2.AssignRefs(refMap, &counter)
	var btn2, link2, head2 string
	for _, e := range tree2.Elems {
		switch e.Backend {
		case 10:
			btn2 = e.Ref
		case 11:
			link2 = e.Ref
		case 12:
			head2 = e.Ref
		}
	}
	if btn2 != buttonRef {
		t.Errorf("button ref drifted: %q -> %q (must stay stable across re-render)", buttonRef, btn2)
	}
	if link2 != linkRef {
		t.Errorf("link ref drifted: %q -> %q (must stay stable across re-render)", linkRef, link2)
	}
	if head2 == "" {
		t.Error("new heading got no ref")
	}
	if head2 == buttonRef || head2 == linkRef {
		t.Errorf("new heading reused an existing ref %q", head2)
	}

	// Third build: the link (backend 11) was removed and a NEW checkbox (backend
	// 13) added. The button keeps its ref; the removed link's ref must NOT be
	// reused for the checkbox (monotonic counter = no silent retargeting).
	tree3 := BuildTree([]*accessibility.Node{
		{Role: axStr("button"), Name: axStr("Submit"), BackendDOMNodeID: 10},
		{Role: axStr("checkbox"), Name: axStr("Accept"), BackendDOMNodeID: 13},
	})
	tree3.AssignRefs(refMap, &counter)
	var btn3, check3 string
	for _, e := range tree3.Elems {
		switch e.Backend {
		case 10:
			btn3 = e.Ref
		case 13:
			check3 = e.Ref
		}
	}
	if btn3 != buttonRef {
		t.Errorf("button ref drifted after removal: %q -> %q", buttonRef, btn3)
	}
	if check3 == linkRef {
		t.Errorf("removed link's ref %q was reused for a different node (checkbox) - stale ref would silently retarget", linkRef)
	}
}

// TestBuildTreeDropsDecorativeUnfocused proves the decorative-junk filter: an
// interactive role on an element that is NOT focusable AND has no accessible
// name is dropped (a decorative div[role=button] / ad slot / span[role=button]
// with no handler - the junk a role whitelist can't otherwise distinguish from a
// real control). A native <button> (focusable) with no name stays; a named
// custom widget stays even if unfocusable.
func TestBuildTreeDropsDecorativeUnfocused(t *testing.T) {
	mk := func(role, name string, backend cdp.BackendNodeID, focusable bool) *accessibility.Node {
		n := &accessibility.Node{Role: axStr(role), Name: axStr(name), BackendDOMNodeID: backend}
		if focusable {
			n.Properties = append(n.Properties, &accessibility.Property{
				Name: accessibility.PropertyNameFocusable,
				Value: &accessibility.Value{Value: jsontext.Value(`true`)},
			})
		}
		return n
	}
	tree := BuildTree([]*accessibility.Node{
		mk("button", "", 1, false),           // decorative: unnamed + unfocusable -> DROP
		mk("button", "Save", 2, false),       // named custom (unfocusable) -> KEEP
		mk("button", "", 3, true),            // native icon button (focusable, unnamed) -> KEEP
		mk("link", "", 4, false),             // decorative link -> DROP
		mk("textbox", "", 5, true),           // unlabeled input (focusable) -> KEEP (act DOM fallback needs it)
	})
	refs := map[int64]bool{}
	for _, e := range tree.Elems {
		refs[e.Backend] = true
	}
	if refs[1] {
		t.Error("unnamed + unfocusable button should be dropped (decorative)")
	}
	if refs[4] {
		t.Error("unnamed + unfocusable link should be dropped (decorative)")
	}
	if !refs[2] {
		t.Error("named custom widget (unfocusable) should be kept")
	}
	if !refs[3] {
		t.Error("focusable unnamed button (real icon button) should be kept")
	}
	if !refs[5] {
		t.Error("focusable unlabeled input should be kept (act DOM fallback targets it)")
	}
	if len(tree.Elems) != 3 {
		t.Errorf("kept %d elements, want 3 (2 unnamed-unfocusable dropped)", len(tree.Elems))
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
