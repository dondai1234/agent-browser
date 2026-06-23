package mcpserver

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// dataURL builds a data:text/html;base64,... URL so live tests can run a
// self-contained page with no network + no fixture server. Keeps the v2.2
// selector + sort-verdict tests deterministic + hermetic.
func dataURL(html string) string {
	return "data:text/html;charset=utf-8;base64," + base64.StdEncoding.EncodeToString([]byte(html))
}

// TestSelectorEscapeHatch: a <span> with an onclick but NO a11y role is dropped
// by the a11y tree (generic role), so find-by-role/text cannot reach it - the
// exact gap a CSS-selector tool (charlotte) exploits. The v2.2 selector escape
// hatch reaches it: find selector returns it with a sel=, and act/click/fill by
// selector act on it. This closes charlotte's CSS-selector edge.
func TestSelectorEscapeHatch(t *testing.T) {
	html := `<!doctype html><html><body>
	<h1>Widget page</h1>
	<div><span class="hit" onclick="document.getElementById('o').textContent='clicked'">Hit me</span></div>
	<p id="o">not clicked</p>
	<input class="off" type="text" oninput="document.getElementById('o2').textContent=this.value">
	<p id="o2">empty</p>
	</body></html>`
	sess, ctx, cleanup := realWorldSetupWithFlags(t, "--allow-insecure-schemes")
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": dataURL(html)})

	// The span has no a11y role, so a normal find by role=button/text must miss.
	if got := callTool(t, sess, ctx, "find", map[string]any{"role": "button", "text": "Hit me"}); strings.Contains(got, "Hit me") {
		t.Fatalf("off-tree span should NOT appear in a11y find, got: %s", got)
	}

	// find selector reaches it + returns a sel= the agent can act on.
	got := callTool(t, sess, ctx, "find", map[string]any{"selector": ".hit"})
	if !strings.Contains(got, "[css]") || !strings.Contains(got, "sel=") || !strings.Contains(got, "Hit me") {
		t.Fatalf("find selector=.hit should return a [css] line with sel= + label, got: %s", got)
	}

	// act selector (auto: a span -> click) fires the onclick.
	out := callTool(t, sess, ctx, "act", map[string]any{"selector": ".hit"})
	if !strings.Contains(out, "act selector") || !strings.Contains(out, "click") {
		t.Fatalf("act selector should report a click verb, got: %s", out)
	}
	body := callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "clicked") {
		t.Fatalf("the span's onclick should have fired (body should contain 'clicked'), got: %s", firstLine(body))
	}

	// fill selector fills the off-tree input + the oninput handler updates o2.
	callTool(t, sess, ctx, "fill", map[string]any{"selector": ".off", "value": "hello"})
	body = callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "hello") {
		t.Fatalf("fill selector should set the input value (body should contain 'hello'), got: %s", firstLine(body))
	}

	// click selector directly (explicit mode) on the same span should still work.
	callTool(t, sess, ctx, "navigate", map[string]any{"url": dataURL(html)})
	callTool(t, sess, ctx, "click", map[string]any{"selector": ".hit"})
	body = callTool(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "clicked") {
		t.Fatalf("click selector should fire the onclick, got: %s", firstLine(body))
	}
}

// TestVerdictSortReorder: a click that reorders the same DOM nodes (a sort)
// leaves the backend-id set unchanged, so the element diff sees no
// add/remove/changed. Pre-v2.2 this produced the misleading "no visible effect"
// verdict (the report's saucedemo sort complaint). v2.2's content signature
// detects the reorder + the verdict says "page updated" instead.
func TestVerdictSortReorder(t *testing.T) {
	html := `<!doctype html><html><body>
	<h1>List</h1>
	<div id="l">
		<button class="item">Backpack</button>
		<button class="item">Bike Light</button>
		<button class="item">Fleece</button>
	</div>
	<button id="sort" onclick="sortItems()">Sort desc</button>
	<script>
	function sortItems(){
		var l=document.getElementById('l');
		var items=[].slice.call(l.querySelectorAll('.item'));
		items.sort(function(a,b){return b.textContent.localeCompare(a.textContent);});
		items.forEach(function(i){l.appendChild(i);});
	}
	</script>
	</body></html>`
	sess, ctx, cleanup := realWorldSetupWithFlags(t, "--allow-insecure-schemes")
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": dataURL(html)})

	// Snapshot once so the reorder is measured against a real before-tree (the
	// signature diff needs before + after built from the same page).
	callTool(t, sess, ctx, "see", map[string]any{})

	out := callTool(t, sess, ctx, "act", map[string]any{"intent": "Sort desc"})
	if strings.Contains(strings.ToLower(out), "no visible effect") {
		t.Fatalf("sort reorder must NOT read 'no visible effect' anymore; got: %s", out)
	}
	if !strings.Contains(out, "page updated") {
		t.Fatalf("sort reorder verdict should say 'page updated', got: %s", out)
	}
	// And the order actually changed: Fleece (desc winner) should now be first.
	body := callTool(t, sess, ctx, "read", map[string]any{})
	fleeceIdx := strings.Index(body, "Fleece")
	bikeIdx := strings.Index(body, "Bike Light")
	if fleeceIdx < 0 || bikeIdx < 0 || fleeceIdx > bikeIdx {
		t.Fatalf("after sort-desc, Fleece should precede Bike Light in body; got fleece@%d bike@%d: %s", fleeceIdx, bikeIdx, firstLine(body))
	}
}

// firstLine returns the first line of s (for compact failure messages).
func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

// keep the mcp import referenced (dataURL tests use the SDK client via setup).
var _ = mcp.ClientSession{}
var _ context.Context
