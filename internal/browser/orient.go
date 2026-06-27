package browser

import (
	"fmt"
	"time"
)

// defaultSettle is how long an act-and-see action waits for the DOM to settle
// before re-snapshotting, when the caller doesn't specify one. Short: the
// stable-poll (pullAXLocked) does the real waiting via content-signature
// convergence, so this just gives the page a head-start to start reacting.
const defaultSettle = 150 * time.Millisecond

// settleDur returns settleMs as a Duration, or the default if <= 0. Shared by
// the browser-package action paths (the mcpserver package has its own copy for
// the tool layer).
func settleDur(settleMs int) time.Duration {
	if settleMs > 0 {
		return time.Duration(settleMs) * time.Millisecond
	}
	return defaultSettle
}

// ScrollInfo returns a compact scroll-position string for the current tab
// ("fits viewport" / "at bottom (Npx)" / "Y/Npx (more below)") - the loop-closer
// for lazy-loaded lists. Exported so the nav/see tools can append it to an
// orientation without a separate tool call.
func (s *Session) ScrollInfo() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.scrollInfoLocked(s.curTabLocked())
}

// TabLine returns a "tab: t2 of 3 (label)" line for the current tab, or "" when
// there's only one tab (the common case - keeps the orientation lean). Exported
// for the nav/see orientation output.
func (s *Session) TabLine() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil || len(s.tabs) <= 1 {
		return ""
	}
	id := t.id
	if t.label != "" {
		id = fmt.Sprintf("%s (%q)", t.id, t.label)
	}
	return fmt.Sprintf("tab: %s of %d", id, len(s.tabs))
}
