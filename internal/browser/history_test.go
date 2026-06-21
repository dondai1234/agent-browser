package browser

import (
	"strconv"
	"strings"
	"testing"
)

func TestHistoryEmpty(t *testing.T) {
	s := &Session{}
	if got := s.History(0, false); !strings.Contains(got, "empty") {
		t.Errorf("fresh session history should be empty, got %q", got)
	}
}

func TestHistoryCapAndMonotonicStep(t *testing.T) {
	s := &Session{}
	for i := 0; i < 250; i++ {
		s.recordHistoryLocked("a"+strconv.Itoa(i), "verdict", "/u")
	}
	// only the last maxHistory entries are kept...
	if len(s.history) != maxHistory {
		t.Errorf("history should be capped at %d, got %d", maxHistory, len(s.history))
	}
	// ...but the step counter keeps incrementing (monotonic across trims) so the
	// agent can reference "since step N" stably.
	if s.histStep != 250 {
		t.Errorf("histStep should be 250 (monotonic), got %d", s.histStep)
	}
	// the oldest kept entry is step 51 (250-200+1), not step 1.
	if s.history[0].Step != 51 {
		t.Errorf("first kept entry should be step 51, got %d", s.history[0].Step)
	}
}

func TestHistoryLastFilter(t *testing.T) {
	s := &Session{}
	for i := 0; i < 5; i++ {
		s.recordHistoryLocked("a"+strconv.Itoa(i), "verdict", "/u")
	}
	out := s.History(2, false)
	if !strings.Contains(out, "showing last 2") {
		t.Errorf("last=2 should note 'showing last 2', got %q", out)
	}
	if !strings.Contains(out, "a4") || !strings.Contains(out, "a3") {
		t.Errorf("last=2 should include the two most recent (a3, a4), got %q", out)
	}
	if strings.Contains(out, "a0") {
		t.Errorf("last=2 should NOT include a0, got %q", out)
	}
}

func TestHistoryErrorsOnly(t *testing.T) {
	s := &Session{}
	s.recordHistoryLocked("a1", "changed: +1", "/u")
	s.recordHistoryLocked("a2", "CHALLENGE: Cloudflare wall", "/u")
	s.recordHistoryLocked("a3", "navigated to /x", "/x")
	out := s.History(0, true)
	if !strings.Contains(out, "errors only") {
		t.Errorf("errors=true should note 'errors only', got %q", out)
	}
	if !strings.Contains(out, "CHALLENGE") {
		t.Errorf("errors=true should include the blocked entry, got %q", out)
	}
	// non-error entries must not leak into the errors-only view
	if strings.Contains(out, "a1") || strings.Contains(out, "a3") {
		t.Errorf("errors-only should exclude non-blocked entries, got %q", out)
	}
}
