package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v3/internal/browser"
	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

func registerNav(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Action       string `json:"action,omitempty" jsonschema:"open (default: navigate to url) | back | forward | reload"`
		URL          string `json:"url,omitempty" jsonschema:"URL to open (action=open; http/https only; other schemes blocked unless --allow-insecure-schemes). Required for open and for newTab."`
		NewTab       bool   `json:"newTab,omitempty" jsonschema:"open url in a new tab (makes it current); requires url. back/forward/reload ignore this."`
		Label        string `json:"label,omitempty" jsonschema:"optional memorable label for a new tab (e.g. \"admin\")"`
		WaitSelector string `json:"waitSelector,omitempty" jsonschema:"CSS selector to wait for before building the tree (native waitForSelector for slow SPAs). e.g. \"input[name=email]\" or \"#content\". Times out after 10s. Only for action=open."`
		Level        string `json:"level,omitempty" jsonschema:"orientation detail to return: brief (default: page type + auth + primary actions with refs + regions + counts) | minimal (url/title/landmarks/headings/counts) | refs (interactive list with refs) | full (refs + visible text)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "nav",
		Description: "Navigate to a URL and get an orientation back - page type, auth, primary actions WITH refs, counts. Act from here without calling see. Auto-dismisses cookie banners, auto-recovers consent redirects, detects CHALLENGE and BLANK PAGE. waitSelector= waits for a CSS selector before returning (slow SPAs). back/forward/reload for history; newTab=true opens a new tab.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		action := strings.ToLower(strings.TrimSpace(a.Action))
		level := navLevel(a.Level)
		var tree *snapshot.Tree
		var err error
		switch action {
		case "", "open":
			if a.NewTab {
				if strings.TrimSpace(a.URL) == "" {
					return errResult(fmt.Errorf("newTab needs a url")), nil, nil
				}
				tree, err = sess.NewTab(a.URL)
				if err == nil && strings.TrimSpace(a.Label) != "" {
					_ = sess.SetTabLabel(a.Label)
				}
			} else {
				tree, err = sess.NavigateAndSee(a.URL, a.WaitSelector)
			}
		case "back", "forward", "reload":
			tree, err = sess.NavigateAction(action)
		default:
			return errResult(fmt.Errorf("unknown nav action %q (open|back|forward|reload)", action)), nil, nil
		}
		if err != nil {
			return errResult(err), nil, nil
		}
		if tree == nil {
			return textResult("new tab opened (blank)"), nil, nil
		}
		if level == snapshot.LevelFull {
			if err := sess.FillText(); err != nil {
				return errResult(err), nil, nil
			}
		}
		return textResult(renderOrientation(sess, tree, level)), nil, nil
	})
}

// navLevel parses the level arg, defaulting to brief (the v3 orientation win:
// nav lands you with page type + primary-action refs instead of a bare url).
func navLevel(s string) snapshot.Level {
	switch snapshot.Level(strings.ToLower(strings.TrimSpace(s))) {
	case snapshot.LevelMinimal, snapshot.LevelSummary, snapshot.LevelFull:
		return snapshot.Level(s)
	case "refs":
		return snapshot.LevelSummary
	}
	return snapshot.LevelBrief
}
