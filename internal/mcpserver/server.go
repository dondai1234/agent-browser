// Package mcpserver wires the browser.Session into an MCP server: 15 tools
// (navigate, see, find, read, click, fill, select, scroll, wait, screenshot,
// eval, tabs, upload, press_key, hover) served over stdio.
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/internal/browser"
)

// Version is the server version reported to MCP clients.
const Version = "1.0.0"

type registerFunc func(srv *mcp.Server, sess *browser.Session)

// toolRegistry maps a tool name to its registration function. Each function
// calls mcp.AddTool with a typed args struct + handler bound to the session.
var toolRegistry = map[string]registerFunc{
	"navigate":   registerNavigate,
	"see":        registerSee,
	"find":       registerFind,
	"read":       registerRead,
	"click":      registerClick,
	"fill":       registerFill,
	"select":     registerSelect,
	"scroll":     registerScroll,
	"wait":       registerWait,
	"screenshot": registerScreenshot,
	"eval":       registerEval,
	"tabs":       registerTabs,
	"upload":     registerUpload,
	"press_key":  registerPressKey,
	"hover":      registerHover,
}

// toolOrder is the deterministic registration order (map iteration is unordered).
var toolOrder = []string{
	"navigate", "see", "find", "read", "click", "fill", "select", "scroll", "wait",
	"screenshot", "eval", "tabs", "upload", "press_key", "hover",
}

// New builds an MCP server with all tools bound to a Session.
func New(sess *browser.Session, opts *mcp.ServerOptions) (*mcp.Server, error) {
	srv := mcp.NewServer(&mcp.Implementation{Name: "agent-browser", Version: Version}, opts)
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
