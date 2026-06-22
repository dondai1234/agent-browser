package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
	"github.com/dondai1234/agent-browser/v2/internal/snapshot"
)

func registerAct(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Intent   string `json:"intent" jsonschema:"the name/label of the control to act on, e.g. \"Sign in\", \"Username\", \"Add to cart\". Resolved on the cached snapshot (local heuristics, no LLM); the right action for the role is performed (click buttons/links, fill inputs, select combobox options) and a verdict + delta are returned."`
		Value    string `json:"value,omitempty" jsonschema:"for an input/combobox target: the value to fill or the option to select; ignored for click targets"`
		Role     string `json:"role,omitempty" jsonschema:"constrain the match to a role (button, link, textbox, ...) to disambiguate"`
		Nth      int    `json:"nth,omitempty" jsonschema:"1-based pick from the ranked matches, to disambiguate when several controls share a name"`
		SettleMs int    `json:"settleMs,omitempty" jsonschema:"ms to let the DOM settle before re-snapshot (default 150; raise for slow SPAs)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "act",
		Description: "Act by intent: pass a control's name (e.g. \"Sign in\", \"Username\", \"Add to cart\"); the tool resolves it on the cached snapshot with local heuristics (no LLM) and performs the default action for its role - click buttons/links, fill textbox/searchbox (pass value), select combobox (pass value) - returning a verdict + delta. Collapses find + click/fill + see into one call. If several controls match it returns the ranked candidates; disambiguate with nth or role, or use click/fill by ref. Two-tier matching: first the a11y name (label/placeholder/aria-label), then, on no match, the DOM name/id/placeholder/title/aria-label - so poorly-labeled inputs (only a name=/id= you know from HTML or extract form) are still reachable by intent. The response names the resolved ref + verb so you stay in control.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(a.Intent) == "" {
			return errResult(errors.New("intent required: the name/label of the control to act on (e.g. \"Sign in\", \"Username\", \"Add to cart\")")), nil, nil
		}
		res, err := sess.Act(a.Intent, a.Value, a.Role, a.Nth, settleDur(a.SettleMs))
		if err != nil {
			// Ambiguous (candidates present) or no-match or fillable-needs-value:
			// surface the message, and append the candidate list when ambiguous so
			// the agent can disambiguate without a separate find.
			msg := err.Error()
			if res != nil && res.CandidatesText != "" {
				msg += "\nmatches:\n" + res.CandidatesText
			} else if res != nil && len(res.Candidates) > 0 {
				limit := len(res.Candidates)
				if limit > 8 {
					limit = 8
				}
				msg += "\nmatches:\n" + snapshot.RenderElements(res.Candidates[:limit])
				if len(res.Candidates) > 8 {
					msg += fmt.Sprintf("\n... and %d more (pass a more specific name, or role/nth to pick)", len(res.Candidates)-8)
				}
			}
			return errResult(errors.New(msg)), nil, nil
		}
		// Acted: name the resolved ref + verb, then the verdict + delta (same
		// format as click/fill, so the agent's parsing is uniform).
		var b strings.Builder
		fmt.Fprintf(&b, "act %q -> [%s] %s %q (%s)\n", a.Intent, res.Resolved.Ref, res.Resolved.Role, res.Resolved.Name, res.Verb)
		b.WriteString(deltaOut(res.Delta, res.After))
		return textResult(b.String()), nil, nil
	})
}
