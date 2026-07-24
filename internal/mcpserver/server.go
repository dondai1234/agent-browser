// Package mcpserver wires the browser.Session into an MCP server: 9 tools
// (nav, see, act, js, find, tabs, history, session, login) served over stdio. v4
// adds batch form filling (act fields=), named profiles (session mode=profile),
// self-healing refs, confidence-scored verdicts, and login improvements (remember
// me, forgot password, SSO redirect detection) - all within the same 9-tool surface.
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v4/internal/browser"
)

// Version is the server version reported to MCP clients.
const Version = "4.0.0"

type registerFunc func(srv *mcp.Server, sess *browser.Session)

// toolRegistry maps a tool name to its registration function. Each function
// calls mcp.AddTool with a typed args struct + handler bound to the session.
var toolRegistry = map[string]registerFunc{
	"nav":     registerNav,
	"see":     registerSee,
	"act":     registerAct,
	"js":      registerJS,
	"find":    registerFind,
	"tabs":    registerTabs,
	"history": registerHistory,
	"session": registerSession,
	"login":   registerLogin,
}

// toolOrder is the deterministic registration order (map iteration is unordered).
var toolOrder = []string{"nav", "see", "act", "js", "find", "tabs", "history", "session", "login"}

// New builds an MCP server with all tools bound to a Session.
func New(sess *browser.Session, opts *mcp.ServerOptions) (*mcp.Server, error) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "goshawk", Version: Version}, opts)
	for _, name := range toolOrder {
		if reg, ok := toolRegistry[name]; ok {
			reg(srv, sess)
		}
	}
	return srv, nil
}

// Run serves the MCP server over stdio until ctx is cancelled.
func Run(ctx context.Context, srv *mcp.Server) error {
	return srv.Run(ctx, &mcp.StdioTransport{})
}
