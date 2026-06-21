package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
)

func registerUpload(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Ref   string   `json:"ref,omitempty" jsonschema:"element ref of the file input (optional; if omitted, the first file input is auto-targeted)"`
		Paths []string `json:"paths" jsonschema:"file paths to upload (absolute, or relative to the server's working directory)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "upload",
		Description: "Upload files to a file input. Omit ref to auto-target the first <input type=file> on the page (file inputs rarely appear in the text snapshot, so auto-target is the common path). Returns 'uploaded'; you usually submit the form with click after.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		if len(a.Paths) == 0 {
			return errResult(fmt.Errorf("paths required")), nil, nil
		}
		if err := sess.Upload(a.Ref, a.Paths); err != nil {
			return errResult(err), nil, nil
		}
		return textResult("uploaded"), nil, nil
	})
}
