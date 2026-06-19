package browser

import (
	"errors"
	"fmt"

	"github.com/chromedp/chromedp"

	"github.com/dondai1234/agent-browser/internal/snapshot"
)

// TabInfo is a tab's summary for the tabs tool.
type TabInfo struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	URL     string `json:"url"`
	Title   string `json:"title"`
	Current bool   `json:"current"`
}

// Tabs lists all tabs.
func (s *Session) Tabs() []TabInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]TabInfo, 0, len(s.tabs))
	for i, t := range s.tabs {
		info := TabInfo{ID: t.id, Label: t.label, Current: i == s.cur}
		if t.tree != nil {
			info.URL = t.tree.URL
			info.Title = t.tree.Title
		}
		out = append(out, info)
	}
	return out
}

// NewTab opens a new tab (navigating to url if non-empty), makes it current,
// and returns its tree (nil if url is empty).
func (s *Session) NewTab(url string) (*snapshot.Tree, error) {
	if url != "" {
		if _, err := ValidateURL(url, s.AllowInsecureSchemes); err != nil {
			return nil, err
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	newCtx, cancel := chromedp.NewContext(s.browserCtx)
	s.counter++
	t := &tab{id: fmt.Sprintf("t%d", s.counter), ctx: newCtx, cancel: cancel}
	s.tabs = append(s.tabs, t)
	s.cur = len(s.tabs) - 1
	s.setupTabListenersLocked(t)
	if url != "" {
		if err := chromedp.Run(t.ctx,
			chromedp.Navigate(url),
			chromedp.WaitReady("body", chromedp.ByQuery),
		); err != nil {
			return nil, fmt.Errorf("navigate new tab: %w", err)
		}
		if err := s.buildTreeLocked(); err != nil {
			return nil, err
		}
	}
	return s.curTabLocked().tree, nil
}

// SwitchTab makes the tab with the given id (or label) current.
func (s *Session) SwitchTab(idOrLabel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, t := range s.tabs {
		if t.id == idOrLabel || t.label == idOrLabel {
			s.cur = i
			return nil
		}
	}
	return fmt.Errorf("tab %q not found", idOrLabel)
}

// CloseTab closes the tab with the given id (or label). The last tab cannot be
// closed (the session must always have one).
func (s *Session) CloseTab(idOrLabel string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.tabs) <= 1 {
		return errors.New("cannot close the last tab")
	}
	idx := -1
	for i, t := range s.tabs {
		if t.id == idOrLabel || t.label == idOrLabel {
			idx = i
			break
		}
	}
	if idx == -1 {
		return fmt.Errorf("tab %q not found", idOrLabel)
	}
	t := s.tabs[idx]
	if t.cancel != nil {
		t.cancel()
	}
	s.tabs = append(s.tabs[:idx], s.tabs[idx+1:]...)
	if s.cur >= len(s.tabs) {
		s.cur = len(s.tabs) - 1
	}
	if s.cur < 0 {
		s.cur = 0
	}
	return nil
}

// SetTabLabel sets a label on the current tab (optional, for memorable names).
func (s *Session) SetTabLabel(label string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t := s.curTabLocked()
	if t == nil {
		return errors.New("no tab")
	}
	t.label = label
	return nil
}
