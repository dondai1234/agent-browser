package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/internal/browser"
)

func registerScreenshot(srv *mcp.Server, sess *browser.Session) {
	type args struct{} // no parameters
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "screenshot",
		Description: "Capture the current viewport as a PNG. Use when visual layout matters (canvas, icons, charts, spatial relationships) that the text snapshot can't capture. Returns an image - only useful if your harness can view images.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		png, err := sess.Screenshot()
		if err != nil {
			return errResult(err), nil, nil
		}
		return imageResult(png), nil, nil
	})
}

func registerWait(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Seconds float64 `json:"seconds" jsonschema:"max seconds to wait"`
		Text    string  `json:"text,omitempty" jsonschema:"if set, return early once this text appears in the page body"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "wait",
		Description: "Wait up to Seconds seconds, or until Text appears in the page body (whichever is first). Most actions already wait for the DOM to settle - use this only for async content that needs extra time (XHRs, animations).",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		d := time.Duration(a.Seconds * float64(time.Second))
		if err := sess.Wait(d, a.Text); err != nil {
			return errResult(err), nil, nil
		}
		return textResult("ok"), nil, nil
	})
}

func registerEval(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Script string `json:"script" jsonschema:"JavaScript expression to evaluate in the page (result is JSON-serialized)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "eval",
		Description: "Run arbitrary JavaScript in the page and return the JSON result. Enabled by default; the operator can disable with --no-eval. Use for what the typed tools don't cover: canvas/CSV extraction, computed styles, window state, history (window.history.back()/go()), cookies/localStorage (document.cookie, localStorage), console errors (window.onerror). For key actions (Enter/Escape/Tab) use press_key, and for hover use hover - real CDP input events, which unlike JS-dispatched events trigger native behavior and CSS :hover. Session persistence across restarts: start the server with --user-data-dir.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		out, err := sess.Eval(a.Script)
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})
}
