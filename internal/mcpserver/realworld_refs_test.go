package mcpserver

import (
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestStableRefsAcrossReRender is the live proof for the ref-stability fix.
// It reproduces the commenter's failure mode: a page mutation that shifts tree
// order. With positional refs (the old design), an element's ref would drift to
// a DIFFERENT control after such a mutation - so an agent holding an old ref
// would silently click the wrong element. With stable backend-keyed refs, the
// same DOM node keeps its ref, so an old ref still resolves to the same control.
//
// Page: an "Add item" button + a list with Item 1, Item 2. Clicking "Add item"
// PREPENDS a new "Item 3 (new)" button BEFORE Item 1 - exactly the order shift
// that breaks positional refs. We then verify:
//   - Item 1 and Item 2 keep the SAME refs after the re-render.
//   - The new Item 3 gets a fresh ref (not a reused one).
//   - Reading the OLD Item 1 ref still returns "Item 1" (not "Item 3 (new)"),
//     proving the ref did not retarget to the newly-prepended control.
func TestStableRefsAcrossReRender(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	url := servePage(t, `<!doctype html><html><head><title>Refs</title></head><body>
		<h1>Ref stability</h1>
		<button id="add">Add item</button>
		<ul id="list">
			<li><button id="i1">Item 1</button></li>
			<li><button id="i2">Item 2</button></li>
		</ul>
		<script>
		document.getElementById('add').addEventListener('click', function() {
			var ul = document.getElementById('list');
			var li = document.createElement('li');
			var b = document.createElement('button');
			b.textContent = 'Item 3 (new)';
			li.appendChild(b);
			ul.prepend(li);  // new item goes FIRST -> shifts positional order
		});
		</script>
		</body></html>`)
	callTool(t, sess, ctx, "navigate", map[string]any{"url": url})
	callTool(t, sess, ctx, "see", map[string]any{})

	// Record the refs of Item 1 and Item 2 before the mutation.
	item1Before := firstRef(callTool(t, sess, ctx, "find", map[string]any{"text": "Item 1", "exact": true}))
	item2Before := firstRef(callTool(t, sess, ctx, "find", map[string]any{"text": "Item 2", "exact": true}))
	addBefore := firstRef(callTool(t, sess, ctx, "find", map[string]any{"text": "Add item", "exact": true}))
	t.Logf("before: Item 1=%q Item 2=%q Add=%q", item1Before, item2Before, addBefore)
	if item1Before == "" || item2Before == "" || addBefore == "" {
		t.Fatalf("missing refs before mutation: i1=%q i2=%q add=%q", item1Before, item2Before, addBefore)
	}

	// Click "Add item" -> re-render that PREPENDS a new button before Item 1.
	clickRes, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: "act", Arguments: map[string]any{"intent": "Add item"}})
	if err != nil {
		t.Fatalf("act Add item: %v", err)
	}
	if resIsErr(clickRes) {
		t.Fatalf("act Add item errored: %s", oneLine(contentText(clickRes)))
	}

	// After the mutation, Item 1 + Item 2 MUST keep their refs.
	callTool(t, sess, ctx, "see", map[string]any{})
	item1After := firstRef(callTool(t, sess, ctx, "find", map[string]any{"text": "Item 1", "exact": true}))
	item2After := firstRef(callTool(t, sess, ctx, "find", map[string]any{"text": "Item 2", "exact": true}))
	addAfter := firstRef(callTool(t, sess, ctx, "find", map[string]any{"text": "Add item", "exact": true}))
	item3After := firstRef(callTool(t, sess, ctx, "find", map[string]any{"text": "Item 3"}))
	t.Logf("after:  Item 1=%q Item 2=%q Add=%q Item3=%q", item1After, item2After, addAfter, item3After)

	if item1After != item1Before {
		t.Errorf("Item 1 ref drifted across re-render: %q -> %q (must stay stable - same DOM node)", item1Before, item1After)
	}
	if item2After != item2Before {
		t.Errorf("Item 2 ref drifted across re-render: %q -> %q (must stay stable - same DOM node)", item2Before, item2After)
	}
	if addAfter != addBefore {
		t.Errorf("Add item ref drifted across re-render: %q -> %q (must stay stable)", addBefore, addAfter)
	}
	if item3After == "" {
		t.Fatal("new Item 3 got no ref after the re-render")
	}
	if item3After == item1Before || item3After == item2Before || item3After == addBefore {
		t.Errorf("new Item 3 ref %q reused an existing node's ref (must be fresh - monotonic counter)", item3After)
	}

	// The decisive check: the OLD Item 1 ref must still resolve to Item 1, NOT
	// to the newly-prepended "Item 3 (new)". With positional refs this would
	// silently retarget (Item 1's old ref would now point at the new first
	// button). Read by ref and confirm the text.
	read1 := callTool(t, sess, ctx, "read", map[string]any{"ref": item1Before})
	t.Logf("read old Item 1 ref %q after re-render: %s", item1Before, oneLine(read1))
	if !strings.Contains(read1, "Item 1") || strings.Contains(read1, "Item 3") {
		t.Errorf("old Item 1 ref %q retargeted to a different control after re-render (read=%q) - this is the silent-wrong-click failure mode", item1Before, oneLine(read1))
	}
}
