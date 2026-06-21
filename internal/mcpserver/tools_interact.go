package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/agent-browser/v2/internal/browser"
	"github.com/dondai1234/agent-browser/v2/internal/snapshot"
)

// deltaOut renders an act-and-see result: the verdict (one-line semantic
// outcome) first, then the delta detail. On navigation the verdict already
// says "navigated to <url>", so we skip the redundant navigated line and just
// append the new page orientation (the refs the agent needs next).
func deltaOut(delta *snapshot.Delta, after *snapshot.Tree) string {
	var out string
	if delta.Verdict != "" {
		out = "verdict: " + delta.Verdict + "\n"
	}
	if delta.Navigated {
		if after != nil {
			out += after.Render(snapshot.LevelMinimal)
		}
		return out
	}
	out += delta.Render()
	return out
}

func registerClick(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Ref      string `json:"ref" jsonschema:"element ref to click (e.g. r46)"`
		SettleMs int    `json:"settleMs,omitempty" jsonschema:"ms to let the DOM settle before re-snapshot (default 150; raise for slow SPAs)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "click",
		Description: "Click an element by ref (moves the real mouse onto it, then clicks). Returns a verdict + the delta (only what changed); on navigation the verdict says 'navigated to <url>' and the new page orientation follows. Act-and-see: you usually don't need to call see after. If the ref is gone the error says so - re-see.",
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
		Ref      string            `json:"ref,omitempty" jsonschema:"element ref of the input/textarea to fill (single-field mode)"`
		Value    string            `json:"value,omitempty" jsonschema:"value to set (single-field mode)"`
		Fields   map[string]string `json:"fields,omitempty" jsonschema:"a {ref: value} map to fill many inputs in one call (e.g. a whole checkout form from extract form); one round-trip + one delta instead of N. Takes priority over ref/value."`
		SettleMs int               `json:"settleMs,omitempty" jsonschema:"ms to let the DOM settle before re-snapshot (default 150; raise for slow SPAs or to capture autocomplete)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "fill",
		Description: "Set input/textarea value(s) and return a verdict + the delta. Single field: pass ref + value. Whole form: pass fields={ref:value,...} (e.g. from extract form's refs) to fill many in one call - one round-trip + one delta instead of N. Uses the native value setter + dispatches input+change so React/Vue/etc. see the change. For <select> use select, not fill.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		if len(a.Fields) > 0 {
			delta, after, err := sess.FillMany(a.Fields, settleDur(a.SettleMs))
			if err != nil {
				return errResult(err), nil, nil
			}
			return textResult(deltaOut(delta, after)), nil, nil
		}
		if a.Ref == "" {
			return errResult(fmt.Errorf("fill needs either ref+value (single field) or fields={ref:value} (multi)")), nil, nil
		}
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
		Description: "Set a <select> dropdown's selection by ref and return a verdict + the delta. Value matches an option's value OR its visible text - pass what the snapshot shows.",
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
		Ref      string `json:"ref,omitempty" jsonschema:"element ref to scroll into view (e.g. r12); takes priority over dx/dy"`
		DX       int    `json:"dx,omitempty" jsonschema:"horizontal scroll in CSS pixels"`
		DY       int    `json:"dy,omitempty" jsonschema:"vertical scroll in CSS pixels (positive = down)"`
		SettleMs int    `json:"settleMs,omitempty" jsonschema:"ms to wait for lazy-loaded content (default 150)"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "scroll",
		Description: "Scroll and return a verdict + the delta. By pixels (dx/dy; newly visible lazy-loaded elements appear as added) or scroll an element into view (ref, block:center) when it's off-screen. The verdict reports the scroll position ('more below' / 'at bottom') so you know whether to keep scrolling.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		var delta *snapshot.Delta
		var after *snapshot.Tree
		var err error
		if a.Ref != "" {
			delta, after, err = sess.ScrollToAndSee(a.Ref, settleDur(a.SettleMs))
		} else {
			delta, after, err = sess.ScrollAndSee(a.DX, a.DY, settleDur(a.SettleMs))
		}
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
		Description: "Press a key on the focused element (real CDP key event, so native defaults fire: Enter submits a form, Escape closes a modal, Tab moves focus, a char inserts). No ref - it acts on whatever is focused (focus first with fill/click/eval). Returns a verdict + the delta. Prefer this over eval KeyboardEvent, which does NOT trigger native behavior.",
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
		Description: "Move the real mouse onto an element by ref (CDP mouseMoved, iframe-offset-aware). Triggers CSS :hover + JS mouseover/mouseenter, so hover-only menus/tooltips appear. Returns a verdict + the delta (revealed items show as Added). Prefer this over eval mouseover, which does NOT trigger CSS :hover.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		delta, after, err := sess.HoverAndSee(a.Ref, settleDur(a.SettleMs))
		if err != nil {
			return errResult(err), nil, nil
		}
		return textResult(deltaOut(delta, after)), nil, nil
	})
}
