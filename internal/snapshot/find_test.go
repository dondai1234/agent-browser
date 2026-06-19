package snapshot

import "testing"

func TestFindByText(t *testing.T) {
	tree := &Tree{Elems: []Element{
		{Ref: "r1", Role: "link", Name: "Learn more"},
		{Ref: "r2", Role: "link", Name: "About"},
		{Ref: "r3", Role: "link", Name: "US holds off blacklisting X, more than 100 firms"},
	}}
	got := tree.Find("link", "more")
	// substring: matches both "Learn more" and "...more than..."
	if len(got) != 2 {
		t.Errorf("Find(link,more) = %d matches, want 2", len(got))
	}
}

func TestFindByRole(t *testing.T) {
	tree := &Tree{Elems: []Element{
		{Ref: "r1", Role: "link", Name: "A"},
		{Ref: "r2", Role: "button", Name: "B"},
		{Ref: "r3", Role: "button", Name: "C"},
	}}
	got := tree.Find("button", "")
	if len(got) != 2 {
		t.Errorf("Find(button) = %d, want 2", len(got))
	}
}

// TestFindExact: exact match must NOT match "Learn more" or "...more than..."
// when looking for "More" - this is the false-positive fix.
func TestFindExact(t *testing.T) {
	tree := &Tree{Elems: []Element{
		{Ref: "r1", Role: "link", Name: "Learn more"},
		{Ref: "r2", Role: "link", Name: "More"},
		{Ref: "r3", Role: "link", Name: "US holds off blacklisting X, more than 100 firms"},
	}}
	got := tree.FindExact("link", "more") // case-insensitive exact
	if len(got) != 1 || got[0].Ref != "r2" {
		t.Errorf("FindExact(link,more) = %+v, want only r2", got)
	}
}

func TestByRef(t *testing.T) {
	tree := &Tree{Elems: []Element{
		{Ref: "r1", Role: "link", Backend: 100},
		{Ref: "r2", Role: "button", Backend: 101},
	}}
	el, ok := tree.ByRef("r2")
	if !ok || el.Backend != 101 {
		t.Errorf("ByRef(r2) = %+v ok=%v, want backend 101", el, ok)
	}
	if _, ok := tree.ByRef("r99"); ok {
		t.Error("ByRef(r99) should be false")
	}
}
