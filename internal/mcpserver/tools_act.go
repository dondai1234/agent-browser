package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v3/internal/browser"
	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

func registerAct(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Intent    string            `json:"intent,omitempty" jsonschema:"the control's name/label to act on, e.g. \"Sign in\", \"Username\", \"Add to cart\". Resolved on the cached snapshot (local heuristics, no LLM); act does the right thing for its role. Mutually exclusive with ref/selector."`
		Ref       string            `json:"ref,omitempty" jsonschema:"a stable ref from see/find (e.g. r12) to act on precisely. Mutually exclusive with intent/selector."`
		Selector  string            `json:"selector,omitempty" jsonschema:"CSS selector to act on directly (e.g. \".btn-checkout\", \"div[role=widget]\") - the escape hatch for elements the a11y tree does NOT surface. Mutually exclusive with intent/ref."`
		Value     string            `json:"value,omitempty" jsonschema:"for an input/dropdown target: the value to fill or the option to select (by value or visible text). Ignored for click/hover/press/upload."`
		Role      string            `json:"role,omitempty" jsonschema:"constrain intent matches to a role (button, link, textbox, ...) to disambiguate"`
		Nth       int               `json:"nth,omitempty" jsonschema:"disambiguate ambiguous intent matches: positive = 1-based from the top (nth=1 = best), negative = from the end (nth=-1 = last)"`
		Hover     bool              `json:"hover,omitempty" jsonschema:"hover the target (fires CSS :hover + mouseover) instead of clicking - for hover-only menus/tooltips"`
		Key       string            `json:"key,omitempty" jsonschema:"press a key (named key: Enter, Escape, Tab, Backspace, Delete, ArrowUp/Down/Left/Right, Home, End, PageUp, PageDown, Space; or a single char). Acts on the focused element by default; pass ref or intent to focus a target first (Enter on a chosen input submits its form). Mutually exclusive with the click/fill/select/hover/upload modes."`
		Modifiers string            `json:"modifiers,omitempty" jsonschema:"key modifiers, '+'-joined: ctrl, shift, alt, meta (e.g. \"ctrl\", \"ctrl+shift\"); for key= only"`
		Files     []string          `json:"files,omitempty" jsonschema:"file paths to upload; target a file input by ref/selector/intent, or omit to auto-find the first input[type=file]. Mutually exclusive with the other modes."`
		WaitURL   string            `json:"waitUrl,omitempty" jsonschema:"after the action, wait for the URL to contain this before re-snapshotting (e.g. \"/dashboard\") - fuses act+wait so the delta reflects the landed page"`
		WaitText  string            `json:"waitText,omitempty" jsonschema:"after the action, wait for this text to appear in the body before re-snapshotting"`
		WaitGone  string            `json:"waitGone,omitempty" jsonschema:"after the action, wait for this text to DISAPPEAR from the body before re-snapshotting (e.g. a spinner clearing)"`
		WaitMs    int               `json:"waitMs,omitempty" jsonschema:"wait budget in ms (default 10000)"`
		SettleMs  int               `json:"settleMs,omitempty" jsonschema:"ms to let the DOM settle before the wait/re-snapshot (default 150; raise for slow SPAs)"`
		Fields    map[string]string `json:"fields,omitempty" jsonschema:"BATCH FORM FILL: a map of field labels to values, e.g. {\"Username\":\"john\",\"Password\":\"hunter2\",\"Remember me\":\"true\",\"Country\":\"United States\"}. Resolves each label to a form field (a11y name + DOM fallback), detects the type, and does the right action: fill for text inputs, select for dropdowns, toggle for checkboxes (value true/false/1/0/yes/no), click for radios (value = option label), set for sliders, upload for file inputs (value = comma-separated paths). One call, one re-snapshot. Mutually exclusive with intent/ref/selector/key/files/hover."`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "act",
		Description: "Do ONE thing to the page. Name a control (intent) or give a ref/selector; act performs the right action for it and returns a VERDICT + DELTA (you usually do NOT re-see after - the verdict tells you what happened). Modes: (1) click/fill/select - DEFAULT: act clicks buttons/links, fills text inputs (pass value=), selects dropdowns (pass value=, by value or visible text) - the verb is picked from the element's role + whether value is set. (2) hover=true - hover the target (fires CSS :hover, reveals hover menus). (3) key= - press a key on the focused element (Enter submits, Escape closes, Tab moves focus); pass ref/intent to target it. (4) files=[..] - upload (target by ref/selector/intent, or auto-find the file input). (5) fields={..} - BATCH FORM FILL: pass a map of field labels to values (e.g. {\"Username\":\"john\",\"Remember me\":\"true\",\"Country\":\"US\"}) and act resolves each label to a form field, detects the type (text/checkbox/radio/select/slider/file), and does the right action in one call - then re-snapshots once and reports validation errors. This is the go-to for filling a whole form: one call instead of N. Target resolution: intent uses the a11y name first, then a DOM name/id/placeholder/aria-label fallback for poorly-labeled inputs; ref is a stable see/find ref; selector is the CSS escape hatch. Ambiguous intent matches return ranked candidates (disambiguate with nth/role) - act never guesses. Optional waitUrl/waitText/waitGone fuses a wait into the action (the re-snapshot happens after the wait). Verdicts: navigated to / dialog opened / status / changed / page updated / no visible effect / CHALLENGE; non-nav actions also fold in the XHR/Fetch responses that fired (net:).",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		// One action category must be set (target + a mode, or a key, or files, or fields).
		hasTarget := strings.TrimSpace(a.Intent) != "" || strings.TrimSpace(a.Ref) != "" || strings.TrimSpace(a.Selector) != ""
		if a.Key == "" && len(a.Files) == 0 && len(a.Fields) == 0 && !hasTarget {
			return errResult(errors.New("act needs a target (intent, ref, or selector) OR key= (press a key) OR files= (upload) OR fields= (batch form fill)")), nil, nil
		}
		if a.Key != "" && (a.Value != "" || a.Hover || len(a.Files) > 0 || len(a.Fields) > 0) {
			return errResult(errors.New("key= is mutually exclusive with value/hover/files/fields (press a key OR click/fill/select/hover/upload/form-fill, not both)")), nil, nil
		}
		if len(a.Files) > 0 && (a.Value != "" || a.Hover || a.Key != "" || len(a.Fields) > 0) {
			return errResult(errors.New("files= is mutually exclusive with value/hover/key/fields (upload OR click/fill/select/hover/press/form-fill, not both)")), nil, nil
		}
		if len(a.Fields) > 0 && (hasTarget || a.Key != "" || len(a.Files) > 0 || a.Hover) {
			return errResult(errors.New("fields= is mutually exclusive with intent/ref/selector/key/files/hover (form fill OR a single action, not both)")), nil, nil
		}
		if a.Key != "" && strings.TrimSpace(a.Selector) != "" {
			return errResult(errors.New("key= targets via ref or intent (to focus the element), not selector - use ref or intent to target a key press")), nil, nil
		}

		res, err := sess.Perform(browser.PerformArgs{
			Intent: a.Intent, Ref: a.Ref, Selector: a.Selector, Value: a.Value, Role: a.Role, Nth: a.Nth,
			Hover: a.Hover, Key: a.Key, Modifiers: a.Modifiers, Files: a.Files, Fields: a.Fields,
			WaitURL: a.WaitURL, WaitText: a.WaitText, WaitGone: a.WaitGone, WaitMs: a.WaitMs, SettleMs: a.SettleMs,
		})
		if err != nil {
			msg := err.Error()
			if res != nil {
				if res.CandidatesText != "" {
					msg += "\nmatches:\n" + res.CandidatesText
				} else if len(res.Candidates) > 0 {
					limit := len(res.Candidates)
					if limit > 8 {
						limit = 8
					}
					msg += "\nmatches:\n" + snapshot.RenderElements(res.Candidates[:limit])
					if len(res.Candidates) > 8 {
						msg += fmt.Sprintf("\n... and %d more (pass a more specific name, or role/nth to pick)", len(res.Candidates)-8)
					}
				}
			}
			return errResult(errors.New(msg)), nil, nil
		}
		if res.FormFill != nil {
			var b strings.Builder
			ff := res.FormFill
			fmt.Fprintf(&b, "form fill: %d filled", ff.Filled)
			if ff.Skipped > 0 {
				fmt.Fprintf(&b, ", %d skipped", ff.Skipped)
			}
			if len(ff.Errors) > 0 {
				fmt.Fprintf(&b, ", %d errors", len(ff.Errors))
				for _, e := range ff.Errors {
					fmt.Fprintf(&b, "\n  error: %s", e)
				}
			}
			if len(ff.Valid) > 0 {
				fmt.Fprintf(&b, "\nvalidation errors:")
				for _, e := range ff.Valid {
					fmt.Fprintf(&b, "\n  %s", e)
				}
			}
			b.WriteString("\n")
			b.WriteString(deltaOut(res.Delta, res.After))
			return textResult(b.String()), nil, nil
		}
		var b strings.Builder
		switch {
		case res.Resolved != nil:
			fmt.Fprintf(&b, "act %s %q -> [%s] %s %q (%s)\n", verbLabel(res.Verb), a.Intent, res.Resolved.Ref, res.Resolved.Role, res.Resolved.Name, res.Verb)
		case res.Verb == "press":
			fmt.Fprintf(&b, "press %s @%s\n", a.Key, res.Target)
		case res.Verb == "upload":
			fmt.Fprintf(&b, "upload %d file(s)\n", len(a.Files))
		case res.Target != "":
			fmt.Fprintf(&b, "%s %s (%s)\n", verbLabel(res.Verb), res.Target, res.Verb)
		default:
			fmt.Fprintf(&b, "%s\n", res.Verb)
		}
		b.WriteString(deltaOut(res.Delta, res.After))
		return textResult(b.String()), nil, nil
	})
}

// verbLabel maps a verb to the action-log prefix for the result header.
func verbLabel(verb string) string {
	switch verb {
	case "fill":
		return "fill"
	case "select":
		return "select"
	case "hover":
		return "hover"
	case "upload":
		return "upload"
	case "press":
		return "press"
	default:
		return "click"
	}
}

// deltaOut renders an act-and-see result: the verdict first, then the delta
// detail; on navigation the verdict already names the url, so we append the new
// page orientation (the refs the agent needs next).
func deltaOut(delta *snapshot.Delta, after *snapshot.Tree) string {
	var out string
	if delta != nil && delta.Verdict != "" {
		conf := ""
		if delta.Confidence != "" {
			conf = "[" + delta.Confidence + "] "
		}
		out = "verdict: " + conf + delta.Verdict + "\n"
	}
	if delta == nil {
		return out
	}
	if delta.Navigated {
		if after != nil {
			out += after.Render(snapshot.LevelMinimal)
		}
		return out
	}
	if after == nil {
		// Soft-fail: the action fired but the page wouldn't re-snapshot in one
		// pull (navigating/wedged); the verdict already says to call see.
		return out
	}
	out += delta.Render()
	return out
}
