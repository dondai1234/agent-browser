package mcpserver

import (
	"context"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v3/internal/browser"
	"github.com/dondai1234/goshawk/v3/internal/snapshot"
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
		Description: "Inspect the current page without navigating. Levels: brief (re-orient: type+auth+actions+refs+counts), refs (full interactive list with refs for act), text (visible body text), outline (semantic skeleton with CSS selectors for js), full (refs+text), shot (screenshot). Use outline before js-scraping to get the right selectors. Reaches into same-origin iframes.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		level := strings.ToLower(strings.TrimSpace(a.Level))
		switch level {
		case "", "brief":
			tree, err := sess.EnsureTree()
			if err != nil {
				return errResult(err), nil, nil
			}
			return textResult(renderOrientation(sess, tree, snapshot.LevelBrief)), nil, nil
		case "refs", "summary":
			tree, err := sess.EnsureTree()
			if err != nil {
				return errResult(err), nil, nil
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
			tree, err := sess.EnsureTree()
			if err != nil {
				return errResult(err), nil, nil
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
