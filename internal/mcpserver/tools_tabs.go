package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v3/internal/browser"
)

func registerTabs(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Action string `json:"action,omitempty" jsonschema:"list (default: list all tabs) | switch | close | label"`
		ID     string `json:"id,omitempty" jsonschema:"tab id or label (e.g. t2, or a label you set); for switch/close"`
		Label  string `json:"label,omitempty" jsonschema:"the label to set on the current tab (action=label)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tabs",
		Description: "Manage tabs. action=list (default) lists every tab (id, label, url, title, current). switch id=t2 makes that tab current. close id=t2 closes it (the last tab cannot be closed). label label=\"admin\" names the current tab so you can switch to it by name later. Open a NEW tab with nav newTab=true.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		action := strings.ToLower(strings.TrimSpace(a.Action))
		switch action {
		case "", "list":
			tabs := sess.Tabs()
			b, err := json.MarshalIndent(tabs, "", "  ")
			if err != nil {
				return errResult(err), nil, nil
			}
			return textResult(string(b)), nil, nil
		case "switch":
			if err := sess.SwitchTab(a.ID); err != nil {
				return errResult(err), nil, nil
			}
			return textResult("switched to " + a.ID), nil, nil
		case "close":
			if err := sess.CloseTab(a.ID); err != nil {
				return errResult(err), nil, nil
			}
			return textResult("closed " + a.ID), nil, nil
		case "label":
			if err := sess.SetTabLabel(a.Label); err != nil {
				return errResult(err), nil, nil
			}
			return textResult(fmt.Sprintf("labeled current tab %q", a.Label)), nil, nil
		default:
			return errResult(fmt.Errorf("unknown tabs action %q (list|switch|close|label)", action)), nil, nil
		}
	})
}
