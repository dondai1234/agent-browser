package browser

import (
	"testing"
	"time"
)

func TestShortURL(t *testing.T) {
	cases := map[string]string{
		"https://api.site.com/v1/cart?x=1": "/v1/cart?x=1",
		"http://x.io/y":                    "/y",
		"https://x.io":                     "/",
		"/already/short":                   "/already/short",
	}
	for in, want := range cases {
		if got := shortURL(in); got != want {
			t.Errorf("shortURL(%q) = %q, want %q", in, got, want)
		}
	}
	// truncation: a path longer than 60 chars is cut with an ellipsis.
	long := "/" + string(make([]byte, 80)) // 81 chars
	if got := shortURL(long); len(got) > 63 || got[len(got)-3:] != "..." {
		t.Errorf("long URL should truncate to <=63 with '...', got len=%d %q", len(got), got)
	}
}

func TestSummarizeNet(t *testing.T) {
	// one event -> just "path status"
	if got := summarizeNet([]netEvt{{url: "https://x.io/api/cart", status: 200}}); got != "/api/cart 200" {
		t.Errorf("1 evt: got %q", got)
	}
	// two events -> comma-joined, both shown
	if got := summarizeNet([]netEvt{
		{url: "https://x.io/a", status: 200},
		{url: "https://x.io/b", status: 500},
	}); got != "/a 200, /b 500" {
		t.Errorf("2 evts: got %q", got)
	}
	// five events -> count + last 3 (the action's own request is usually last)
	got := summarizeNet([]netEvt{
		{url: "https://x.io/1", status: 200},
		{url: "https://x.io/2", status: 200},
		{url: "https://x.io/3", status: 200},
		{url: "https://x.io/4", status: 200},
		{url: "https://x.io/5", status: 200},
	})
	if got != "5 requests (last: /3 200, /4 200, /5 200)" {
		t.Errorf("5 evts: got %q", got)
	}
}

// TestRecentNetLocked: only events at or after `since` are returned, so the
// verdict's net: summary reflects the current action window, not stale requests
// from before the action started.
func TestRecentNetLocked(t *testing.T) {
	s := &Session{}
	base := time.Now()
	tt := &tab{netEvents: []netEvt{
		{url: "/old", status: 200, ts: base.Add(-50 * time.Millisecond)}, // before window
		{url: "/a", status: 200, ts: base},                               // at start (inclusive)
		{url: "/b", status: 500, ts: base.Add(10 * time.Millisecond)},    // during
		{url: "/c", status: 200, ts: base.Add(20 * time.Millisecond)},    // during
	}}
	got := s.recentNetLocked(tt, base)
	if len(got) != 3 {
		t.Fatalf("want 3 events in window (>= base), got %d: %+v", len(got), got)
	}
	for _, e := range got {
		if e.url == "/old" {
			t.Errorf("stale /old event should be filtered out, got %+v", got)
		}
	}
	// since after all events -> nothing
	if got := s.recentNetLocked(tt, base.Add(time.Second)); len(got) != 0 {
		t.Errorf("future since should return nothing, got %d", len(got))
	}
	// nil tab -> nil, no panic
	if got := s.recentNetLocked(nil, base); got != nil {
		t.Errorf("nil tab should return nil, got %+v", got)
	}
}
