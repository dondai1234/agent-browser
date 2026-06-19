package snapshot

import (
	"fmt"
	"strings"
	"testing"

	"github.com/chromedp/cdproto/accessibility"
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

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}
