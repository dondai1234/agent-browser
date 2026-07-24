package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v4/internal/browser"
)

func registerHistory(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Last   int  `json:"last,omitempty" jsonschema:"show only the most recent N entries (after any error filter)"`
		Errors bool `json:"errors,omitempty" jsonschema:"show only blocked (CHALLENGE) or failed (error) actions"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "history",
		Description: "Session action log - re-orient after a long flow without carrying the transcript. Every act/nav records a step. errors=true for failures only, last=N for recent N.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		last := a.Last // 0 = all
		return textResult(sess.History(last, a.Errors)), nil, nil
	})
}
