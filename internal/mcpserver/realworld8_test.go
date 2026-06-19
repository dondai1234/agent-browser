package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestRealWorldPressKey: pressing Enter on a focused single-line input must
// SUBMIT the form (the browser's native default action). eval's
// new KeyboardEvent('keydown',{key:'Enter'}) does NOT fire native defaults, so
// this proves press_key uses real CDP input (Input.dispatchKeyEvent). fill
// focuses the input first; press_key Enter submits; #result is updated.
func TestRealWorldPressKey(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body>
<form onsubmit="document.getElementById('r').textContent='submitted:'+this.q.value; return false">
<input name="q" aria-label="Query">
</form>
<div id="r">empty</div>
</body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": srv.URL, "level": "summary"})
	qRef := refFor(t, callTool(t, sess, ctx, "find", map[string]any{"role": "textbox", "text": "Query"}), "Query")
	if qRef == "" {
		t.Fatal("Query input not found in snapshot")
	}
	callTool(t, sess, ctx, "fill", map[string]any{"ref": qRef, "value": "hello-enter"})
	callTool(t, sess, ctx, "press_key", map[string]any{"key": "Enter"})

	body := callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "submitted:hello-enter") {
		t.Errorf("press_key Enter did not submit the form (native default action). body=%q", body)
	}
}

// TestRealWorldHover: hovering an element must trigger CSS :hover so a
// hover-only menu appears. eval's dispatchEvent(new MouseEvent('mouseover'))
// does NOT trigger CSS :hover (which is driven by real mouse position), so this
// proves hover uses real CDP input (Input.dispatchMouseEvent mouseMoved). The
// menu links start display:none (absent from the a11y tree); after hover they
// appear as Added.
func TestRealWorldHover(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><style>.menu{display:none} .trigger:hover .menu{display:block}</style></head><body>
<div class="trigger" role="button" tabindex="0">Menu
<div class="menu">
<a href="#" aria-label="HiddenLink1">Link1</a>
<a href="#" aria-label="HiddenLink2">Link2</a>
</div>
</div>
</body></html>`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": srv.URL, "level": "summary"})

	// Before hover: the menu links are display:none -> not in the a11y tree.
	beforeFind := callTool(t, sess, ctx, "find", map[string]any{"role": "link", "text": "HiddenLink1"})
	if strings.Contains(beforeFind, "HiddenLink1") {
		t.Fatalf("HiddenLink1 should be hidden before hover, got find=%q", beforeFind)
	}

	trigRef := refFor(t, callTool(t, sess, ctx, "find", map[string]any{"role": "button", "text": "Menu"}), "Menu")
	if trigRef == "" {
		t.Fatal("Menu trigger button not found in snapshot")
	}

	delta := callTool(t, sess, ctx, "hover", map[string]any{"ref": trigRef})
	t.Logf("hover delta: %s", delta)

	// After hover the CSS :hover menu appears; the link shows up in the delta
	// or in a follow-up find.
	if !strings.Contains(delta, "HiddenLink1") {
		afterFind := callTool(t, sess, ctx, "find", map[string]any{"role": "link", "text": "HiddenLink1"})
		if !strings.Contains(afterFind, "HiddenLink1") {
			t.Errorf("hover did not reveal the CSS :hover menu: HiddenLink1 not found after hover. delta=%q afterFind=%q", delta, afterFind)
		}
	}
}
