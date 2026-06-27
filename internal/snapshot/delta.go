package snapshot

import (
	"fmt"
	"strings"
)

// Delta Added caps are role-aware, so important interactive elements (inputs,
// buttons, links) aren't hidden while bursty autocomplete/menu noise is. The
// full set always stays available via find (free, cached).
const (
	// MaxDeltaOptions caps bursty autocomplete/menu roles (option, menuitem,
	// treeitem) - the agent usually wants the top match; the rest are noise.
	MaxDeltaOptions = 5
	// MaxDeltaOther caps all other added roles (textbox, button, link, ...).
	// Generous, so real forms/modals/search results rarely get cut.
	MaxDeltaOther = 20
)

var deltaNoiseRoles = map[string]bool{
	"option": true, "menuitem": true, "menuitemcheckbox": true, "menuitemradio": true, "treeitem": true,
}

// Delta describes what changed between two Trees. Agent-POV win: after an
// action, return only this, not a fresh full snapshot. Over a multi-step flow
// this compounds (each action returns a tiny delta instead of re-dumping).
type Delta struct {
	Navigated      bool // URL changed → refs fully reset; caller returns new minimal.
	NewURL         string
	NewTitle       string
	Added          []Element // present after, not before (carry NEW refs, usable next)
	Removed        []Element // present before, not after (OLD refs, now invalid)
	Changed        []Element // same Backend, name/value differs (NEW refs, usable)
	AddedSignals   []Element // live-region/modal nodes that appeared (toast/dialog opened)
	RemovedSignals []Element // live-region/modal nodes that vanished (dialog closed)
	ContentChanged bool      // URL-stable content/order shift (sort/filter/SPA re-render) detected via Tree.Signature; the backend-id element diff misses a pure reorder
	Verdict        string    // one-line semantic outcome ("navigated to ...", "dialog opened: ...", "no visible effect"); set by the browser layer via InferVerdict + challenge/network augmentation
}

// Diff compares two trees. If the URL changed (navigation), Navigated=true and
// the caller returns the new minimal orientation (all refs reset). Otherwise
// element-level added/removed/changed are reported, keyed by Backend id (which
// is stable across non-navigation DOM updates).
func Diff(before, after *Tree) *Delta {
	d := &Delta{NewURL: after.URL, NewTitle: after.Title}
	if before == nil || before.URL != after.URL {
		d.Navigated = true
		return d
	}
	beforeIdx := make(map[int64]Element, len(before.Elems))
	for _, e := range before.Elems {
		if e.Backend != 0 {
			beforeIdx[e.Backend] = e
		}
	}
	afterSeen := make(map[int64]struct{}, len(after.Elems))
	for _, e := range after.Elems {
		if e.Backend == 0 {
			continue
		}
		afterSeen[e.Backend] = struct{}{}
		if prev, ok := beforeIdx[e.Backend]; ok {
			if elChanged(prev, e) {
				d.Changed = append(d.Changed, e)
			}
		} else {
			d.Added = append(d.Added, e)
		}
	}
	for _, e := range before.Elems {
		if e.Backend == 0 {
			continue
		}
		if _, ok := afterSeen[e.Backend]; !ok {
			d.Removed = append(d.Removed, e)
		}
	}
	// Diff outcome-signal nodes (alert/status/dialog) the same way, so the
	// verdict can detect a toast appearing or a modal opening/closing - things
	// the element delta misses because those roles aren't interactive.
	beforeSig := make(map[int64]Element, len(before.Signals))
	for _, e := range before.Signals {
		if e.Backend != 0 {
			beforeSig[e.Backend] = e
		}
	}
	afterSigSeen := make(map[int64]struct{}, len(after.Signals))
	for _, e := range after.Signals {
		if e.Backend == 0 {
			continue
		}
		afterSigSeen[e.Backend] = struct{}{}
		if _, ok := beforeSig[e.Backend]; !ok {
			d.AddedSignals = append(d.AddedSignals, e)
		}
	}
	for _, e := range before.Signals {
		if e.Backend == 0 {
			continue
		}
		if _, ok := afterSigSeen[e.Backend]; !ok {
			d.RemovedSignals = append(d.RemovedSignals, e)
		}
	}
	// URL-stable reorder/content shift (sort, filter, SPA re-render): the same
	// backend-id set is present, so the element diff sees no add/remove/changed,
	// but the ordered content signature changed. Surface it so the verdict does
	// not read "no visible effect" when the page actually re-sorted.
	if before.Signature != 0 && after.Signature != 0 && before.Signature != after.Signature {
		d.ContentChanged = true
	}
	return d
}

func elChanged(a, b Element) bool {
	return a.Name != b.Name || a.Value != b.Value
}

// HasChanges reports whether any element-level change occurred (ignored when Navigated).
func (d *Delta) HasChanges() bool {
	return len(d.Added) > 0 || len(d.Removed) > 0 || len(d.Changed) > 0
}

// Summary is a one-line log line for the delta.
func (d *Delta) Summary() string {
	return fmt.Sprintf("navigated=%v added=%d removed=%d changed=%d", d.Navigated, len(d.Added), len(d.Removed), len(d.Changed))
}

// Render returns a dense delta. When Navigated, just new url+title (caller
// appends the new minimal orientation). Added/Changed use NEW refs (usable);
// Removed shows role+name only (the old ref is invalid, so we drop it).
func (d *Delta) Render() string {
	if d.Navigated {
		s := "navigated: " + d.NewURL
		if d.NewTitle != "" {
			s += fmt.Sprintf(" %q", d.NewTitle)
		}
		return s
	}
	if !d.HasChanges() {
		if d.ContentChanged {
			return "page updated (URL stable; e.g. sort/filter/SPA re-render) - call see to refresh refs"
		}
		return "no changes (no visible effect; call see to refresh refs if you expected one)"
	}
	var b strings.Builder
	// Role-aware cap: bursty autocomplete/menu noise (option/menuitem/treeitem)
	// is capped low; other roles (inputs, buttons, links) get a generous cap so
	// real surfaces aren't hidden. The full set is always available via find.
	noiseShown, otherShown, noiseHidden, otherHidden := 0, 0, 0, 0
	for _, e := range d.Added {
		if deltaNoiseRoles[e.Role] {
			if noiseShown < MaxDeltaOptions {
				fmt.Fprintf(&b, "+ %s\n", formatElement(e))
				noiseShown++
			} else {
				noiseHidden++
			}
		} else {
			if otherShown < MaxDeltaOther {
				fmt.Fprintf(&b, "+ %s\n", formatElement(e))
				otherShown++
			} else {
				otherHidden++
			}
		}
	}
	if noiseHidden > 0 || otherHidden > 0 {
		var parts []string
		if noiseHidden > 0 {
			parts = append(parts, fmt.Sprintf("%d more options (find role=option)", noiseHidden))
		}
		if otherHidden > 0 {
			parts = append(parts, fmt.Sprintf("%d more elements (use find)", otherHidden))
		}
		fmt.Fprintf(&b, "... and %s added\n", strings.Join(parts, " + "))
	}
	// Removed carries the OLD ref so the agent can map its stale plan (the ref
	// is now invalid, but naming it makes the breakage explicit).
	for _, e := range d.Removed {
		fmt.Fprintf(&b, "- [%s] %s %q (gone)\n", e.Ref, e.Role, e.Name)
	}
	for _, e := range d.Changed {
		fmt.Fprintf(&b, "~ %s\n", formatElement(e))
	}
	return strings.TrimRight(b.String(), "\n")
}

// InferVerdict produces a one-line semantic outcome from the delta: what the
// action *meant*, not just what changed mechanically. Priority: navigation >
// modal opened (with any toast text) > toast/status > modal closed > generic
// change counts > nothing. The browser layer calls this, then overrides with a
// CHALLENGE: line if the after-page is a bot-check interstitial (the action
// didn't achieve its intent - the challenge blocked it).
//
// This is the v2 foundation: every act-and-see action returns a verdict, so
// the agent knows "did it work" without re-seeing. Richer verdicts (page type,
// auth state, "logged in") are layered on by brief/act in later steps.
func (d *Delta) InferVerdict() string {
	if d.Navigated {
		s := "navigated to " + d.NewURL
		if d.NewTitle != "" {
			s += fmt.Sprintf(" %q", d.NewTitle)
		}
		return s
	}
	// A modal/dialog opening is the strongest non-navigation signal - report it
	// first, and if a status/alert appeared alongside (common: a dialog + a
	// toast), fold the toast text in.
	for _, e := range d.AddedSignals {
		if e.Role == "alertdialog" || e.Role == "dialog" {
			name := e.Name
			if name == "" {
				name = e.Role
			}
			for _, a := range d.AddedSignals {
				if (a.Role == "alert" || a.Role == "status") && a.Name != "" {
					return fmt.Sprintf("dialog opened: %s; %s", name, a.Name)
				}
			}
			return "dialog opened: " + name
		}
	}
	for _, e := range d.AddedSignals {
		if (e.Role == "alert" || e.Role == "status") && e.Name != "" {
			return "status: " + e.Name
		}
	}
	for _, e := range d.RemovedSignals {
		if e.Role == "alertdialog" || e.Role == "dialog" {
			return "dialog closed"
		}
	}
	if d.HasChanges() {
		return fmt.Sprintf("changed: +%d -%d ~%d", len(d.Added), len(d.Removed), len(d.Changed))
	}
	if d.ContentChanged {
		return "page updated (URL stable; e.g. sort/filter/SPA re-render) - call see to refresh refs"
	}
	return "no visible effect"
}
