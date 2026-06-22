package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
)

func registerHistory(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Last   int  `json:"last,omitempty" jsonschema:"return only the most recent N entries (default all)"`
		Errors bool `json:"errors,omitempty" jsonschema:"if true, show only blocked (CHALLENGE) or failed (error) actions"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "history",
		Description: "Recall the session's action log - what you've done on the page, with each action's verdict + URL - offloaded from your context so a long task doesn't bloat it. Each entry: step number, time, action, verdict, url. Pass last=N for the recent N; errors=true for blocked (CHALLENGE) or failed (error) actions only. Use it to re-orient after a long flow ('where am I, what did I just do, did that last click work') instead of re-snapshotting or relying on your own context. Step numbers are monotonic across the whole session.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		return textResult(sess.History(a.Last, a.Errors)), nil, nil
	})
}
