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
		Description: "Recover from a wedged or dead tab without restarting the server. If the browser is alive (the common case: a tool timed out, an SPA is unresponsive, refs seem unusable), it re-navigates the current tab to a fresh page (url, or about:blank) and KEEPS your other tabs + their logins. If the browser itself is dead, it relaunches Chrome (other tabs are lost). Returns the new page orientation. If reset itself fails, restart the MCP server.",
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
