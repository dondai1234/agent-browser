// Package mcpserver wires the browser.Session into an MCP server: 20 tools
// (navigate, see, find, extract, read, click, act, fill, select, scroll, wait,
// screenshot, eval, tabs, upload, press_key, hover, history, where, reset) served over
// stdio. v2 adds intent-first act, a verdict on every action, level=brief page
// comprehension, extract (structured data), history (session memory offload),
// semantic wait conditions, multi-field fill, scroll-awareness, browser
// back/forward/reload, scroll-to-ref, link href on read, full-page/element
// screenshots, and where (one-shot re-orientation).
package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
)

// Version is the server version reported to MCP clients.
const Version = "2.2.2"

type registerFunc func(srv *mcp.Server, sess *browser.Session)

// toolRegistry maps a tool name to its registration function. Each function
// calls mcp.AddTool with a typed args struct + handler bound to the session.
var toolRegistry = map[string]registerFunc{
	"navigate":   registerNavigate,
	"see":        registerSee,
	"find":       registerFind,
	"extract":    registerExtract,
	"read":       registerRead,
	"click":      registerClick,
	"act":        registerAct,
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
	"history":    registerHistory,
	"where":      registerWhere,
	"reset":      registerReset,
}

// toolOrder is the deterministic registration order (map iteration is unordered).
var toolOrder = []string{
	"navigate", "see", "find", "extract", "read", "click", "act", "fill", "select", "scroll", "wait",
	"screenshot", "eval", "tabs", "upload", "press_key", "hover", "history", "where", "reset",
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
