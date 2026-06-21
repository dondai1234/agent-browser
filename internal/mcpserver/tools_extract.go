package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
)

func registerExtract(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Kind string `json:"kind" jsonschema:"what to extract: table | links | list | form | article"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "extract",
		Description: "Pull structured data off the page as JSON (or text for article), instead of reading 200 refs and reconstructing it. table -> rows (objects if the first row is headers, else arrays). links -> [{text,href}] for every <a>. list -> the largest <ul>/<ol> item texts. form -> [{ref,role,name,value,checked?}] for the page's form controls (from the cached tree, free - use it to fill via act/click). article -> the <article>/<main> text. Returns 'no X found' if the page has none - then use see/read. Capped to keep the response lean; narrow by navigating to the specific page/section.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		out, err := sess.Extract(a.Kind)
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})
}
