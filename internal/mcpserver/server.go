// Package mcpserver wires the browser.Session into an MCP server: 8 tools
// (nav, see, act, js, find, tabs, history, session) served over stdio. v3
// collapses the v2 22-tool surface into a few god-tier, composable tools an
// agent masters from the defs alone: nav returns an orientation, act is any
// single action (click/fill/select/hover/press/upload + optional wait) by
// intent/ref/selector, js is the structured-data hero (run JS with a helper API
// -> clean JSON), see adds `outline` (a semantic skeleton with working CSS
// selectors), and find bridges a11y refs and CSS selectors.
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v3/internal/browser"
)

// Version is the server version reported to MCP clients.
const Version = "3.2.0"

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
