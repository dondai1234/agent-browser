package mcpserver

import (
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v3/internal/browser"
	"github.com/dondai1234/agent-browser/v3/internal/snapshot"
)

// defaultSettle is how long an act-and-see action waits for the DOM to settle
// before re-snapshotting, when the caller doesn't specify one. Short: the
// stable-poll (pullAXLocked) does the real waiting via content-signature
// convergence, so this just gives the page a head-start to start reacting.
// Raise via settleMs for slow XHR-driven updates.
const defaultSettle = 150 * time.Millisecond

func ptrBool(b bool) *bool { return &b }

// openWorld marks a tool that interacts with the open web (all our tools do).
func openWorld() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{OpenWorldHint: ptrBool(true)}
}

// readOnly marks a read-only tool (no page mutation).
func readOnly() *mcp.ToolAnnotations {
	return &mcp.ToolAnnotations{OpenWorldHint: ptrBool(true), ReadOnlyHint: true}
}

// textResult wraps a plain-text response.
func textResult(text string) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: text}},
	}
}

// imageResult wraps a PNG screenshot response. Data is raw bytes; the SDK
// base64-encodes []byte on the wire.
func imageResult(png []byte) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.ImageContent{Data: png, MIMEType: "image/png"}},
	}
}

// errResult wraps an error as an MCP tool-error result (isError=true) so the
// agent sees the message and can react, rather than a transport-level error.
func errResult(err error) *mcp.CallToolResult {
	return &mcp.CallToolResult{
		IsError: true,
		Content: []mcp.Content{&mcp.TextContent{Text: err.Error()}},
	}
}

// settleDur returns settleMs as a Duration, or the default if <= 0.
func settleDur(settleMs int) time.Duration {
	if settleMs > 0 {
		return time.Duration(settleMs) * time.Millisecond
	}
	return defaultSettle
}

// renderOrientation renders a page tree at the given level and appends the tab +
// scroll lines for the orientation levels (brief/minimal), so `nav` and `session`
// land the agent oriented in one call. Refs/full are dense lists - no tab/scroll.
func renderOrientation(sess *browser.Session, tree *snapshot.Tree, level snapshot.Level) string {
	if tree == nil {
		return "no page snapshot (call nav)"
	}
	out := tree.Render(level)
	if level == snapshot.LevelBrief || level == snapshot.LevelMinimal {
		if tab := sess.TabLine(); tab != "" {
			out += "\n" + tab
		}
		out += "\nscroll: " + sess.ScrollInfo()
	}
	return out
}
