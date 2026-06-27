package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v3/internal/browser"
	"github.com/dondai1234/agent-browser/v3/internal/snapshot"
)

func registerFind(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Role      string `json:"role,omitempty" jsonschema:"a11y role to filter by: button, link, textbox, checkbox, menuitem, option, tab, ..."`
		Text      string `json:"text,omitempty" jsonschema:"name substring to filter by (case-insensitive); or exact name if exact=true"`
		Exact     bool   `json:"exact,omitempty" jsonschema:"match the name exactly (case-insensitive) instead of substring"`
		Selector  string `json:"selector,omitempty" jsonschema:"CSS selector to query the DOM directly (e.g. \"div[role=widget]\", \".btn\") - reaches elements the a11y tree does NOT surface. Returns [css] lines with a sel= you can pass to js or act selector=. Mutually exclusive with role/text/exact."`
		Selectors bool   `json:"selectors,omitempty" jsonschema:"also compute a unique CSS selector for each a11y match (so you can target it in js), not just a ref. Costs one cheap resolve per result (capped 20). Use when you'll js-scrape the matched elements."`
		Limit     int    `json:"limit,omitempty" jsonschema:"cap the number of results (default 50 for a11y, 20 for selector)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "find",
		Description: "Locate elements to act on or scrape. Two modes: (1) a11y - role= and/or text= filter the cached snapshot -> elements with refs (pass to act ref=) AND names; reach into same-origin iframes (shown with 'in \"frame\"'). exact=true matches the name exactly (avoids 'more' matching '...more than...'). (2) selector=\"<css>\" - query the DOM directly -> [css] lines with a sel= you pass to js or act selector=; the escape hatch for elements the a11y tree drops (custom widgets, presentational nodes). selectors=true (a11y mode) also computes a CSS selector per match so you can target the same element in js - the bridge between the ref world and the selector world. Omit role and text to list every interactive element (can be large - prefer a filter).",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		if sel := strings.TrimSpace(a.Selector); sel != "" {
			matches, err := sess.FindSelector(sel)
			if err != nil {
				return errResult(err), nil, nil
			}
			return textResult(browser.RenderSelectorMatches(matches)), nil, nil
		}
		tree, err := sess.EnsureTree()
		if err != nil {
			return errResult(err), nil, nil
		}
		var els []snapshot.Element
		if a.Exact {
			els = tree.FindExact(a.Role, a.Text)
		} else {
			els = tree.Find(a.Role, a.Text)
		}
		limit := a.Limit
		if limit <= 0 {
			limit = 50
		}
		if len(els) > limit {
			els = els[:limit]
		}
		if !a.Selectors {
			out := snapshot.RenderElements(els)
			if len(els) == limit && a.Limit <= 0 {
				out += fmt.Sprintf("\n... (%d shown; pass a tighter role/text or limit= to see more)", limit)
			}
			return textResult(out), nil, nil
		}
		// selectors=true: annotate each a11y match with a unique CSS selector so
		// the agent can target it in `js`. Capped at 20 (one cheap resolve each).
		var b strings.Builder
		cap := len(els)
		if cap > 20 {
			cap = 20
		}
		for _, el := range els[:cap] {
			sel, err := sess.SelectorForRef(el.Ref)
			if err != nil {
				fmt.Fprintf(&b, "[%s] %s %q (no selector: %v)\n", el.Ref, el.Role, el.Name, err)
				continue
			}
			fmt.Fprintf(&b, "[%s] %s %q sel=%q\n", el.Ref, el.Role, el.Name, sel)
		}
		if len(els) > cap {
			fmt.Fprintf(&b, "... and %d more (raise limit=, or narrow role/text)\n", len(els)-cap)
		}
		return textResult(strings.TrimRight(b.String(), "\n")), nil, nil
	})
}
