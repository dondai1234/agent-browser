package browser

import (
	"errors"
	"testing"

	"github.com/dondai1234/agent-browser/v3/internal/snapshot"
)

func treeWith(elems ...snapshot.Element) *snapshot.Tree {
	return &snapshot.Tree{Elems: elems}
}

func el(ref, role, name string, backend int64) snapshot.Element {
	return snapshot.Element{Ref: ref, Role: role, Name: name, Backend: backend}
}

func TestResolveIntentSingle(t *testing.T) {
	tr := treeWith(el("r1", "button", "Sign in", 1), el("r2", "link", "About", 2))
	got, cands, err := resolveIntent(tr, "Sign in", "", "", 0)
	if err != nil || len(cands) != 0 || got.Ref != "r1" {
		t.Fatalf("want r1 no err, got ref=%q cands=%d err=%v", got.Ref, len(cands), err)
	}
}

func TestResolveIntentExactWinsOverSubstring(t *testing.T) {
	tr := treeWith(
		el("r1", "button", "Sign in", 1),           // exact
		el("r2", "link", "Sign in with Google", 2), // substring
	)
	got, _, err := resolveIntent(tr, "Sign in", "", "", 0)
	if err != nil || got.Ref != "r1" {
		t.Fatalf("exact should win, got ref=%q err=%v", got.Ref, err)
	}
}

func TestResolveIntentAmbiguous(t *testing.T) {
	// Four identical "Add to cart" buttons -> all exact, all tie -> ambiguous.
	tr := treeWith(
		el("r1", "button", "Add to cart", 1),
		el("r2", "button", "Add to cart", 2),
		el("r3", "button", "Add to cart", 3),
		el("r4", "button", "Add to cart", 4),
	)
	got, cands, err := resolveIntent(tr, "Add to cart", "", "", 0)
	if !errors.Is(err, errAmbiguous) {
		t.Fatalf("want errAmbiguous, got err=%v ref=%q", err, got.Ref)
	}
	if len(cands) != 4 {
		t.Errorf("want 4 candidates, got %d", len(cands))
	}
	if got.Ref != "" {
		t.Errorf("ambiguous should not resolve, got ref=%q", got.Ref)
	}
}

func TestResolveIntentAmbiguousButtonAndLink(t *testing.T) {
	// A button AND a link both named "Sign in" -> tie -> ambiguous (don't guess).
	tr := treeWith(
		el("r1", "button", "Sign in", 1),
		el("r2", "link", "Sign in", 2),
	)
	_, cands, err := resolveIntent(tr, "Sign in", "", "", 0)
	if !errors.Is(err, errAmbiguous) || len(cands) != 2 {
		t.Fatalf("want ambiguous 2 cands, got err=%v cands=%d", err, len(cands))
	}
}

func TestResolveIntentNth(t *testing.T) {
	tr := treeWith(
		el("r1", "button", "Add to cart", 1),
		el("r2", "button", "Add to cart", 2),
		el("r3", "button", "Add to cart", 3),
	)
	got, _, err := resolveIntent(tr, "Add to cart", "", "", 2)
	if err != nil || got.Ref != "r2" {
		t.Fatalf("nth=2 should pick r2, got ref=%q err=%v", got.Ref, err)
	}
}

func TestResolveIntentNthOutOfRange(t *testing.T) {
	tr := treeWith(el("r1", "button", "Add to cart", 1))
	_, _, err := resolveIntent(tr, "Add to cart", "", "", 5)
	if err == nil {
		t.Fatal("nth out of range should error")
	}
}

func TestResolveIntentNthNegative(t *testing.T) {
	// Six identical "Add to cart" buttons (the saucedemo inventory case). nth=-1
	// picks the LAST (priciest after a price-asc sort), -2 the second-last, without
	// the agent having to count. Out-of-range negatives error.
	tr := treeWith(
		el("r1", "button", "Add to cart", 1),
		el("r2", "button", "Add to cart", 2),
		el("r3", "button", "Add to cart", 3),
		el("r4", "button", "Add to cart", 4),
		el("r5", "button", "Add to cart", 5),
		el("r6", "button", "Add to cart", 6),
	)
	got, _, err := resolveIntent(tr, "Add to cart", "", "", -1)
	if err != nil || got.Ref != "r6" {
		t.Fatalf("nth=-1 should pick r6 (last), got ref=%q err=%v", got.Ref, err)
	}
	got, _, err = resolveIntent(tr, "Add to cart", "", "", -2)
	if err != nil || got.Ref != "r5" {
		t.Fatalf("nth=-2 should pick r5 (second-last), got ref=%q err=%v", got.Ref, err)
	}
	got, _, err = resolveIntent(tr, "Add to cart", "", "", -6)
	if err != nil || got.Ref != "r1" {
		t.Fatalf("nth=-6 should pick r1 (first, wraps to start), got ref=%q err=%v", got.Ref, err)
	}
	_, _, err = resolveIntent(tr, "Add to cart", "", "", -7)
	if err == nil {
		t.Fatal("nth=-7 (only 6 matches) should error")
	}
}

func TestResolveIntentNoMatch(t *testing.T) {
	tr := treeWith(el("r1", "button", "Sign in", 1))
	got, cands, err := resolveIntent(tr, "Checkout", "", "", 0)
	if err == nil {
		t.Fatal("no match should error")
	}
	if got.Ref != "" || len(cands) != 0 {
		t.Errorf("no-match should return no ref + no candidates, got ref=%q cands=%d", got.Ref, len(cands))
	}
}

func TestResolveIntentEmpty(t *testing.T) {
	tr := treeWith(el("r1", "button", "Sign in", 1))
	_, _, err := resolveIntent(tr, "  ", "", "", 0)
	if err == nil {
		t.Fatal("empty intent should error")
	}
}

func TestResolveIntentRoleFilter(t *testing.T) {
	// A link and a textbox both containing "search"; role=textbox constrains to
	// the textbox.
	tr := treeWith(
		el("r1", "link", "Search help", 1),
		el("r2", "textbox", "Search", 2),
	)
	got, _, err := resolveIntent(tr, "Search", "", "textbox", 0)
	if err != nil || got.Ref != "r2" {
		t.Fatalf("role=textbox should pick r2, got ref=%q err=%v", got.Ref, err)
	}
}

func TestResolveIntentValuePrefersFillable(t *testing.T) {
	// A button AND a searchbox both exactly named "Search". With a value, the
	// searchbox (fillable, +20) must rank above the button (clickable, no boost
	// when value given) and be resolved - not ambiguous.
	tr := treeWith(
		el("r1", "button", "Search", 1),
		el("r2", "searchbox", "Search", 2),
	)
	got, _, err := resolveIntent(tr, "Search", "cats", "", 0)
	if err != nil {
		t.Fatalf("value should disambiguate to the searchbox, got err=%v", err)
	}
	if got.Ref != "r2" {
		t.Errorf("value should prefer the fillable searchbox r2, got ref=%q", got.Ref)
	}
}

func TestResolveIntentNoValuePrefersClickable(t *testing.T) {
	// Same setup, NO value: the button (clickable +20) wins over the searchbox
	// (fillable, no boost without value) -> resolve the button, click it.
	tr := treeWith(
		el("r1", "button", "Search", 1),
		el("r2", "searchbox", "Search", 2),
	)
	got, _, err := resolveIntent(tr, "Search", "", "", 0)
	if err != nil {
		t.Fatalf("no value should disambiguate to the button, got err=%v", err)
	}
	if got.Ref != "r1" {
		t.Errorf("no value should prefer the clickable button r1, got ref=%q", got.Ref)
	}
}
