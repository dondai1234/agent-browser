package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v3/internal/browser"
	"github.com/dondai1234/agent-browser/v3/internal/snapshot"
)

func registerSee(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Level    string `json:"level,omitempty" jsonschema:"what to return: brief (default: page type + auth + primary actions with refs + regions + counts, ~50 tok) | refs (every interactive element with refs, capped 150) | text (visible body text, walks iframes; use offset to paginate) | outline (semantic skeleton - headings/tables/lists/forms/regions each with a WORKING css selector - use this to pick selectors for js) | full (refs + visible text) | shot (screenshot: use fullPage/ref)"`
		Offset   int    `json:"offset,omitempty" jsonschema:"char offset into the body text (level=text; paginate long pages)"`
		FullPage bool   `json:"fullPage,omitempty" jsonschema:"capture the whole page, not just the viewport (level=shot)"`
		Ref      string `json:"ref,omitempty" jsonschema:"element ref to capture just that element, clipped to its box (level=shot); takes priority over fullPage"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "see",
		Description: "Look at the current page without navigating. Pick the level that gives what you need: brief = one-glance comprehension (page type, auth, top primary actions with refs, regions, counts) - re-orient after a long flow; refs = the interactive element list with refs to pass to act; text = the visible body text (offset to paginate); outline = the semantic skeleton (headings/tables/lists/forms/regions) each with a WORKING css selector - use this when you're about to js-scrape and need the right selectors; full = refs + visible text; shot = a PNG screenshot (fullPage or ref) for visual layout a text snapshot can't capture. Reaches into same-origin iframes.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		level := strings.ToLower(strings.TrimSpace(a.Level))
		switch level {
		case "", "brief":
			tree := sess.Tree()
			if tree == nil {
				return errResult(browser.ErrNoSnapshot), nil, nil
			}
			return textResult(renderOrientation(sess, tree, snapshot.LevelBrief)), nil, nil
		case "refs", "summary":
			tree := sess.Tree()
			if tree == nil {
				return errResult(browser.ErrNoSnapshot), nil, nil
			}
			return textResult(tree.Render(snapshot.LevelSummary)), nil, nil
		case "text":
			out, err := sess.Read("", a.Offset)
			if err != nil {
				return errResult(err), nil, nil
			}
			return textResult(out), nil, nil
		case "outline":
			out, err := sess.Outline()
			if err != nil {
				return errResult(err), nil, nil
			}
			return textResult(out), nil, nil
		case "full":
			tree := sess.Tree()
			if tree == nil {
				return errResult(browser.ErrNoSnapshot), nil, nil
			}
			if err := sess.FillText(); err != nil {
				return errResult(err), nil, nil
			}
			tree = sess.Tree()
			return textResult(tree.Render(snapshot.LevelFull)), nil, nil
		case "shot", "screenshot":
			png, err := sess.Screenshot(a.FullPage, a.Ref)
			if err != nil {
				return errResult(err), nil, nil
			}
			return imageResult(png), nil, nil
		default:
			return errResult(browser.ErrNoSnapshot), nil, nil
		}
	})
}
