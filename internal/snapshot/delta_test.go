package snapshot

import (
	"fmt"
	"strings"
	"testing"
)

// TestDiffElementLevel: a non-navigation update must classify changed/removed/added
// by Backend id, and Added/Changed must carry NEW refs the agent can act on next.
func TestDiffElementLevel(t *testing.T) {
	before := &Tree{URL: "https://x", Elems: []Element{
		{Ref: "r1", Role: "button", Name: "Submit", Backend: 100},
		{Ref: "r2", Role: "link", Name: "About", Backend: 101},
	}}
	after := &Tree{URL: "https://x", Elems: []Element{
		{Ref: "r1", Role: "button", Name: "Submit (done)", Backend: 100}, // changed
		// 101 gone → removed
		{Ref: "r2", Role: "link", Name: "Contact", Backend: 102}, // added
	}}
	d := Diff(before, after)
	if d.Navigated {
		t.Fatal("expected not navigated, got navigated")
	}
	if len(d.Changed) != 1 || d.Changed[0].Backend != 100 {
		t.Errorf("changed: want 1 (backend=100), got %+v", d.Changed)
	}
	if len(d.Removed) != 1 || d.Removed[0].Backend != 101 {
		t.Errorf("removed: want 1 (backend=101), got %+v", d.Removed)
	}
	if len(d.Added) != 1 || d.Added[0].Backend != 102 {
		t.Errorf("added: want 1 (backend=102), got %+v", d.Added)
	}
	if d.Added[0].Ref != "r2" {
		t.Errorf("added should carry NEW ref: got %q want r2", d.Added[0].Ref)
	}
}

func TestDiffNavigated(t *testing.T) {
	before := &Tree{URL: "https://x", Elems: []Element{{Ref: "r1", Backend: 100}}}
	after := &Tree{URL: "https://y", Title: "Y", Elems: []Element{{Ref: "r1", Backend: 200}}}
	d := Diff(before, after)
	if !d.Navigated {
		t.Fatal("expected navigated")
	}
	if got := d.Render(); got != `navigated: https://y "Y"` {
		t.Errorf("render = %q", got)
	}
}

func TestDiffNoChanges(t *testing.T) {
	before := &Tree{URL: "https://x", Elems: []Element{{Ref: "r1", Role: "button", Name: "Go", Backend: 100}}}
	after := &Tree{URL: "https://x", Elems: []Element{{Ref: "r1", Role: "button", Name: "Go", Backend: 100}}}
	d := Diff(before, after)
	if d.HasChanges() {
		t.Fatal("expected no changes")
	}
	if got := d.Render(); got != "no changes (no visible effect; call see to refresh refs if you expected one)" {
		t.Errorf("render = %q, want %q", got, "no changes (no visible effect; call see to refresh refs if you expected one)")
	}
}

// TestDiffNilBefore: a nil before-tree (first snapshot) is treated as navigation.
func TestDiffNilBefore(t *testing.T) {
	after := &Tree{URL: "https://x", Elems: []Element{{Ref: "r1", Backend: 100}}}
	d := Diff(nil, after)
	if !d.Navigated {
		t.Fatal("nil before should be navigated")
	}
}

// TestDiffAddedCap: an autocomplete-style burst of option elements must be
// capped low (MaxDeltaOptions) with an overflow hint pointing to find. This is
// the audit's bloat complaint; the cap tames it without touching other roles.
func TestDiffAddedCap(t *testing.T) {
	before := &Tree{URL: "https://x"}
	afterElems := make([]Element, 20)
	for i := range afterElems {
		afterElems[i] = Element{Ref: fmt.Sprintf("r%d", i+1), Role: "option", Name: "sug" + fmt.Sprint(i), Backend: int64(100 + i)}
	}
	after := &Tree{URL: "https://x", Elems: afterElems}
	d := Diff(before, after)
	if d.Navigated || len(d.Added) != 20 {
		t.Fatalf("expected 20 added non-nav, got navigated=%v added=%d", d.Navigated, len(d.Added))
	}
	out := d.Render()
	if !strings.Contains(out, "15 more options (find role=option)") {
		t.Errorf("expected '15 more options' hint, got:\n%s", out)
	}
	for i := 0; i < MaxDeltaOptions; i++ {
		if !strings.Contains(out, fmt.Sprintf("r%d", i+1)) {
			t.Errorf("expected first %d option refs in render, got:\n%s", MaxDeltaOptions, out)
		}
	}
	if strings.Contains(out, "r20") {
		t.Errorf("cut option r20 should not appear, got:\n%s", out)
	}
}

// TestDiffAddedOtherUncapped: important interactive elements (textbox, button,
// link) get the generous MaxDeltaOther cap, so a real form/modal opening isn't
// hidden behind the autocomplete cap. A 10-field form must show all 10 - this is
// the "don't skip important things" guarantee.
func TestDiffAddedOtherUncapped(t *testing.T) {
	before := &Tree{URL: "https://x"}
	afterElems := make([]Element, 10)
	for i := range afterElems {
		afterElems[i] = Element{Ref: fmt.Sprintf("r%d", i+1), Role: "textbox", Name: "field" + fmt.Sprint(i), Backend: int64(100 + i)}
	}
	after := &Tree{URL: "https://x", Elems: afterElems}
	d := Diff(before, after)
	out := d.Render()
	if strings.Contains(out, "more options") || strings.Contains(out, "more elements") {
		t.Errorf("10 textboxes should not be capped, got:\n%s", out)
	}
	for i := 0; i < 10; i++ {
		if !strings.Contains(out, fmt.Sprintf("r%d", i+1)) {
			t.Errorf("expected all 10 textbox refs, missing r%d, got:\n%s", i+1, out)
		}
	}
}
