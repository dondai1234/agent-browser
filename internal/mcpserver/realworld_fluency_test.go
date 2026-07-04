package mcpserver

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestStealthHardening: the 2026 stealth patches survive a real page load +
// close the known tells. Runs against example.com (no bot protection) so the
// assertions are about the spoofed environment, not the site.
func TestStealthHardening(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": "https://example.com"})

	out := callTool(t, sess, ctx, "js", map[string]any{
		"script": `return {
  notif: Notification.permission,
  perm: (navigator.permissions && navigator.permissions.query) ? (await navigator.permissions.query({name:'notifications'})).state : 'no-permissions-api',
  outerW: window.outerWidth,
  outerH: window.outerHeight,
  conn: navigator.connection ? navigator.connection.effectiveType : 'undefined',
  webdriver: String(navigator.webdriver)
}`,
	})
	t.Logf("stealth probes: %s", out)
	// The consistent pair: Notification.permission='default' <-> query='prompt'
	// (the Permissions API expresses the unrequested state as 'prompt', not 'default').
	if !strings.Contains(out, `"notif":"default"`) {
		t.Fatalf("Notification.permission should be 'default', got: %s", out)
	}
	if !strings.Contains(out, `"perm":"prompt"`) {
		t.Fatalf("permissions.query for notifications should return 'prompt' (consistent with Notification='default'), got: %s", out)
	}
	if strings.Contains(out, `"outerW":0`) {
		t.Fatalf("outerWidth should be nonzero (headless=0 is a bot tell), got: %s", out)
	}
	if strings.Contains(out, `"conn":"undefined"`) {
		t.Fatalf("navigator.connection should be defined (undefined in headless is a tell), got: %s", out)
	}
	// webdriver===true is the automation tell; false (real non-automated Chrome) is fine.
	if strings.Contains(out, `"webdriver":"true"`) {
		t.Fatalf("navigator.webdriver should not be 'true' (the automation tell), got: %s", out)
	}
}

// cookieBannerFixture is a faithful OneTrust-style banner DOM: the real CMP
// container id, a reject handler that hides the banner (as OneTrust does), and
// a page body the banner overlays. The dismiss engine should click reject +
// free the page. Served locally so the assertion is deterministic (a live CMP
// site is geo/bot-flaky and may not show the banner in headless).
const cookieBannerFixture = `<!doctype html><html><head><title>Shop</title></head>
<body>
<h1>Welcome to the shop</h1>
<p>Real page content the agent wants to read.</p>
<div id="onetrust-banner-sdk" style="position:fixed;bottom:0;left:0;right:0;padding:16px;background:#fff;border-top:1px solid #ccc;z-index:9999">
  <p>We use cookies to improve your experience.</p>
  <button id="onetrust-accept-all-handler">Accept All</button>
  <button id="onetrust-reject-all-handler" onclick="document.getElementById('onetrust-banner-sdk').style.display='none'">Reject All</button>
</div>
</body></html>`

// TestCookieDismissLocal: a OneTrust-style banner is auto-dismissed on nav
// (reject preferred), the nav verdict names the dismiss, and the banner is
// gone from a follow-up see (the agent isn't left fighting the overlay).
func TestCookieDismissLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(cookieBannerFixture))
	}))
	defer srv.Close()

	sess, ctx, cleanup := realWorldSetupWithFlags(t, "--allow-insecure-schemes")
	defer cleanup()

	nav := callTool(t, sess, ctx, "nav", map[string]any{"url": srv.URL})
	if !strings.Contains(nav, "consent:") {
		t.Fatalf("nav orientation should surface the auto-dismiss (consent: line), got: %s", nav)
	}
	t.Logf("nav verdict: %s", nav)

	// The banner is gone: no Accept/Reject in refs, no 'We use cookies' in text.
	refs := callTool(t, sess, ctx, "see", map[string]any{"level": "refs"})
	if strings.Contains(refs, "Accept All") || strings.Contains(refs, "Reject All") {
		t.Fatalf("banner buttons should be gone after dismiss, got refs:\n%s", refs)
	}
	body := callTool(t, sess, ctx, "see", map[string]any{"level": "text"})
	if strings.Contains(body, "We use cookies") {
		t.Fatalf("cookie banner should be dismissed (text gone), but 'We use cookies' still present:\n%s", body)
	}
	if !strings.Contains(body, "Real page content") {
		t.Fatalf("real page content should be visible after dismiss, got: %s", body)
	}
}

// TestCookieDismissDisabled: with --no-cookie-dismiss the banner is left alone
// (the opt-out works + doesn't touch the page). Proves the flag gates the
// behavior, so a user who wants the banner stays in control.
func TestCookieDismissDisabled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(cookieBannerFixture))
	}))
	defer srv.Close()

	sess, ctx, cleanup := realWorldSetupWithFlags(t, "--allow-insecure-schemes", "--no-cookie-dismiss")
	defer cleanup()

	nav := callTool(t, sess, ctx, "nav", map[string]any{"url": srv.URL})
	if strings.Contains(nav, "consent:") {
		t.Fatalf("nav should NOT surface a dismiss when --no-cookie-dismiss is set, got: %s", nav)
	}
	body := callTool(t, sess, ctx, "see", map[string]any{"level": "text"})
	if !strings.Contains(body, "We use cookies") {
		t.Fatalf("with --no-cookie-dismiss the banner should remain, got: %s", body)
	}
}

// TestLoginSaucedemo: a REAL public single-step login. standard_user logs in +
// lands on the inventory page. The verdict is state-verified (the silent-
// failure guard: we check the page moved off the login form, not the return).
func TestLoginSaucedemo(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	out := callTool(t, sess, ctx, "login", map[string]any{
		"url":      "https://www.saucedemo.com/",
		"username": "standard_user",
		"password": "secret_sauce",
	})
	if !strings.HasPrefix(out, "logged in") {
		t.Fatalf("saucedemo login should succeed (state-verified 'logged in'), got: %s", out)
	}
	if !strings.Contains(out, "/inventory") {
		t.Fatalf("should land on /inventory.html after saucedemo login, got: %s", out)
	}
	t.Logf("saucedemo login: %s", out)
}

// TestLoginWrongPassword: a REAL login with wrong creds. The verdict reports
// the error state (not a silent success), proving the state-verification
// catches a failed login instead of hiding it.
func TestLoginWrongPassword(t *testing.T) {
	sess, ctx, cleanup := realWorldSetup(t)
	defer cleanup()

	out := callTool(t, sess, ctx, "login", map[string]any{
		"url":      "https://www.saucedemo.com/",
		"username": "standard_user",
		"password": "wrong_password",
	})
	// saucedemo shows an error bubble; the form stays. Either an explicit
	// "error:" verdict or "still on login page" is an honest failure signal -
	// the one thing that must NOT happen is a "logged in" lie.
	if strings.HasPrefix(out, "logged in") {
		t.Fatalf("wrong password must NOT report 'logged in' (silent-failure guard failed), got: %s", out)
	}
	if !strings.Contains(out, "error") && !strings.Contains(out, "still on login page") {
		t.Fatalf("wrong password should report error/still-on-login, got: %s", out)
	}
	t.Logf("wrong-password verdict: %s", out)
}

// multiStepLoginFixture is a faithful 2-step login (the Google/Microsoft
// pattern): username + Next, then a password field + Sign in appears. Correct
// creds land on a logged-in state (logout link + welcome text). No public
// multi-step login is both bot-friendly and account-free, so this local
// fixture is the controlled, deterministic test of the multi-step orchestration
// (the genuinely novel logic).
const multiStepLoginFixture = `<!doctype html><html><head><title>Sign in</title></head>
<body>
<div id="step1">
  <h1>Sign in</h1>
  <input id="email" type="email" autocomplete="username" placeholder="Email" aria-label="Email">
  <button id="nextBtn" onclick="nextStep()">Next</button>
</div>
<div id="step2" style="display:none">
  <h1>Welcome</h1>
  <input id="pwd" type="password" autocomplete="current-password" placeholder="Password" aria-label="Password">
  <button id="signinBtn" onclick="doLogin()">Sign in</button>
  <p id="err" style="display:none;color:red">Wrong password. Try again.</p>
</div>
<div id="logged" style="display:none">
  <h1>Welcome, you are signed in</h1>
  <a href="/logout" id="logout">Log out</a>
</div>
<script>
var USER = "agent@example.com", PASS = "correct123";
function nextStep() {
  var e = document.getElementById('email').value;
  if (!e) { return; }
  document.getElementById('step1').style.display = 'none';
  document.getElementById('step2').style.display = 'block';
}
function doLogin() {
  var e = document.getElementById('email').value, p = document.getElementById('pwd').value;
  if (e === USER && p === PASS) {
    document.getElementById('step2').style.display = 'none';
    document.getElementById('logged').style.display = 'block';
  } else {
    document.getElementById('err').style.display = 'block';
  }
}
</script>
</body></html>`

// TestLoginMultiStepLocal: username -> Next -> password appears -> Sign in.
// The login tool handles both steps in ONE call + reports a state-verified
// "logged in". Proves the multi-step orchestration (detect -> fill user ->
// click Next -> wait -> re-detect password -> fill -> submit -> verify).
func TestLoginMultiStepLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(multiStepLoginFixture))
	}))
	defer srv.Close()

	sess, ctx, cleanup := realWorldSetupWithFlags(t, "--allow-insecure-schemes")
	defer cleanup()

	out := callTool(t, sess, ctx, "login", map[string]any{
		"url":      srv.URL,
		"username": "agent@example.com",
		"password": "correct123",
	})
	if !strings.HasPrefix(out, "logged in") {
		t.Fatalf("multi-step login should succeed (state-verified), got: %s", out)
	}
	t.Logf("multi-step login: %s", out)
}

// TestLoginMultiStepWrong: multi-step with the right username but wrong
// password lands on the error state (not a silent success).
func TestLoginMultiStepWrong(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(multiStepLoginFixture))
	}))
	defer srv.Close()

	sess, ctx, cleanup := realWorldSetupWithFlags(t, "--allow-insecure-schemes")
	defer cleanup()

	out := callTool(t, sess, ctx, "login", map[string]any{
		"url":      srv.URL,
		"username": "agent@example.com",
		"password": "wrongpass",
	})
	if strings.HasPrefix(out, "logged in") {
		t.Fatalf("wrong multi-step password must NOT report 'logged in', got: %s", out)
	}
	if !strings.Contains(out, "error") && !strings.Contains(out, "still on login page") {
		t.Fatalf("wrong multi-step password should report error/still-on-login, got: %s", out)
	}
	t.Logf("multi-step wrong verdict: %s", out)
}

// comboboxFixture is the W3C button+listbox combobox pattern (aria-haspopup=
// listbox, aria-controls -> a listbox of role=option), the case a native
// <select> can't express and a fill can't reach. Selecting an option requires
// open -> wait -> click-option (the open-select dance).
const comboboxFixture = `<!doctype html><html><head><title>Pick</title></head>
<body>
<h1>Choose a country</h1>
<button id="cb" role="combobox" aria-haspopup="listbox" aria-expanded="false" aria-controls="lb" onclick="toggle()">Select a country</button>
<ul id="lb" role="listbox" style="display:none;list-style:none;padding:0;margin:4px 0;border:1px solid #ccc">
  <li role="option" onclick="pick(this)">Nepal</li>
  <li role="option" onclick="pick(this)">India</li>
  <li role="option" onclick="pick(this)">Japan</li>
  <li role="option" onclick="pick(this)">Brazil</li>
</ul>
<p>Selected: <span id="out">none</span></p>
<script>
function toggle() {
  var lb = document.getElementById('lb'), cb = document.getElementById('cb');
  var open = lb.style.display === 'none';
  lb.style.display = open ? 'block' : 'none';
  cb.setAttribute('aria-expanded', open ? 'true' : 'false');
}
function pick(el) {
  document.getElementById('out').textContent = el.textContent;
  document.getElementById('lb').style.display = 'none';
  document.getElementById('cb').setAttribute('aria-expanded','false');
  document.getElementById('cb').textContent = el.textContent;
}
</script>
</body></html>`

// TestComboboxOpenSelectLocal: act value="Japan" on a button+listbox combobox
// runs the open-select dance + the option is selected (proven by reading the
// page's selected span, not the return status - the silent-failure guard).
func TestComboboxOpenSelectLocal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.Write([]byte(comboboxFixture))
	}))
	defer srv.Close()

	sess, ctx, cleanup := realWorldSetupWithFlags(t, "--allow-insecure-schemes")
	defer cleanup()
	callTool(t, sess, ctx, "nav", map[string]any{"url": srv.URL})

	// Debug: what does the a11y tree expose for the combobox?
	refs := callTool(t, sess, ctx, "see", map[string]any{"level": "refs"})
	t.Logf("combobox fixture refs:\n%s", refs)

	// Target the combobox by its a11y name + pass the value to select.
	out := callTool(t, sess, ctx, "act", map[string]any{
		"intent": "Select a country",
		"value":  "Japan",
	})
	if strings.Contains(out, "no visible effect") {
		t.Fatalf("open-select should not read 'no visible effect', got: %s", out)
	}

	// State-verified: read the page's selected span (not the return status).
	chosen := callTool(t, sess, ctx, "js", map[string]any{"script": `return text('#out')`})
	if strings.TrimSpace(chosen) != "Japan" {
		t.Fatalf("combobox should have selected 'Japan', got: %q", chosen)
	}
	t.Logf("combobox open-select verdict: %s | selected=%q", out, chosen)
}
