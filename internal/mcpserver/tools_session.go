package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v3/internal/browser"
	"github.com/dondai1234/goshawk/v3/internal/snapshot"
)

func registerSession(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Mode   string `json:"mode" jsonschema:"reset (relaunch the browser - recover from a wedged tab/crashed browser/stale state) | clear (wipe ALL cookies + the current origin's localStorage/sessionStorage and reload) | profile (manage named browser profiles: create/switch/list/delete/export/import)"`
		Action string `json:"action,omitempty" jsonschema:"profile mode: list (list all profiles) | create (make a new profile) | switch (switch to a profile - relaunches with that identity) | delete (remove a profile) | current (show the active profile) | export (dump cookies+localStorage as JSON) | import (restore cookies+localStorage from JSON)"`
		Name   string `json:"name,omitempty" jsonschema:"profile name (for create/switch/delete)"`
		Data   string `json:"data,omitempty" jsonschema:"profile mode: JSON export string to import (action=import)"`
		URL    string `json:"url,omitempty" jsonschema:"reset: navigate to this url after relaunch (optional). clear: navigate to this url after wiping (optional; default reloads the current page)."`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "session",
		Description: "Recover, reset, or switch identity. mode=reset relaunches the whole browser when something is wedged (a tool returned an op-timeout or 'browser session is dead', or a page is an unresponsive SPA) - it re-navigates the current tab to url if given and returns the fresh orientation; other tabs are lost. mode=clear wipes ALL cookies + the current origin's localStorage/sessionStorage and reloads (or opens url) - the one-call clean slate for leftover state. mode=profile manages named browser profiles: action=list shows all profiles, create name= makes a new isolated profile, switch name= switches to it (relaunches the browser with that identity's cookies/auth/storage - page state is lost, re-navigate after), delete name= removes it, current shows the active profile, export dumps the current session's cookies+localStorage as JSON (copy it), import data= restores from that JSON. Profiles give you multiple isolated identities (different logins, cookies, storage) switchable in one call. reset and clear return the fresh page orientation; profile actions return a status message.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		mode := strings.ToLower(strings.TrimSpace(a.Mode))
		switch mode {
		case "reset":
			tree, err := sess.Reset(a.URL)
			if err != nil {
				return errResult(err), nil, nil
			}
			return textResult(renderOrientation(sess, tree, snapshot.LevelBrief)), nil, nil
		case "clear":
			tree, err := sess.Clear(a.URL)
			if err != nil {
				return errResult(err), nil, nil
			}
			return textResult(renderOrientation(sess, tree, snapshot.LevelBrief)), nil, nil
		case "profile":
			return handleProfile(sess, a.Action, a.Name, a.Data)
		default:
			return errResult(fmt.Errorf("unknown session mode %q (reset|clear|profile)", mode)), nil, nil
		}
	})
}

// handleProfile dispatches profile-mode actions for the session tool.
func handleProfile(sess *browser.Session, action, name, data string) (*mcp.CallToolResult, any, error) {
	action = strings.ToLower(strings.TrimSpace(action))
	switch action {
	case "", "list":
		profiles := sess.ListProfiles()
		if len(profiles) == 0 {
			return textResult("no named profiles yet (using the default/temp profile). Create one: session mode=profile action=create name=\"work\""), nil, nil
		}
		b, err := json.MarshalIndent(profiles, "", "  ")
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult("profiles:\n" + string(b)), nil, nil
	case "create":
		if strings.TrimSpace(name) == "" {
			return errResult(fmt.Errorf("profile action=create needs name=")), nil, nil
		}
		if err := sess.CreateProfile(name); err != nil {
			return errResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("created profile %q (switch to it: session mode=profile action=switch name=%q)", name, name)), nil, nil
	case "switch":
		if strings.TrimSpace(name) == "" {
			return errResult(fmt.Errorf("profile action=switch needs name=")), nil, nil
		}
		if err := sess.SwitchProfile(name); err != nil {
			return errResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("switched to profile %q (browser relaunched with that identity; call nav to open a page)", name)), nil, nil
	case "delete":
		if strings.TrimSpace(name) == "" {
			return errResult(fmt.Errorf("profile action=delete needs name=")), nil, nil
		}
		if err := sess.DeleteProfile(name); err != nil {
			return errResult(err), nil, nil
		}
		return textResult(fmt.Sprintf("deleted profile %q", name)), nil, nil
	case "current":
		cur := sess.CurrentProfile()
		if cur == "" {
			return textResult("current profile: default/temp (no named profile active)"), nil, nil
		}
		return textResult(fmt.Sprintf("current profile: %q", cur)), nil, nil
	case "export":
		out, err := sess.ExportState()
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(out), nil, nil
	case "import":
		if strings.TrimSpace(data) == "" {
			return errResult(fmt.Errorf("profile action=import needs data= (the JSON from a prior export)")), nil, nil
		}
		if err := sess.ImportState(data); err != nil {
			return errResult(err), nil, nil
		}
		return textResult("imported cookies + localStorage (call nav or reload to see the restored state)"), nil, nil
	default:
		return errResult(fmt.Errorf("unknown profile action %q (list|create|switch|delete|current|export|import)", action)), nil, nil
	}
}
