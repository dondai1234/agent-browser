package mcpserver

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
)

func registerCollect(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Fields map[string]string `json:"fields" jsonschema:"{label: CSS selector} map of the values to pull from the page, e.g. {'stars':'.stars','price':'#price','title':'h1'}. Returns {label: text} JSON in one call."`
		Attrs  map[string]string `json:"attrs,omitempty" jsonschema:"optional {label: attribute name} map; for a field whose value should be an attribute instead of text, e.g. {'link':'href'} returns the link's href."`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "collect",
		Description: "Get named values from the page in one call - the multi-value pull without writing JS. Pass fields={label:selector} (e.g. {'stars':'.stars','price':'#price','title':'h1'}) and get back {label:text} JSON. One call replaces N extract calls or a custom eval. Use attrs={label:attrName} to pull an attribute instead of text (e.g. a link's href). Ideal when you need several specific values at once: a repo's stars + language + issues + latest release, or an article's title + first paragraph + infobox. A selector that doesn't match returns null for that label (not an error), so you get a partial result you can branch on.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		out, err := sess.Collect(a.Fields, a.Attrs)
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})
}
