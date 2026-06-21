package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
	"github.com/dondai1234/agent-browser/v2/internal/snapshot"
)

// registerReset adds the reset tool: the explicit recovery path when a tab is
// wedged or dead. The agent asked for this directly ("no session management -
// no explicit close/reset tool when session hangs"); it pairs with the per-op
// timeout (which guarantees the session mutex is never held forever, so reset
// can always acquire it within the op budget).
func registerReset(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		URL string `json:"url,omitempty" jsonschema:"optional URL to open in the fresh tab (default: a blank tab)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "reset",
		Description: "Recover from a wedged or dead tab: drop the current tab (cancelling any hung operation on it) and open a fresh one, navigating to url if given. Other tabs are kept. Use this when a tool returned an operation-timeout error, or the page is an unresponsive SPA, or refs seem unusable - it is the explicit session-recovery path (no need to restart the server). Returns the new tab's orientation. If reset itself fails, the browser process is dead; restart the MCP server.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		tree, err := sess.Reset(a.URL)
		if err != nil {
			return errResult(err), nil, nil
		}
		if tree != nil {
			return textResult(tree.Render(snapshot.LevelMinimal)), nil, nil
		}
		return textResult("reset: opened a fresh blank tab"), nil, nil
	})
}
