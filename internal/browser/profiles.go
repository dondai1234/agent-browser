package browser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// ProfileInfo is one named profile's summary for the list action.
type ProfileInfo struct {
	Name   string `json:"name"`
	Active bool   `json:"active"`
	Path   string `json:"path"`
}

// StorageState is the export/import format for a profile's browser state
// (cookies + the current origin's localStorage).
type StorageState struct {
	Cookies      []StorageCookie   `json:"cookies"`
	LocalStorage map[string]string `json:"localStorage"`
}

// StorageCookie is one cookie in the export/import format.
type StorageCookie struct {
	Name     string `json:"name"`
	Value    string `json:"value"`
	Domain   string `json:"domain"`
	Path     string `json:"path"`
	Secure   bool   `json:"secure"`
	HTTPOnly bool   `json:"httpOnly"`
	SameSite string `json:"sameSite,omitempty"`
}

// profilesRoot returns the directory for named profiles, creating it if needed.
// Default: <os config dir>/goshawk-profiles/. Called once in New.
func profilesRoot() (string, error) {
	d, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	root := filepath.Join(d, "goshawk-profiles")
	if err := os.MkdirAll(root, 0o700); err != nil {
		return "", fmt.Errorf("create profiles dir: %w", err)
	}
	return root, nil
}

// ListProfiles lists all named profiles. Caller must hold s.mu.
func (s *Session) ListProfiles() []ProfileInfo {
	if s.profilesDir == "" {
		return nil
	}
	entries, err := os.ReadDir(s.profilesDir)
	if err != nil {
		return nil
	}
	var out []ProfileInfo
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			continue
		}
		out = append(out, ProfileInfo{
			Name:   name,
			Active: name == s.activeProfile,
			Path:   filepath.Join(s.profilesDir, name),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// CreateProfile creates a new named profile directory. Returns an error if it
// already exists. Caller must hold s.mu.
func (s *Session) CreateProfile(name string) error {
	name = sanitizeProfileName(name)
	if name == "" {
		return errors.New("profile name required (alphanumeric, dash, underscore)")
	}
	dir := filepath.Join(s.profilesDir, name)
	if _, err := os.Stat(dir); err == nil {
		return fmt.Errorf("profile %q already exists", name)
	}
	return os.MkdirAll(dir, 0o700)
}

// SwitchProfile switches to a named profile. Tears down the current browser
// (if any) and sets the user-data-dir to the profile's directory. The browser
// relaunches lazily on the next navigate. Page state from the old profile is
// lost. Caller must hold s.mu.
func (s *Session) SwitchProfile(name string) error {
	name = sanitizeProfileName(name)
	if name == "" {
		return errors.New("profile name required")
	}
	dir := filepath.Join(s.profilesDir, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("profile %q not found (use session mode=profile action=create first)", name)
	}
	s.teardownBrowserLocked()
	s.dead = nil
	s.persistFallback = false
	s.activeProfile = name
	s.cfg.UserDataDir = dir
	s.history = nil
	s.histStep = 0
	return nil
}

// DeleteProfile removes a named profile directory. If it's the active profile,
// tears down the browser first and reverts to the default (temp) profile.
// Caller must hold s.mu.
func (s *Session) DeleteProfile(name string) error {
	name = sanitizeProfileName(name)
	if name == "" {
		return errors.New("profile name required")
	}
	dir := filepath.Join(s.profilesDir, name)
	if _, err := os.Stat(dir); err != nil {
		return fmt.Errorf("profile %q not found", name)
	}
	if name == s.activeProfile {
		s.teardownBrowserLocked()
		s.dead = nil
		s.persistFallback = false
		s.activeProfile = ""
		s.cfg.UserDataDir = ""
		s.history = nil
		s.histStep = 0
	}
	return os.RemoveAll(dir)
}

// CurrentProfile returns the active profile name ("" = default/temp profile).
func (s *Session) CurrentProfile() string {
	return s.activeProfile
}

// ExportState exports the current page's cookies + the current origin's
// localStorage as a JSON string. The browser must be running with a page
// loaded. Caller must hold s.mu.
func (s *Session) ExportState() (string, error) {
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return "", ErrNoSnapshot
	}
	var state StorageState
	// 1. Dump cookies via CDP (returns cookies for the current page + frames).
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		cookies, err := network.GetCookies().Do(ctx)
		if err != nil {
			return fmt.Errorf("get cookies: %w", err)
		}
		for _, c := range cookies {
			sc := StorageCookie{
				Name:     c.Name,
				Value:    c.Value,
				Domain:   c.Domain,
				Path:     c.Path,
				Secure:   c.Secure,
				HTTPOnly: c.HTTPOnly,
				SameSite: string(c.SameSite),
			}
			state.Cookies = append(state.Cookies, sc)
		}
		return nil
	})); err != nil {
		return "", fmt.Errorf("export cookies: %w", err)
	}
	// 2. Dump the current origin's localStorage.
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		res, exc, e := runtime.Evaluate(`(function(){ try { var out={}; for(var i=0;i<localStorage.length;i++){var k=localStorage.key(i);out[k]=localStorage.getItem(k);} return JSON.stringify(out); } catch(e){ return '{}'; } })()`).WithReturnByValue(true).Do(ctx)
		if e != nil {
			return e
		}
		if exc != nil {
			return fmt.Errorf("dump localStorage: %s", exc.Text)
		}
		if res != nil && len(res.Value) > 0 {
			_ = json.Unmarshal(res.Value, &state.LocalStorage)
		}
		return nil
	})); err != nil {
		return "", fmt.Errorf("export localStorage: %w", err)
	}
	if state.LocalStorage == nil {
		state.LocalStorage = map[string]string{}
	}
	out, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal state: %w", err)
	}
	return string(out), nil
}

// ImportState imports cookies + localStorage from a JSON string. Clears
// existing cookies first (clean restore, not a merge). The browser must be
// running with a page loaded. Caller must hold s.mu.
func (s *Session) ImportState(data string) error {
	t := s.curTabLocked()
	if t == nil || t.tree == nil {
		return ErrNoSnapshot
	}
	var state StorageState
	if err := json.Unmarshal([]byte(data), &state); err != nil {
		return fmt.Errorf("parse import data: %w", err)
	}
	// 1. Clear all cookies, then set the imported cookies.
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		if err := network.ClearBrowserCookies().Do(ctx); err != nil {
			return fmt.Errorf("clear cookies: %w", err)
		}
		for _, c := range state.Cookies {
			sc := network.SetCookie(c.Name, c.Value).
				WithDomain(c.Domain).
				WithPath(c.Path)
			if c.Secure {
				sc = sc.WithSecure(true)
			}
			if c.HTTPOnly {
				sc = sc.WithHTTPOnly(true)
			}
			switch strings.ToLower(c.SameSite) {
			case "strict":
				sc = sc.WithSameSite(network.CookieSameSiteStrict)
			case "lax":
				sc = sc.WithSameSite(network.CookieSameSiteLax)
			case "none":
				sc = sc.WithSameSite(network.CookieSameSiteNone)
			}
			if err := sc.Do(ctx); err != nil {
				return fmt.Errorf("set cookie %q: %w", c.Name, err)
			}
		}
		return nil
	})); err != nil {
		return fmt.Errorf("import cookies: %w", err)
	}
	// 2. Set localStorage items on the current origin.
	if len(state.LocalStorage) > 0 {
		if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			for k, v := range state.LocalStorage {
				kJSON, _ := json.Marshal(k)
				vJSON, _ := json.Marshal(v)
				_, _, e := runtime.Evaluate(fmt.Sprintf(`try{localStorage.setItem(%s,%s)}catch(e){}`, string(kJSON), string(vJSON))).Do(ctx)
				if e != nil {
					return e
				}
			}
			return nil
		})); err != nil {
			return fmt.Errorf("import localStorage: %w", err)
		}
	}
	return nil
}

// sanitizeProfileName strips path separators and dangerous characters from a
// profile name, keeping it to a safe filename subset.
func sanitizeProfileName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			b.WriteRune(r)
		}
	}
	return b.String()
}
