package mcpserver

import (
	"context"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
)

func registerScreenshot(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		FullPage bool   `json:"fullPage,omitempty" jsonschema:"capture the whole page, not just the viewport"`
		Ref      string `json:"ref,omitempty" jsonschema:"element ref to capture just that element (clipped to its box); takes priority over fullPage"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "screenshot",
		Description: "Capture the page as a PNG. Default: the current viewport. fullPage=true: the whole page. ref=r12: just that element (clipped to its bounding box). Use when visual layout matters (canvas, icons, charts, spatial relationships) that the text snapshot can't capture. Returns an image - only useful if your harness can view images.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		png, err := sess.Screenshot(a.FullPage, a.Ref)
		if err != nil {
			return errResult(err), nil, nil
		}
		return imageResult(png), nil, nil
	})
}

func registerWait(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Seconds float64 `json:"seconds" jsonschema:"max seconds to wait"`
		Text    string  `json:"text,omitempty" jsonschema:"return early once this text appears in the page body"`
		URL     string  `json:"url,omitempty" jsonschema:"return early once the page URL contains this substring (e.g. a redirect to /dashboard)"`
		Gone    string  `json:"gone,omitempty" jsonschema:"return early once this text is NO LONGER in the page body (e.g. a Loading spinner disappearing)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "wait",
		Description: "Wait up to Seconds seconds, or until a condition is met (whichever is first). url= waits for the URL to contain a substring (redirect done, login landed); text= waits for text to appear in the body; gone= waits for text to disappear (a spinner/loading banner clearing). Returns what satisfied it, or an error on timeout. Most actions already wait for the DOM to settle - use this for async content that needs extra time (XHRs, animations, redirects).",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		d := time.Duration(a.Seconds * float64(time.Second))
		out, err := sess.Wait(d, a.Text, a.URL, a.Gone)
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})
}

func registerEval(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Script string `json:"script" jsonschema:"JavaScript expression to evaluate in the page (result is JSON-serialized)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "eval",
		Description: "Run JavaScript in the page and return the JSON result. The go-to for pulling specific values the page shows but extract can't target by selector - a star count, a price, a date, a status, or several at once (return them as one object: {stars:..., price:..., date:...}). One expression, no re-snapshot, no refs. Also covers what the typed tools don't: canvas/CSV extraction, computed styles, window state, history (window.history.back()/go()), cookies/localStorage (document.cookie, localStorage), console errors (window.onerror). Enabled by default; the operator can disable with --no-eval. For key actions (Enter/Escape/Tab) use press_key, and for hover use hover - real CDP input events, which unlike JS-dispatched events trigger native behavior and CSS :hover. Session persistence across restarts: start the server with --user-data-dir.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		out, err := sess.Eval(a.Script)
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})
}
