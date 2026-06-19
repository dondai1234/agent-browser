package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callLog calls a tool and logs its response size (to track efficiency per
// step) + a short snippet. Fails on transport/tool errors.
func callLog(t *testing.T, sess *mcp.ClientSession, ctx context.Context, name string, args map[string]any) string {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	text := contentText(res)
	if res.IsError {
		t.Fatalf("%s error: %s", name, text)
	}
	t.Logf("%s -> %d chars (~%d tok): %s", name, len(text), len(text)/4, snippet(text, 180))
	return text
}

func snippet(s string, n int) string {
	if len(s) <= n {
		return strings.ReplaceAll(s, "\n", " ")
	}
	return strings.ReplaceAll(s[:n], "\n", " ") + "..."
}

func firstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}

// allRefs parses every [rN] from a find/summary output, in order.
func allRefs(out string) []string {
	var refs []string
	for _, line := range strings.Split(out, "\n") {
		start := strings.Index(line, "[r")
		if start < 0 {
			continue
		}
		end := strings.IndexByte(line[start+1:], ']')
		if end < 0 {
			continue
		}
		refs = append(refs, line[start+1:start+1+end])
	}
	return refs
}

// TestEffectivenessSaucedemoPurchase: the marquee effectiveness test. A full
// real purchase flow on a React SPA using ONLY the efficient tools (minimal +
// find + fill + select + click + delta + read): log in, sort by price, add the
// cheapest + the priciest item (disambiguating same-named "Add to cart"
// buttons by ref order), open the cart, and verify the two specific items +
// the total are present.
func TestEffectivenessSaucedemoPurchase(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	// Login.
	callLog(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	tb := callLog(t, sess, ctx, "find", map[string]any{"role": "textbox"})
	callLog(t, sess, ctx, "fill", map[string]any{"ref": refFor(t, tb, "Username"), "value": "standard_user"})
	callLog(t, sess, ctx, "fill", map[string]any{"ref": refFor(t, tb, "Password"), "value": "secret_sauce"})
	btn := callLog(t, sess, ctx, "find", map[string]any{"role": "button"})
	callLog(t, sess, ctx, "click", map[string]any{"ref": refFor(t, btn, "Login")})

	// Sort by price low -> high.
	combos := callLog(t, sess, ctx, "find", map[string]any{"role": "combobox"})
	callLog(t, sess, ctx, "select", map[string]any{"ref": refFor(t, combos, "combobox"), "value": "Price (low to high)"})

	// Inspect the post-select tree to see what survived.
	seeAfter := callLog(t, sess, ctx, "see", map[string]any{"level": "summary"})
	t.Logf("post-select summary (first 25 lines):\n%s", firstLines(seeAfter, 25))

	// Find the Add-to-cart buttons (in sorted order: first = cheapest, last = priciest).
	adds := callLog(t, sess, ctx, "find", map[string]any{"role": "button", "text": "Add to cart"})
	refs := allRefs(adds)
	t.Logf("add-to-cart refs (sorted order): %v", refs)
	if len(refs) < 2 {
		t.Fatalf("expected >=2 Add to cart buttons, got %d (%q)", len(refs), adds)
	}
	callLog(t, sess, ctx, "click", map[string]any{"ref": refs[0]})           // cheapest
	callLog(t, sess, ctx, "click", map[string]any{"ref": refs[len(refs)-1]}) // priciest

	// Open the cart and verify the two specific items + a total line.
	callLog(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com/cart.html"})
	cart := callLog(t, sess, ctx, "read", map[string]any{})
	t.Logf("cart:\n%s", cart)
	if !strings.Contains(cart, "Onesie") {
		t.Errorf("cheapest item (Sauce Labs Onesie) missing from cart: %q", cart)
	}
	if !strings.Contains(cart, "Fleece Jacket") {
		t.Errorf("priciest item (Sauce Labs Fleece Jacket) missing from cart: %q", cart)
	}
	if !strings.Contains(cart, "$7.99") {
		t.Errorf("cheapest price missing: %q", cart)
	}
	if !strings.Contains(cart, "$49.99") {
		t.Errorf("priciest price missing: %q", cart)
	}
	if !strings.Contains(cart, "Checkout") {
		t.Errorf("no Checkout button in cart: %q", cart)
	}
}

// TestEffectivenessGitHubExtract: extract data from a real heavy GitHub repo
// page (shadow DOM web components, many elements). Can we read the description
// + find the star button?
func TestEffectivenessGitHubExtract(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callLog(t, sess, ctx, "navigate", map[string]any{"url": "https://github.com/chromedp/chromedp"})
	body := callLog(t, sess, ctx, "read", map[string]any{})
	t.Logf("github body snippet:\n%s", snippet(body, 600))
	if !strings.Contains(body, "chromedp") {
		t.Errorf("repo name not in read: %q", snippet(body, 300))
	}
	links := callLog(t, sess, ctx, "find", map[string]any{"role": "link", "text": "Star"})
	t.Logf("star link search: %s", links)
}

// TestEffectivenessWikipediaExtract: extract a specific fact (capital) from a
// huge real article. Does read's 8000-char truncation still capture the
// infobox fact?
func TestEffectivenessWikipediaExtract(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callLog(t, sess, ctx, "navigate", map[string]any{"url": "https://en.wikipedia.org/wiki/Nepal"})
	body := callLog(t, sess, ctx, "read", map[string]any{})
	if !strings.Contains(body, "Kathmandu") {
		t.Errorf("capital 'Kathmandu' not in read (truncation?): %q", snippet(body, 400))
	}
}

// TestEffectivenessOffscreenFindClick: find + click an element that's beyond
// the viewport WITHOUT scrolling (the a11y tree is full-page, not viewport-
// bound). On HN, the "More" pagination link is off-screen; find it and click
// to reach page 2.
func TestEffectivenessOffscreenFindClick(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callLog(t, sess, ctx, "navigate", map[string]any{"url": "https://news.ycombinator.com"})
	more := callLog(t, sess, ctx, "find", map[string]any{"role": "link", "text": "More", "exact": true})
	ref := refFor(t, more, "More")
	click := callLog(t, sess, ctx, "click", map[string]any{"ref": ref})
	if !strings.Contains(click, "navigated") || !strings.Contains(click, "news") {
		t.Errorf("off-screen More click did not navigate to page 2: %q", click)
	}
}
