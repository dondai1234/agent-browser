package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/internal/browser"
	"github.com/dondai1234/agent-browser/internal/snapshot"
)

func registerTabs(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Action string `json:"action" jsonschema:"list | new | switch | close | label"`
		ID     string `json:"id,omitempty" jsonschema:"tab id or label (for switch/close)"`
		URL    string `json:"url,omitempty" jsonschema:"url to open in the new tab (action=new; optional)"`
		Label  string `json:"label,omitempty" jsonschema:"label to set on the current/new tab (action=label or new)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "tabs",
		Description: "Manage tabs. list: all tabs (* = current). new: open a tab (url optional) + make it current; returns its orientation. switch: switch to id/label; returns that tab's orientation. close: close id/label (refuses the last tab). label: name the current tab. New/switch save you a see call. Refs are per-tab - after switch/new, use the returned orientation's refs, not refs from another tab.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		switch a.Action {
		case "", "list":
			return textResult(formatTabs(sess.Tabs())), nil, nil
		case "new":
			tree, err := sess.NewTab(a.URL)
			if err != nil {
				return errResult(err), nil, nil
			}
			if a.Label != "" {
				_ = sess.SetTabLabel(a.Label)
			}
			if tree != nil {
				return textResult(tree.Render(snapshot.LevelMinimal)), nil, nil
			}
			return textResult("opened new tab (empty)"), nil, nil
		case "switch":
			if err := sess.SwitchTab(a.ID); err != nil {
				return errResult(err), nil, nil
			}
			// Like `new`: return the current tab's orientation if it has a
			// snapshot, so the agent doesn't need a separate see after switching.
			if tree := sess.Tree(); tree != nil {
				return textResult(tree.Render(snapshot.LevelMinimal)), nil, nil
			}
			return textResult(formatTabs(sess.Tabs())), nil, nil
		case "close":
			if err := sess.CloseTab(a.ID); err != nil {
				return errResult(err), nil, nil
			}
			return textResult(formatTabs(sess.Tabs())), nil, nil
		case "label":
			if err := sess.SetTabLabel(a.Label); err != nil {
				return errResult(err), nil, nil
			}
			return textResult(formatTabs(sess.Tabs())), nil, nil
		default:
			return errResult(fmt.Errorf("unknown tabs action %q (list|new|switch|close|label)", a.Action)), nil, nil
		}
	})
}

func formatTabs(tabs []browser.TabInfo) string {
	if len(tabs) == 0 {
		return "(no tabs)"
	}
	var b strings.Builder
	for _, t := range tabs {
		mark := "  "
		if t.Current {
			mark = "* "
		}
		fmt.Fprintf(&b, "%s%s", mark, t.ID)
		if t.Label != "" {
			fmt.Fprintf(&b, " (%s)", t.Label)
		}
		if t.URL != "" {
			fmt.Fprintf(&b, " %s", t.URL)
		}
		if t.Title != "" {
			fmt.Fprintf(&b, " %q", t.Title)
		}
		b.WriteString("\n")
	}
	return strings.TrimRight(b.String(), "\n")
}
