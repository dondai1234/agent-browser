package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/internal/browser"
	"github.com/dondai1234/agent-browser/internal/snapshot"
)

// deltaOut renders an act-and-see delta, appending the new page orientation if
// the action navigated (refs reset on navigation).
func deltaOut(delta *snapshot.Delta, after *snapshot.Tree) string {
	out := delta.Render()
	if delta.Navigated {
		out += "\n" + after.Render(snapshot.LevelMinimal)
	}
	return out
}

func registerClick(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Ref      string `json:"ref" jsonschema:"element ref to click (e.g. r46)"`
		SettleMs int    `json:"settleMs,omitempty" jsonschema:"ms to let the DOM settle before re-snapshot (default 150; raise for slow SPAs)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "click",
		Description: "Click an element by ref (moves the real mouse onto it, then clicks). Returns the delta (only what changed). If the click navigated, returns 'navigated: <url>' + the new page orientation. Act-and-see: you usually don't need to call see after. If the ref is gone the error says so - re-see.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		delta, after, err := sess.ClickAndSee(a.Ref, settleDur(a.SettleMs))
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(deltaOut(delta, after)), nil, nil
	})
}

func registerFill(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Ref      string `json:"ref" jsonschema:"element ref of the input/textarea to fill"`
		Value    string `json:"value" jsonschema:"value to set"`
		SettleMs int    `json:"settleMs,omitempty" jsonschema:"ms to let the DOM settle before re-snapshot (default 150; raise for slow SPAs or to capture autocomplete)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fill",
		Description: "Set an input/textarea value by ref and return the delta. Uses the native value setter + dispatches input+change so React/Vue/etc. see the change. For <select> use select, not fill.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		delta, after, err := sess.FillAndSee(a.Ref, a.Value, settleDur(a.SettleMs))
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(deltaOut(delta, after)), nil, nil
	})
}

func registerSelect(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Ref      string `json:"ref" jsonschema:"element ref of the <select> dropdown"`
		Value    string `json:"value" jsonschema:"option to select: the option's value OR its visible display text (pass what the snapshot shows)"`
		SettleMs int    `json:"settleMs,omitempty" jsonschema:"ms to let the DOM settle before re-snapshot (default 150)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "select",
		Description: "Set a <select> dropdown's selection by ref and return the delta. Value matches an option's value OR its visible text - pass what the snapshot shows.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		delta, after, err := sess.SelectAndSee(a.Ref, a.Value, settleDur(a.SettleMs))
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(deltaOut(delta, after)), nil, nil
	})
}

func registerScroll(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		DX       int `json:"dx,omitempty" jsonschema:"horizontal scroll in CSS pixels"`
		DY       int `json:"dy,omitempty" jsonschema:"vertical scroll in CSS pixels (positive = down)"`
		SettleMs int `json:"settleMs,omitempty" jsonschema:"ms to wait for lazy-loaded content (default 150)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "scroll",
		Description: "Scroll the page by dx/dy CSS pixels and return the delta (newly visible lazy-loaded elements appear as added). 'no changes' means nothing new loaded - you've likely hit the bottom.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		delta, after, err := sess.ScrollAndSee(a.DX, a.DY, settleDur(a.SettleMs))
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(deltaOut(delta, after)), nil, nil
	})
}

func registerPressKey(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Key       string `json:"key" jsonschema:"the key to press: a named key (Enter, Escape, Tab, Backspace, Delete, ArrowUp/Down/Left/Right, Home, End, PageUp, PageDown, Space) or a single character (a, 1, /)"`
		Modifiers string `json:"modifiers,omitempty" jsonschema:"modifier keys, '+'-joined: ctrl, shift, alt, meta (e.g. \"ctrl\", \"ctrl+shift\"); default none"`
		SettleMs  int    `json:"settleMs,omitempty" jsonschema:"ms to let the DOM settle before re-snapshot (default 150)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "press_key",
		Description: "Press a key on the focused element (real CDP key event, so native defaults fire: Enter submits a form, Escape closes a modal, Tab moves focus, a char inserts). No ref - it acts on whatever is focused (focus first with fill/click/eval). Returns the delta. Prefer this over eval KeyboardEvent, which does NOT trigger native behavior.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		if a.Key == "" {
			return errResult(fmt.Errorf("key required")), nil, nil
		}
		delta, after, err := sess.PressKeyAndSee(a.Key, a.Modifiers, settleDur(a.SettleMs))
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(deltaOut(delta, after)), nil, nil
	})
}

func registerHover(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Ref      string `json:"ref" jsonschema:"element ref to hover (e.g. r12)"`
		SettleMs int    `json:"settleMs,omitempty" jsonschema:"ms to let hover-revealed content appear before re-snapshot (default 150)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "hover",
		Description: "Move the real mouse onto an element by ref (CDP mouseMoved, iframe-offset-aware). Triggers CSS :hover + JS mouseover/mouseenter, so hover-only menus/tooltips appear. Returns the delta (revealed items show as Added). Prefer this over eval mouseover, which does NOT trigger CSS :hover.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		delta, after, err := sess.HoverAndSee(a.Ref, settleDur(a.SettleMs))
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(deltaOut(delta, after)), nil, nil
	})
}
