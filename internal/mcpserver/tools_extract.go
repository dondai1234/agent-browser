package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
)

func registerExtract(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Kind     string `json:"kind" jsonschema:"what to extract: table | links | list | form | article | text"`
		Selector string `json:"selector,omitempty" jsonschema:"CSS selector to scope the extraction to a region (e.g. '.infobox', '#release-list'). table/links/list/article search WITHIN that element; text returns each match's text. Omit to search the whole page. The #1 token-saving lever - extract just the region you need instead of the whole page."`
		MaxChars int    `json:"maxChars,omitempty" jsonschema:"cap the returned text/JSON length (default 16000; 12000 for article); lower to spend fewer tokens on a long page"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "extract",
		Description: "Pull structured data off the page as JSON (or text for article), instead of reading 200 refs and reconstructing it. table -> rows (objects if the first row is headers, else arrays). links -> [{text,href}] for every <a>. list -> the largest <ul>/<ol> item texts. form -> [{ref,role,name,value,checked?}] for the page's form controls (from the cached tree, free - use it to fill via act/click). article -> the <article>/<main> text. text -> [string] of each matched element's text (REQUIRES selector; the targeted value pull - a price, a count, a date). Pass selector to scope table/links/list/article to a region (extract just the infobox, just the releases list) - the #1 way to keep the response lean. maxChars caps the length. Returns 'no X found' if the page has none - then use see/read, or a different selector.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		out, err := sess.Extract(a.Kind, a.Selector, a.MaxChars)
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})
}
