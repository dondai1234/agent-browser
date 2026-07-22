package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v3/internal/browser"
)

func registerJS(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Script   string `json:"script" jsonschema:"JS to run. A bare expression (document.title) OR a function body that returns (return {stars: text('#stars'), lang: attr('.lang','aria-label'), items: $$('li').map(text)}). Helpers in scope: $(sel), $$(sel)->array, text(x), attr(x,name), html(x), visible(x), data(x,k), table(sel)->rows, links(sel)->[{text,href}], rect(x), xpath(xp), frame(title)->iframe doc, wait(fn,ms). Result is JSON (a bare string is unquoted to plain text)."`
		Await    string `json:"await,omitempty" jsonschema:"CSS selector to wait for BEFORE running the script (fuses wait+scrape: e.g. await=\"table.releases\" then return table(...))"`
		AwaitMs  int    `json:"awaitMs,omitempty" jsonschema:"await budget in ms (default 5000)"`
		MaxChars int    `json:"maxChars,omitempty" jsonschema:"cap the JSON response length (default 20000); lower to spend fewer tokens"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "js",
		Description: "Run JavaScript in the page and get clean JSON back - the go-to for ANY structured or scattered data (a star count, a price, a date, a table, several fields at once). One call, no re-snapshot, no refs to parse. A helper API is in scope so you write one-liners: $(sel) and $$(sel) (querySelector/-All, $$ returns an array), text(x) (innerText trimmed; takes an element OR a selector), attr(x,name), html(x), visible(x), data(x,k), prop(x,name) (element property e.g. prop(input,'value')), form(sel) (serialize a form to {name:value}), meta(name) (a <meta> content by name/property, e.g. 'og:title'), table(sel) (a <table> to rows, or objects if the first row is <th>), links(sel) (-> [{text,href}]), rect(x), xpath(xp), frame(title) (a same-origin iframe's document, so you can $(sel, frame('widget'))), wait(fn,ms) (async poll for a condition). Return your result: a bare expression like document.title works, or return {a:..., b:...} for several fields. await=\"sel\" waits for a selector first (wait+scrape in one call). A thrown error is surfaced as a tool error with the page-side message. Also covers what typed tools can't: computed styles, window state, cookies (document.cookie), localStorage, canvas, console errors. Disabled if the server was started with --no-eval.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		out, err := sess.RunJS(a.Script, a.Await, a.AwaitMs, a.MaxChars)
		if err != nil {
			return errResult(err), nil, nil
		}
		if out == "" {
			return textResult("(no result)"), nil, nil
		}
		return textResult(out), nil, nil
	})
}
