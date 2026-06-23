package snapshot

import "testing"

// verdictFor builds a delta from before/after and returns its verdict, so the
// verdict tests read as the scenario, not the diff plumbing.
func verdictFor(before, after *Tree) string {
	return Diff(before, after).InferVerdict()
}

func TestInferVerdictNavigated(t *testing.T) {
	before := &Tree{URL: "https://x"}
	after := &Tree{URL: "https://y", Title: "Y"}
	if got := verdictFor(before, after); got != `navigated to https://y "Y"` {
		t.Errorf("got %q", got)
	}
}

func TestInferVerdictDialogOpened(t *testing.T) {
	before := &Tree{URL: "https://x"}
	after := &Tree{URL: "https://x", Signals: []Element{{Role: "dialog", Name: "Confirm delete", Backend: 50}}}
	if got := verdictFor(before, after); got != "dialog opened: Confirm delete" {
		t.Errorf("got %q", got)
	}
}

func TestInferVerdictDialogWithToast(t *testing.T) {
	before := &Tree{URL: "https://x"}
	after := &Tree{URL: "https://x", Signals: []Element{
		{Role: "dialog", Name: "Confirm", Backend: 50},
		{Role: "status", Name: "Saved", Backend: 51},
	}}
	if got := verdictFor(before, after); got != "dialog opened: Confirm; Saved" {
		t.Errorf("got %q", got)
	}
}

func TestInferVerdictStatus(t *testing.T) {
	before := &Tree{URL: "https://x"}
	after := &Tree{URL: "https://x", Signals: []Element{{Role: "alert", Name: "Item added to cart", Backend: 60}}}
	if got := verdictFor(before, after); got != "status: Item added to cart" {
		t.Errorf("got %q", got)
	}
	// role=status with empty name must NOT produce a bare "status:" line.
	after2 := &Tree{URL: "https://x", Signals: []Element{{Role: "status", Name: "", Backend: 61}}, Elems: []Element{{Ref: "r1", Role: "button", Name: "Go", Backend: 70}}}
	if got := verdictFor(after, after2); got == "status: " {
		t.Errorf("empty-name status should fall through, got %q", got)
	}
}

func TestInferVerdictDialogClosed(t *testing.T) {
	before := &Tree{URL: "https://x", Signals: []Element{{Role: "dialog", Name: "Confirm", Backend: 50}}}
	after := &Tree{URL: "https://x"}
	if got := verdictFor(before, after); got != "dialog closed" {
		t.Errorf("got %q", got)
	}
}

func TestInferVerdictChanged(t *testing.T) {
	before := &Tree{URL: "https://x", Elems: []Element{{Ref: "r1", Role: "button", Name: "Add", Backend: 1}}}
	after := &Tree{URL: "https://x", Elems: []Element{
		{Ref: "r1", Role: "button", Name: "Remove", Backend: 1}, // changed
		{Ref: "r2", Role: "link", Name: "New", Backend: 2},      // added
	}}
	if got := verdictFor(before, after); got != "changed: +1 -0 ~1" {
		t.Errorf("got %q", got)
	}
}

func TestInferVerdictNoChanges(t *testing.T) {
	before := &Tree{URL: "https://x", Elems: []Element{{Ref: "r1", Role: "button", Name: "Go", Backend: 1}}}
	after := &Tree{URL: "https://x", Elems: []Element{{Ref: "r1", Role: "button", Name: "Go", Backend: 1}}}
	if got := verdictFor(before, after); got != "no visible effect" {
		t.Errorf("got %q", got)
	}
}

// TestInferVerdictPriority: a dialog + a status + element changes all at once
// must report the dialog (strongest signal), not the generic change counts.
// This guards the priority order: navigation > dialog > status > closed > generic.
func TestInferVerdictPriority(t *testing.T) {
	before := &Tree{URL: "https://x", Elems: []Element{{Ref: "r1", Role: "button", Name: "Add", Backend: 1}}}
	after := &Tree{URL: "https://x", Elems: []Element{
		{Ref: "r1", Role: "button", Name: "Remove", Backend: 1},
		{Ref: "r2", Role: "button", Name: "OK", Backend: 2},
	}, Signals: []Element{
		{Role: "dialog", Name: "Confirm", Backend: 50},
		{Role: "alert", Name: "Done", Backend: 51},
	}}
	if got := verdictFor(before, after); got != "dialog opened: Confirm; Done" {
		t.Errorf("dialog must win over generic changes, got %q", got)
	}
}

// TestDiffSignals: signal nodes are diffed by backend like elements, populating
// AddedSignals/RemovedSignals so the verdict can detect a toast appearing or a
// modal closing.
func TestDiffSignals(t *testing.T) {
	before := &Tree{URL: "https://x", Signals: []Element{
		{Role: "dialog", Name: "Old", Backend: 50},
	}}
	after := &Tree{URL: "https://x", Signals: []Element{
		{Role: "alert", Name: "Saved", Backend: 51}, // added
		{Role: "status", Name: "v2", Backend: 52},   // added
		// 50 gone -> removed
	}}
	d := Diff(before, after)
	if len(d.AddedSignals) != 2 {
		t.Errorf("AddedSignals: want 2, got %d (%+v)", len(d.AddedSignals), d.AddedSignals)
	}
	if len(d.RemovedSignals) != 1 || d.RemovedSignals[0].Backend != 50 {
		t.Errorf("RemovedSignals: want 1 (backend=50), got %+v", d.RemovedSignals)
	}
}

// TestDiffSignalsNoBackend: signal nodes without a backing DOM node (Backend=0,
// e.g. a virtual a11y alert) can't be tracked by id and must be skipped by the
// diff, never crashing or producing phantom added/removed entries.
func TestDiffSignalsNoBackend(t *testing.T) {
	before := &Tree{URL: "https://x", Signals: []Element{{Role: "alert", Name: "x", Backend: 0}}}
	after := &Tree{URL: "https://x", Signals: []Element{{Role: "alert", Name: "y", Backend: 0}}}
	d := Diff(before, after)
	if len(d.AddedSignals) != 0 || len(d.RemovedSignals) != 0 {
		t.Errorf("Backend=0 signals must be skipped, got added=%d removed=%d", len(d.AddedSignals), len(d.RemovedSignals))
	}
}

// sigTree builds a Tree from Elems with its content Signature computed, so the
// ContentChanged tests read as the scenario (a reorder) not the hash plumbing.
func sigTree(url string, elems []Element) *Tree {
	t := &Tree{URL: url, Elems: elems}
	t.Signature = t.computeSignature()
	return t
}

// TestInferVerdictContentChangedReorder: a sort reorders the same items (same
// backend ids, same names/values), so the backend-id element diff sees NO
// add/remove/changed. Without the signature check the verdict would be the
// misleading "no visible effect" (the report's saucedemo sort complaint). The
// content signature catches the reorder and the verdict says "page updated".
func TestInferVerdictContentChangedReorder(t *testing.T) {
	before := sigTree("https://x", []Element{
		{Ref: "r1", Role: "button", Name: "Sauce Labs Backpack", Backend: 1},
		{Ref: "r2", Role: "button", Name: "Sauce Labs Bike Light", Backend: 2},
	})
	after := sigTree("https://x", []Element{
		{Ref: "r2", Role: "button", Name: "Sauce Labs Bike Light", Backend: 2},
		{Ref: "r1", Role: "button", Name: "Sauce Labs Backpack", Backend: 1},
	})
	d := Diff(before, after)
	if d.HasChanges() {
		t.Fatalf("a pure reorder must NOT register as add/remove/changed, got added=%d removed=%d changed=%d", len(d.Added), len(d.Removed), len(d.Changed))
	}
	if !d.ContentChanged {
		t.Fatalf("expected ContentChanged=true for a reorder (sig before=%d after=%d)", before.Signature, after.Signature)
	}
	want := "page updated (URL stable; e.g. sort/filter/SPA re-render) - call see to refresh refs"
	if got := d.InferVerdict(); got != want {
		t.Errorf("verdict=%q, want %q", got, want)
	}
	if got := d.Render(); got != want {
		t.Errorf("render=%q, want %q", got, want)
	}
}

// TestInferVerdictNoChangeNoSignature: identical content + order must NOT set
// ContentChanged (no false "page updated" when nothing moved).
func TestInferVerdictNoChangeNoSignature(t *testing.T) {
	before := sigTree("https://x", []Element{{Ref: "r1", Role: "button", Name: "Go", Backend: 1}})
	after := sigTree("https://x", []Element{{Ref: "r1", Role: "button", Name: "Go", Backend: 1}})
	d := Diff(before, after)
	if d.ContentChanged {
		t.Fatal("identical trees must not set ContentChanged")
	}
	if got := d.InferVerdict(); got != "no visible effect" {
		t.Errorf("verdict=%q, want no visible effect", got)
	}
}

// TestInferVerdictChangedBeatsContentChanged: when a real add/remove/changed
// occurs the signature also shifts, but the element-level verdict must win
// (ContentChanged is the fallback for when HasChanges is false, not a override).
func TestInferVerdictChangedBeatsContentChanged(t *testing.T) {
	before := sigTree("https://x", []Element{{Ref: "r1", Role: "button", Name: "Add", Backend: 1}})
	after := sigTree("https://x", []Element{
		{Ref: "r1", Role: "button", Name: "Remove", Backend: 1}, // changed
		{Ref: "r2", Role: "link", Name: "New", Backend: 2},      // added
	})
	d := Diff(before, after)
	if !d.HasChanges() || !d.ContentChanged {
		t.Fatalf("want HasChanges + ContentChanged both true, got changes=%v content=%v", d.HasChanges(), d.ContentChanged)
	}
	if got := d.InferVerdict(); got != "changed: +1 -0 ~1" {
		t.Errorf("verdict=%q, want changed: +1 -0 ~1 (element verdict wins)", got)
	}
}

// TestSignatureStableAcrossRefReassign: the content signature is over
// (role,name,value) only, NOT refs, so re-assigning refs (AssignRefs) on the
// same content does not change the signature (a re-snapshot of an unchanged
// page must not false-positive ContentChanged).
func TestSignatureStableAcrossRefReassign(t *testing.T) {
	a := sigTree("https://x", []Element{{Ref: "r1", Role: "button", Name: "Go", Backend: 1}})
	b := &Tree{URL: "https://x", Elems: []Element{{Ref: "r99", Role: "button", Name: "Go", Backend: 1}}}
	b.Signature = b.computeSignature()
	if a.Signature != b.Signature {
		t.Fatalf("signature must ignore refs; got a=%d b=%d", a.Signature, b.Signature)
	}
}
