package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
	"github.com/dondai1234/agent-browser/v2/internal/snapshot"
)

func registerNavigate(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Action string `json:"action,omitempty" jsonschema:"open (default: navigate to url) | back | forward | reload"`
		URL    string `json:"url,omitempty" jsonschema:"URL to open (action=open; http/https only; other schemes blocked unless --allow-insecure-schemes)"`
		Level  string `json:"level,omitempty" jsonschema:"brief (page type + auth + primary actions, ~50 tok) | minimal (default: orientation + counts) | summary (interactive list + refs) | full (summary + text)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "navigate",
		Description: "Navigate the current tab and return the page snapshot. action=open (default) opens url; back/forward traverse browser history; reload re-fetches the current page. back/forward with no history is a no-op (returns the current page). level=brief gives a one-glance comprehension (page type, auth, primary actions, regions) - best first call on an unknown page; default minimal (orientation + counts); summary (interactive list + refs to act on immediately); full (summary + text).",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		action := strings.ToLower(strings.TrimSpace(a.Action))
		var tree *snapshot.Tree
		var err error
		switch action {
		case "", "open":
			tree, err = sess.NavigateAndSee(a.URL)
		case "back", "forward", "reload":
			tree, err = sess.NavigateAction(action)
		default:
			return errResult(fmt.Errorf("unknown navigate action %q (open|back|forward|reload)", action)), nil, nil
		}
		if err != nil {
			return errResult(err), nil, nil
		}
		level := snapshot.Level(a.Level)
		switch level {
		case snapshot.LevelBrief, snapshot.LevelMinimal, snapshot.LevelSummary, snapshot.LevelFull:
		default:
			level = snapshot.LevelMinimal
		}
		if level == snapshot.LevelFull {
			if err := sess.FillText(); err != nil {
				return errResult(err), nil, nil
			}
		}
		return textResult(tree.Render(level)), nil, nil
	})
}

func registerSee(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Level string `json:"level,omitempty" jsonschema:"brief (page type + auth + primary actions, ~50 tok) | minimal (default: url/title/landmarks/headings/counts) | summary (every interactive element with refs, capped 150) | full (summary + visible text)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "see",
		Description: "Snapshot the current page from the cached a11y tree (no reload). brief = one-glance comprehension: page type, auth state, the top primary actions with refs, regions, counts (~50 tok) - use it to decide what to do next on an unknown page. minimal = url/title/landmarks/headings/interactive counts. summary = every interactive element with refs, capped 150 (prefer find for specifics). full = summary + visible page text. Pick the cheapest level that gives what you need.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		tree := sess.Tree()
		if tree == nil {
			return errResult(browser.ErrNoSnapshot), nil, nil
		}
		level := snapshot.Level(a.Level)
		switch level {
		case snapshot.LevelBrief, snapshot.LevelMinimal, snapshot.LevelSummary, snapshot.LevelFull:
		default:
			level = snapshot.LevelMinimal
		}
		if level == snapshot.LevelFull {
			// full = summary + visible text; fetch the body text (iframe-walk)
			// and attach it to the cached tree before rendering.
			if err := sess.FillText(); err != nil {
				return errResult(err), nil, nil
			}
			tree = sess.Tree() // re-fetch; FillText set .Text on the cached tree
		}
		return textResult(tree.Render(level)), nil, nil
	})
}

func registerFind(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Role  string `json:"role,omitempty" jsonschema:"a11y role to filter by: button, link, textbox, checkbox, menuitem, option, tab, ..."`
		Text  string `json:"text,omitempty" jsonschema:"name substring to filter by (case-insensitive); or exact name if exact=true"`
		Exact bool   `json:"exact,omitempty" jsonschema:"match the name exactly (case-insensitive) instead of substring"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "find",
		Description: "Find interactive elements by role and/or name from the cached snapshot - free (no page reload). Returns matching elements with refs (e.g. r12) to pass to click/fill/select. Reaches into same-origin iframes (shown with an 'in \"framename\"' tag). Omit both role and text to list every interactive element (can be large - prefer a filter). Use exact=true to match the name exactly (avoids substring false positives like 'more' matching '...more than...').",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		tree := sess.Tree()
		if tree == nil {
			return errResult(browser.ErrNoSnapshot), nil, nil
		}
		var els []snapshot.Element
		if a.Exact {
			els = tree.FindExact(a.Role, a.Text)
		} else {
			els = tree.Find(a.Role, a.Text)
		}
		return textResult(snapshot.RenderElements(els)), nil, nil
	})
}

func registerRead(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Ref    string `json:"ref,omitempty" jsonschema:"element ref (e.g. r12) to read the text of; omit for the whole body"`
		Offset int    `json:"offset,omitempty" jsonschema:"char offset into the body text (paginate long pages)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "read",
		Description: "Read text without re-snapshotting. With ref: url+title+that element's text (a link ref also includes its href, so you know where it goes without clicking). Without ref: url+title+full body text (walks same-origin iframes), truncated at 8000 chars - pass offset to paginate; the marker reports the total length. Cheaper than see full when you only need text.",
		Annotations: readOnly(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		out, err := sess.Read(a.Ref, a.Offset)
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(out), nil, nil
	})
}
