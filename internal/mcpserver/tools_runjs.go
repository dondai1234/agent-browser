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
		Description: "Run JavaScript in the page, get clean JSON back. For any structured or scattered data extraction - one call replaces multiple see/find/act. Helper API in scope: $(sel), $$(sel), text(x), attr(x,name), table(sel)->rows, links(sel), form(sel), meta(name), prop(x,name), frame(title), wait(fn,ms), rect(x). await=sel waits for a selector first. Also covers cookies, localStorage, computed styles, window state. Disabled if started with --no-eval.",
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
