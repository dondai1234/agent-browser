package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v3/internal/browser"
	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

func registerSession(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Mode string `json:"mode" jsonschema:"reset (relaunch the browser - recover from a wedged tab/crashed browser/stale state; other tabs are lost) | clear (wipe ALL cookies + the current origin's localStorage/sessionStorage and reload, or navigate to url - a one-call clean slate for leftover carts/half-filled forms/logins)"`
		URL  string `json:"url,omitempty" jsonschema:"reset: navigate to this url after relaunch (optional). clear: navigate to this url after wiping (optional; default reloads the current page)."`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "session",
		Description: "Recover or reset. mode=reset relaunches the whole browser when something is wedged (a tool returned an op-timeout or 'browser session is dead', or a page is an unresponsive SPA) - it re-navigates the current tab to url if given and returns the fresh orientation; other tabs are lost (a crashed browser took them anyway). mode=clear wipes ALL cookies + the current origin's localStorage/sessionStorage and reloads (or opens url) - the one-call clean slate for leftover state (a cart with items you didn't add, a half-filled form, a logged-in session to drop). Both return the fresh page orientation.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		mode := strings.ToLower(strings.TrimSpace(a.Mode))
		var tree *snapshot.Tree
		var err error
		switch mode {
		case "reset":
			tree, err = sess.Reset(a.URL)
		case "clear":
			tree, err = sess.Clear(a.URL)
		default:
			return errResult(fmt.Errorf("unknown session mode %q (reset|clear)", mode)), nil, nil
		}
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(renderOrientation(sess, tree, snapshot.LevelBrief)), nil, nil
	})
}
