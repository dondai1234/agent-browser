package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
	"github.com/dondai1234/agent-browser/v2/internal/snapshot"
)

func registerClear(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		URL string `json:"url,omitempty" jsonschema:"optional URL to open after clearing (default: reload the current page)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "clear",
		Description: "One-call clean slate: wipe cookies + web storage and reload (or open url). Use it when the page carries leftover state from a previous run - a cart with items you didn't add, a half-filled form, a logged-in session you want to reset - instead of removing items one by one. Clears all cookies (every site) + the current origin's localStorage/sessionStorage, then re-navigates and returns the fresh page orientation. Other tabs keep their pages but lose their cookies too.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		tree, err := sess.Clear(a.URL)
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(tree.Render(snapshot.LevelMinimal)), nil, nil
	})
}
