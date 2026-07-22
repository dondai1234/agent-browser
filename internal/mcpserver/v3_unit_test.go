package mcpserver

import (
	"strings"
	"testing"

	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

func TestNavLevel(t *testing.T) {
	cases := map[string]snapshot.Level{
		"":        snapshot.LevelBrief,
		"brief":   snapshot.LevelBrief,
		"BRIEF":   snapshot.LevelBrief,
		"minimal": snapshot.LevelMinimal,
		"refs":    snapshot.LevelSummary,
		"summary": snapshot.LevelSummary,
		"full":    snapshot.LevelFull,
		"garbage": snapshot.LevelBrief, // unknown -> brief (never an empty level)
		"outline": snapshot.LevelBrief, // outline is a see level, not a nav level -> brief
	}
	for in, want := range cases {
		if got := navLevel(in); got != want {
			t.Errorf("navLevel(%q)=%q want %q", in, got, want)
		}
	}
}

func TestVerbLabel(t *testing.T) {
	cases := map[string]string{
		"click": "click", "fill": "fill", "select": "select",
		"hover": "hover", "upload": "upload", "press": "press",
		"": "click", "unknown": "click",
	}
	for in, want := range cases {
		if got := verbLabel(in); got != want {
			t.Errorf("verbLabel(%q)=%q want %q", in, got, want)
		}
	}
}

func TestDeltaOutNavigated(t *testing.T) {
	d := &snapshot.Delta{Navigated: true, NewURL: "https://x.example/inventory", Verdict: "navigated to https://x.example/inventory"}
	after := &snapshot.Tree{URL: "https://x.example/inventory", Title: "Inv"}
	out := deltaOut(d, after)
	if !strings.HasPrefix(out, "verdict: navigated to ") {
		t.Fatalf("deltaOut navigated should lead with verdict, got: %q", out)
	}
	if !strings.Contains(out, "inventory") {
		t.Fatalf("deltaOut navigated should append the new orientation, got: %q", out)
	}
}

func TestDeltaOutSoftFail(t *testing.T) {
	// after==nil + non-nav: a soft-fail (action fired but page wedged). The
	// verdict line is returned with no stale delta body that would contradict it.
	d := &snapshot.Delta{Verdict: "action fired; call see to refresh refs"}
	out := deltaOut(d, nil)
	if !strings.HasPrefix(out, "verdict: action fired") {
		t.Fatalf("soft-fail should be just the verdict line, got: %q", out)
	}
	if strings.Contains(out, "no changes") {
		t.Fatalf("soft-fail must not append a no-changes body, got: %q", out)
	}
}

func TestDeltaOutNilDelta(t *testing.T) {
	if out := deltaOut(nil, nil); out != "" {
		t.Errorf("nil delta should render empty, got %q", out)
	}
}
