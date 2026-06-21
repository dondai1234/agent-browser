package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
)

func registerWhere(srv *mcp.Server, sess *browser.Session) {
	type args struct{}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "where",
		Description: "One-shot ~30-token re-orientation: current URL, page type, auth state, your last action's verdict, and scroll position (more-below / at-bottom). Use it to recover your place after a long flow or a context compaction instead of a full see + history. If there's no snapshot yet it says so.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		return textResult(sess.Where()), nil, nil
	})
}
