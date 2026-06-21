package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// callToolResult returns the raw result (without fataling on isError) so tests
// can assert on the error path (ambiguous/no-match intents return isError=true
// with a candidate list or a not-found message).
func callToolResult(t *testing.T, sess *mcp.ClientSession, ctx context.Context, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := sess.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	return res
}

// saucedemoLoginViaAct reaches the inventory page using only intent-first act
// calls (the v2 flow), shared by the act tests.
func saucedemoLoginViaAct(t *testing.T, sess *mcp.ClientSession, ctx context.Context) {
	t.Helper()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Username", "value": "standard_user"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Password", "value": "secret_sauce"})
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Login"})
}

// TestRealWorldActLogin: the v2 headline flow - log in to a real SPA using only
// intent names, no refs. act "Username" fills the username field, act "Password"
// fills the password, act "Login" clicks the button and navigates to inventory.
// Collapses navigate+find+fill+fill+find+click+see into navigate+act+act+act.
func TestRealWorldActLogin(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})

	u := callTool(t, sess, ctx, "act", map[string]any{"intent": "Username", "value": "standard_user"})
	t.Logf("act Username:\n%s", u)
	if !strings.Contains(u, "act ") || !strings.Contains(u, "textbox") || !strings.Contains(u, "(fill)") {
		t.Errorf("act Username should resolve to a textbox + fill, got:\n%s", u)
	}
	callTool(t, sess, ctx, "act", map[string]any{"intent": "Password", "value": "secret_sauce"})

	login := callTool(t, sess, ctx, "act", map[string]any{"intent": "Login"})
	t.Logf("act Login:\n%s", login)
	if !strings.Contains(login, "act ") || !strings.Contains(login, "button") || !strings.Contains(login, "(click)") {
		t.Errorf("act Login should resolve to a button + click, got:\n%s", login)
	}
	if !strings.Contains(login, "verdict: navigated") || !strings.Contains(login, "inventory") {
		t.Errorf("act Login should navigate to inventory, got:\n%s", login)
	}
}

// TestRealWorldActAmbiguous: an intent matching several identical controls must
// NOT guess - it returns isError with the ranked candidate list, so the agent
// disambiguates with nth/role instead of clicking the wrong one.
func TestRealWorldActAmbiguous(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	saucedemoLoginViaAct(t, sess, ctx)

	res := callToolResult(t, sess, ctx, "act", map[string]any{"intent": "Add to cart"})
	text := contentText(res)
	t.Logf("act Add to cart (ambiguous):\n%s", text)
	if !res.IsError {
		t.Errorf("ambiguous act must be isError (don't guess), got:\n%s", text)
	}
	if !strings.Contains(text, "matches:") {
		t.Errorf("ambiguous act should list candidates, got:\n%s", text)
	}
	if strings.Count(text, "Add to cart") < 2 {
		t.Errorf("should list multiple Add to cart candidates (ambiguous = don't guess), got %d occurrences:\n%s", strings.Count(text, "Add to cart"), text)
	}
}

// TestRealWorldActNth: nth disambiguates the ambiguous case and performs the
// action on the chosen match, returning a verdict (the button flips to Remove).
func TestRealWorldActNth(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	saucedemoLoginViaAct(t, sess, ctx)

	out := callTool(t, sess, ctx, "act", map[string]any{"intent": "Add to cart", "nth": 1})
	t.Logf("act Add to cart nth=1:\n%s", out)
	if !strings.Contains(out, "act ") || !strings.Contains(out, "(click)") {
		t.Errorf("nth should act on the first match, got:\n%s", out)
	}
	if strings.Contains(out, "no visible effect") {
		t.Errorf("nth=1 click should have an effect (button -> Remove), got:\n%s", out)
	}
}

// TestRealWorldActNoMatch: an intent that matches nothing returns isError with
// a helpful not-found message (not a crash, not a wrong action).
func TestRealWorldActNoMatch(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "navigate", map[string]any{"url": "https://www.saucedemo.com"})

	res := callToolResult(t, sess, ctx, "act", map[string]any{"intent": "Checkout now"})
	text := contentText(res)
	if !res.IsError {
		t.Errorf("no-match act must be isError, got:\n%s", text)
	}
	if !strings.Contains(text, "no element named") {
		t.Errorf("no-match should say 'no element named', got:\n%s", text)
	}
}
