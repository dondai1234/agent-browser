package snapshot

import (
	"strconv"
	"strings"
	"testing"
)

func mkBriefTree() *Tree {
	return &Tree{URL: "https://x", Title: "X", Counts: map[string]int{}}
}

func TestPageTypeLogin(t *testing.T) {
	tr := mkBriefTree()
	tr.Elems = []Element{
		{Ref: "r1", Role: "textbox", Name: "Username", Backend: 1},
		{Ref: "r2", Role: "textbox", Name: "Password", Backend: 2},
		{Ref: "r3", Role: "button", Name: "Login", Backend: 3},
	}
	if got := tr.PageType(); got != "login form" {
		t.Errorf("got %q, want login form", got)
	}
}

func TestPageTypeSignup(t *testing.T) {
	tr := mkBriefTree()
	tr.Elems = []Element{
		{Ref: "r1", Role: "textbox", Name: "Password", Backend: 1},
		{Ref: "r2", Role: "button", Name: "Sign up", Backend: 2},
	}
	if got := tr.PageType(); got != "signup form" {
		t.Errorf("got %q, want signup form", got)
	}
}

func TestPageTypeDialog(t *testing.T) {
	tr := mkBriefTree()
	tr.Signals = []Element{{Role: "dialog", Name: "Confirm", Backend: 50}}
	if got := tr.PageType(); got != "dialog: Confirm" {
		t.Errorf("got %q, want dialog: Confirm", got)
	}
	// dialog with empty name -> "dialog: dialog" (no empty trailing colon)
	tr.Signals = []Element{{Role: "dialog", Name: "", Backend: 51}}
	if got := tr.PageType(); got != "dialog: dialog" {
		t.Errorf("got %q, want dialog: dialog", got)
	}
}

func TestPageTypeChallenge(t *testing.T) {
	tr := mkBriefTree()
	tr.Challenge = "Cloudflare"
	if got := tr.PageType(); got != "challenge interstitial" {
		t.Errorf("got %q, want challenge interstitial", got)
	}
}

func TestPageTypeList(t *testing.T) {
	tr := mkBriefTree()
	tr.Counts["link"] = 16
	if got := tr.PageType(); got != "list / search results" {
		t.Errorf("got %q, want list / search results", got)
	}
}

func TestPageTypeArticle(t *testing.T) {
	tr := mkBriefTree()
	tr.Headings = []Element{{Role: "heading"}, {Role: "heading"}, {Role: "heading"}}
	tr.Counts["link"] = 5
	if got := tr.PageType(); got != "article / content" {
		t.Errorf("got %q, want article / content", got)
	}
}

func TestPageTypeFallback(t *testing.T) {
	tr := mkBriefTree()
	tr.Elems = []Element{{Ref: "r1", Role: "button", Name: "Go", Backend: 1}}
	if got := tr.PageType(); got != "page" {
		t.Errorf("got %q, want page (fallback)", got)
	}
}

func TestAuthStateLoggedIn(t *testing.T) {
	tr := mkBriefTree()
	tr.Elems = []Element{{Ref: "r1", Role: "link", Name: "Log out", Backend: 1}}
	if got := tr.AuthState(); got != "logged in" {
		t.Errorf("got %q, want logged in", got)
	}
}

func TestAuthStateAnonymous(t *testing.T) {
	tr := mkBriefTree()
	tr.Elems = []Element{{Ref: "r1", Role: "button", Name: "Sign in", Backend: 1}}
	if got := tr.AuthState(); got != "anonymous" {
		t.Errorf("got %q, want anonymous", got)
	}
	// "logged in" must win over "anonymous" if BOTH controls are present (a
	// page with a logout link is an authenticated session, even if it also has
	// a "sign in" link to a different account).
	tr.Elems = append(tr.Elems, Element{Ref: "r2", Role: "link", Name: "Log out", Backend: 2})
	if got := tr.AuthState(); got != "logged in" {
		t.Errorf("logout should win over signin, got %q", got)
	}
}

func TestAuthStateUnknown(t *testing.T) {
	tr := mkBriefTree()
	tr.Elems = []Element{{Ref: "r1", Role: "button", Name: "Search", Backend: 1}}
	if got := tr.AuthState(); got != "unknown" {
		t.Errorf("got %q, want unknown", got)
	}
}

func TestAuthStateBlocked(t *testing.T) {
	tr := mkBriefTree()
	tr.Challenge = "Cloudflare"
	if got := tr.AuthState(); got != "blocked" {
		t.Errorf("got %q, want blocked", got)
	}
}

func TestPrimaryActionsVerbMatch(t *testing.T) {
	tr := mkBriefTree()
	tr.Elems = []Element{
		{Ref: "r1", Role: "button", Name: "Sign in", Backend: 1},
		{Ref: "r2", Role: "link", Name: "Sign up", Backend: 2},
		{Ref: "r3", Role: "button", Name: "Search", Backend: 3},
		{Ref: "r4", Role: "textbox", Name: "Email", Backend: 4}, // not clickable, skipped
	}
	acts := tr.primaryActions()
	if len(acts) != 3 {
		t.Fatalf("want 3 actions, got %d: %+v", len(acts), acts)
	}
	if acts[0].Ref != "r1" {
		t.Errorf("first action should be r1 (Sign in), got %s", acts[0].Ref)
	}
	// textbox must never be in primary actions
	for _, a := range acts {
		if a.Role == "textbox" {
			t.Errorf("textbox should not be a primary action: %+v", a)
		}
	}
}

func TestPrimaryActionsCappedAndTopped(t *testing.T) {
	tr := mkBriefTree()
	// 8 verb-named buttons -> capped at 5
	tr.Elems = make([]Element, 8)
	for i := range tr.Elems {
		tr.Elems[i] = Element{Ref: "r" + strconv.Itoa(i+1), Role: "button", Name: "Save " + strconv.Itoa(i), Backend: int64(i + 1)}
	}
	acts := tr.primaryActions()
	if len(acts) != 5 {
		t.Errorf("want 5 (cap), got %d", len(acts))
	}
	// no verb-named -> topped up with first clickables
	tr2 := mkBriefTree()
	tr2.Elems = []Element{
		{Ref: "r1", Role: "button", Name: "Alpha", Backend: 1},
		{Ref: "r2", Role: "link", Name: "Beta", Backend: 2},
	}
	acts2 := tr2.primaryActions()
	if len(acts2) != 2 {
		t.Errorf("want 2 (topped up), got %d", len(acts2))
	}
	// dedup: a button matching two verbs appears once
	tr3 := mkBriefTree()
	tr3.Elems = []Element{{Ref: "r1", Role: "button", Name: "Save and continue", Backend: 1}}
	acts3 := tr3.primaryActions()
	if len(acts3) != 1 {
		t.Errorf("dual-verb button should dedup to 1, got %d", len(acts3))
	}
}

func TestRenderBriefFormat(t *testing.T) {
	tr := &Tree{
		URL:    "https://x/login",
		Title:  "Login",
		Counts: map[string]int{"textbox": 2, "button": 1},
		Elems: []Element{
			{Ref: "r1", Role: "textbox", Name: "Username", Backend: 1},
			{Ref: "r2", Role: "textbox", Name: "Password", Backend: 2},
			{Ref: "r3", Role: "button", Name: "Sign in", Backend: 3},
		},
		Landmarks: []Element{{Role: "main"}, {Role: "banner"}},
	}
	out := tr.Render(LevelBrief)
	// Must carry the comprehension fields, not the raw ref dump.
	for _, want := range []string{"page: login form", "auth: anonymous", "actions:", "r3", "Sign in", "regions:", "interactive: 3"} {
		if !strings.Contains(out, want) {
			t.Errorf("brief missing %q, got:\n%s", want, out)
		}
	}
	// brief must NOT be a full ref dump (that's summary's job)
	if strings.Contains(out, "[r1] textbox") {
		t.Errorf("brief should not render raw ref-lines, got:\n%s", out)
	}
}
