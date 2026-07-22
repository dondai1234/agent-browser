package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v3/internal/browser"
)

func registerHistory(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Last   int  `json:"last,omitempty" jsonschema:"show only the most recent N entries (after any error filter)"`
		Errors bool `json:"errors,omitempty" jsonschema:"show only blocked (CHALLENGE) or failed (error) actions"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "history",
		Description: "The session action log (offloaded from your context). Every act/nav records a step: action, verdict, url. Query it to re-orient after a long flow instead of carrying the transcript. errors=true shows only blocked (CHALLENGE) or failed actions; last=N limits to the most recent N. Step numbers are monotonic across the whole session so you can reference them stably.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		last := a.Last // 0 = all
		return textResult(sess.History(last, a.Errors)), nil, nil
	})
}
