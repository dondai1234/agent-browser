package browser

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

// LoginArgs is the input to the universal login flow. Username + password are
// required; url optionally navigates first. One call does the whole dance that
// today takes 6-10 round-trips and fails silently: detect the form, fill
// username, handle single- vs multi-step (Google/Microsoft: password appears
// after Next), submit, then verify the RESULTING STATE (not the return status)
// so a silent failure is reported, not hidden.
type LoginArgs struct {
	Username string
	Password string
	URL      string // optional: navigate here first (lazy-launches Chrome)
}

// LoginResult carries a state-verified verdict + the SSO buttons present (which
// we report but never auto-click - they open a third-party flow we can't drive).
type LoginResult struct {
	Verdict        string
	URL            string
	OAuth          []string // SSO button labels present
	RememberMe     bool     // a "remember me" / "keep me signed in" checkbox was detected
	ForgotPassword string   // "forgot password" / "reset password" link text if found
	SSORedirect    string   // if the URL moved to a different domain after submit
}

// loginStateJS reads the page's login-relevant state in one evaluate: the
// visible username/password/submit selectors, SSO buttons, captcha/2FA/error
// signals, and whether the login form is gone + an account element is present.
// Called repeatedly by the orchestrator (before, after username, after submit)
// so multi-step + post-submit state are detected from live DOM, not a stale
// snapshot. Returns "" fields when not found. uniqueSel gives a stable CSS
// selector the Go side resolves to a remote object id for fill/click.
const loginStateJS = `(() => {
  var vis = (el) => {
    if (!el || !el.isConnected) return false;
    var s = getComputedStyle(el);
    if (s.display === 'none' || s.visibility === 'hidden' || Number(s.opacity) === 0) return false;
    if (el.disabled || el.getAttribute('aria-hidden') === 'true') return false;
    var r = el.getBoundingClientRect();
    return r.width > 1 && r.height > 1 && r.bottom > 0 && r.right > 0;
  };
  var uniqueSel = (el) => {
    if (!el) return '';
    if (el.id && document.querySelectorAll('#' + CSS.escape(el.id)).length === 1) { try { return '#' + CSS.escape(el.id); } catch(e){} }
    var parts = [];
    while (el && el.nodeType === 1 && el !== document.documentElement) {
      var p = el.parentNode;
      if (!p) { parts.unshift(el.tagName.toLowerCase()); break; }
      var idx = Array.prototype.indexOf.call(p.children, el) + 1;
      parts.unshift(el.tagName.toLowerCase() + ':nth-child(' + idx + ')');
      el = p;
      if (el && el.id && document.querySelectorAll('#' + CSS.escape(el.id)).length === 1) { try { parts.unshift('#' + CSS.escape(el.id)); break; } catch(e){} }
    }
    return parts.join(' > ');
  };
  var txt = (el) => (el.innerText || el.textContent || '').replace(/\s+/g, ' ').trim();
  var sig = (el) => {
    var s = txt(el).toLowerCase();
    var a = [el.getAttribute('aria-label'), el.getAttribute('title'), el.getAttribute('placeholder'), el.getAttribute('name'), el.id].filter(Boolean).join(' ').toLowerCase();
    return s + ' ' + a;
  };
  var matchTxt = (s, re) => re.test(s);

  var passEl = null, userEl = null, submitEl = null;
  // password: any visible type=password (autocomplete new-password counts too for signup-as-login)
  document.querySelectorAll('input[type="password"]').forEach(el => { if (!passEl && vis(el)) passEl = el; });

  // username, priority order
  var pick = (sel) => { var e = document.querySelector(sel); return (e && vis(e)) ? e : null; };
  var byAttr = (re) => {
    var out = null;
    document.querySelectorAll('input:not([type="password"]):not([type="hidden"]):not([type="submit"]):not([type="button"]):not([type="checkbox"]):not([type="radio"]):not([type="file"]):not([type="range"]):not([type="color"])').forEach(el => {
      if (out || !vis(el)) return;
      if (el.getAttribute('type') === 'search' || el.getAttribute('role') === 'searchbox') return;
      if (matchTxt(sig(el), re)) out = el;
    });
    return out;
  };
  userEl = pick('input[autocomplete="username"]')
        || pick('input[autocomplete*="username" i]')
        || pick('input[type="email"]')
        || byAttr(/\b(user|email|login|identifier|account|username|user ?id|e ?mail)\b/i)
        || null;
  // fallback: first visible text-like input in the same form as the password
  if (!userEl && passEl) {
    var form = passEl.closest('form');
    if (form) {
      var inputs = form.querySelectorAll('input:not([type="password"]):not([type="hidden"]):not([type="submit"]):not([type="button"]):not([type="checkbox"]):not([type="radio"]):not([type="file"])');
      for (var i = 0; i < inputs.length; i++) { if (vis(inputs[i]) && inputs[i].getAttribute('type') !== 'search') { userEl = inputs[i]; break; } }
    }
  }

  // submit: in the same form as user/pass, else by text match anywhere
  var form = (userEl || passEl) ? (userEl || passEl).closest('form') : null;
  if (form) {
    submitEl = form.querySelector('button[type="submit"], input[type="submit"]') || null;
    if (!submitEl) {
      var btns = form.querySelectorAll('button, [role="button"], input[type="button"]');
      var re = /(sign\s?in|log\s?in|login|next|continue|submit|enter|go)/i;
      for (var i = 0; i < btns.length; i++) { if (vis(btns[i]) && re.test(txt(btns[i]) + ' ' + (btns[i].getAttribute('aria-label')||''))) { submitEl = btns[i]; break; } }
    }
  }
  if (!submitEl) {
    var re2 = /^(sign\s?in|log\s?in|login|next|continue|submit|enter)$/i;
    document.querySelectorAll('button, [role="button"], input[type="submit"], input[type="button"]').forEach(el => {
      if (submitEl || !vis(el)) return;
      if (matchTxt(txt(el), re2)) submitEl = el;
    });
  }

  // OAuth/SSO buttons (report only - never auto-click)
  var oauth = [];
  var ore = /(sign\s?in with|continue with|log\s?in with)\s+(google|apple|facebook|microsoft|github|gitlab|sso|x|twitter)/i;
  document.querySelectorAll('button, a[role="button"], a[href]').forEach(el => {
    if (!vis(el)) return;
    var s = sig(el);
    var m = s.match(ore);
    if (m) { var label = txt(el) || (m[0]); if (label && oauth.indexOf(label) < 0 && oauth.length < 6) oauth.push(label); }
  });

  // captcha
  var captcha = !!(document.querySelector('.g-recaptcha, .h-captcha, .cf-turnstile, iframe[src*="recaptcha"], iframe[src*="hcaptcha"], iframe[src*="challenges.cloudflare.com"]'));

  // 2FA / OTP
  var twoFA = false;
  if (document.querySelector('input[autocomplete*="one-time-code" i], input[name*="code" i], input[name*="otp" i], input[inputmode="numeric"]')) twoFA = true;
  if (!twoFA && matchTxt(document.body ? document.body.innerText : '', /(two-factor|2fa|two factor|verification code|enter the code|authentication code|enter the \d|we sent (you )?a code|authenticator)/i)) twoFA = true;

  // error message near the form (role=alert / .error / pattern)
  var err = '';
  var alert = document.querySelector('[role="alert"]');
  if (alert && vis(alert)) err = txt(alert);
  if (!err) { var e2 = document.querySelector('.error, .invalid-feedback, .field-error, .form-error, [class*="error" i]'); if (e2 && vis(e2)) { var t = txt(e2); if (t && t.length < 200) err = t; } }
  if (!err) {
    var body = document.body ? document.body.innerText : '';
    var em = body.match(/(incorrect|invalid|wrong (password|email|username)|account not found|does not exist|couldn'?t find|too many (attempts|tries)|please try again|check your (email|password)|password is incorrect|email or password|do not match|don'?t match|mismatch|credentials are incorrect|invalid (username|password|credentials))/i);
    if (em) err = em[0];
  }

  // account-present (logged-in signal): logout/account/profile/avatar/welcome
  var account = false;
  document.querySelectorAll('a[href], button, [role="button"]').forEach(el => {
    if (account || !vis(el)) return;
    if (matchTxt(sig(el), /\b(log\s?out|sign\s?out|log out|my account|account settings|your account|profile|dashboard)\b/i)) account = true;
    var href = el.getAttribute('href') || '';
    if (/logout|signout|\/account|\/profile|\/dashboard/i.test(href)) account = true;
  });
  if (matchTxt(document.body ? document.body.innerText : '', /^(hi|hello|welcome),\s/i)) account = true;

  // "remember me" / "keep me signed in" checkbox near the form
  var rememberMe = false;
  document.querySelectorAll('input[type="checkbox"]').forEach(function(el) {
    if (rememberMe || !vis(el)) return;
    var s = sig(el);
    if (/remember|keep me|stay|keep.*sign|remember.*me/i.test(s)) rememberMe = true;
  });
  // Also check labels wrapping a checkbox
  document.querySelectorAll('label').forEach(function(el) {
    if (rememberMe || !vis(el)) return;
    if (/remember|keep me|stay|keep.*sign/i.test(txt(el))) {
      var cb = el.querySelector('input[type="checkbox"]');
      if (cb && vis(cb)) rememberMe = true;
    }
  });

  // "forgot password" / "reset password" link
  var forgotPassword = '';
  document.querySelectorAll('a, button, [role="link"]').forEach(function(el) {
    if (forgotPassword || !vis(el)) return;
    var s = txt(el).toLowerCase();
    if (/forgot|reset|recover|change.*(password|pin)/i.test(s)) forgotPassword = txt(el);
  });

  return {
    userSel: uniqueSel(userEl), passSel: uniqueSel(passEl), submitSel: uniqueSel(submitEl),
    oauth: oauth, captcha: !!captcha, twoFA: !!twoFA,
    error: err ? String(err).slice(0, 180) : '',
    loginGone: !passEl && !userEl, account: account,
    rememberMe: rememberMe, forgotPassword: forgotPassword,
    url: location.href
  };
})()`

// loginState is the Go view of loginStateJS's JSON.
type loginState struct {
	UserSel        string   `json:"userSel"`
	PassSel        string   `json:"passSel"`
	SubmitSel      string   `json:"submitSel"`
	OAuth          []string `json:"oauth"`
	Captcha        bool     `json:"captcha"`
	TwoFA          bool     `json:"twoFA"`
	Error          string   `json:"error"`
	LoginGone      bool     `json:"loginGone"`
	Account        bool     `json:"account"`
	RememberMe     bool     `json:"rememberMe"`
	ForgotPassword string   `json:"forgotPassword"`
	URL            string   `json:"url"`
}

// readLoginStateLocked evaluates loginStateJS on the current tab. Caller holds s.mu.
func (s *Session) readLoginStateLocked(t *tab) (loginState, error) {
	var st loginState
	var raw []byte
	err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		res, exc, e := runtime.Evaluate(loginStateJS).WithReturnByValue(true).Do(ctx)
		if e != nil {
			return fmt.Errorf("read login state: %w", e)
		}
		if exc != nil {
			return fmt.Errorf("read login state: %s", exc.Text)
		}
		if res == nil || len(res.Value) == 0 {
			return fmt.Errorf("read login state: empty result")
		}
		raw = res.Value
		return nil
	}))
	if err != nil {
		return st, err
	}
	if json.Unmarshal(raw, &st) != nil {
		return st, fmt.Errorf("read login state: bad json")
	}
	return st, nil
}

// fillBySelectorLocked resolves a CSS selector to a remote id and fills it.
// Caller holds s.mu.
func (s *Session) fillBySelectorLocked(ctx context.Context, t *tab, sel, value string) error {
	id, err := s.selectorObjectIDLocked(ctx, sel)
	if err != nil {
		return fmt.Errorf("fill %q: %w", sel, err)
	}
	return s.fillNodeLocked(ctx, id, value)
}

// clickBySelectorLocked resolves a CSS selector and clicks it (real mouse).
func (s *Session) clickBySelectorLocked(ctx context.Context, t *tab, sel string) error {
	id, err := s.selectorObjectIDLocked(ctx, sel)
	if err != nil {
		return fmt.Errorf("click %q: %w", sel, err)
	}
	return s.clickNodeLocked(ctx, id)
}

// Login performs a universal one-call login. See LoginArgs.
func (s *Session) Login(a LoginArgs) (*LoginResult, error) {
	a.URL = strings.TrimSpace(a.URL)
	if strings.TrimSpace(a.Username) == "" || strings.TrimSpace(a.Password) == "" {
		return nil, fmt.Errorf("login needs username and password")
	}
	cleanURL := ""
	if a.URL != "" {
		c, err := ValidateURL(a.URL, s.AllowInsecureSchemes)
		if err != nil {
			return nil, err
		}
		cleanURL = c
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if cleanURL != "" {
		tree, err := s.navigateLocked(cleanURL, "")
		if err != nil {
			return nil, err
		}
		if tree != nil && tree.Challenge != "" {
			return &LoginResult{Verdict: "CHALLENGE: " + tree.Challenge, URL: tree.URL}, nil
		}
	} else {
		if err := s.ensureBrowserLocked(); err != nil {
			return nil, err
		}
		if s.curTabLocked() == nil || s.curTabLocked().tree == nil {
			return nil, ErrNoSnapshot
		}
	}
	t := s.curTabLocked()
	startURL := ""
	if t.tree != nil {
		startURL = t.tree.URL
	}

	// 1. Read the initial state.
	st, err := s.readLoginStateLocked(t)
	if err != nil {
		return nil, err
	}
	if st.Captcha {
		return &LoginResult{Verdict: "CHALLENGE: captcha present on the login page (needs a solver or a human)", URL: st.URL, OAuth: st.OAuth}, nil
	}
	if st.UserSel == "" {
		hint := "couldn't find a username field"
		if len(st.OAuth) > 0 {
			hint = "no password form found; only SSO buttons present - call act to click one"
		}
		return &LoginResult{Verdict: "no login form found: " + hint, URL: st.URL, OAuth: st.OAuth}, nil
	}

	// 2. Fill the username.
	if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
		return s.fillBySelectorLocked(ctx, t, st.UserSel, a.Username)
	})); err != nil {
		return nil, fmt.Errorf("fill username: %w", err)
	}
	time.Sleep(250 * time.Millisecond)

	// 3. Single-step (password visible now) vs multi-step (password appears after Next).
	if st.PassSel != "" {
		if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			if e := s.fillBySelectorLocked(ctx, t, st.PassSel, a.Password); e != nil {
				return e
			}
			time.Sleep(150 * time.Millisecond)
			if st.SubmitSel != "" {
				return s.clickBySelectorLocked(ctx, t, st.SubmitSel)
			}
			return nil
		})); err != nil {
			return nil, fmt.Errorf("login submit: %w", err)
		}
	} else {
		// multi-step: click Next, wait for the password field to appear.
		if st.SubmitSel == "" {
			return &LoginResult{Verdict: "still on login page: username filled but no submit/Next button found (multi-step); call see", URL: st.URL, OAuth: st.OAuth}, nil
		}
		if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			return s.clickBySelectorLocked(ctx, t, st.SubmitSel)
		})); err != nil {
			return nil, fmt.Errorf("login next: %w", err)
		}
		// wait for a password field to appear (up to 8s)
		deadline := time.Now().Add(8 * time.Second)
		var passSel string
		for time.Now().Before(deadline) {
			time.Sleep(350 * time.Millisecond)
			st2, e := s.readLoginStateLocked(t)
			if e == nil && st2.PassSel != "" {
				passSel = st2.PassSel
				break
			}
			if e == nil && st2.Captcha {
				return &LoginResult{Verdict: "CHALLENGE: captcha appeared after username submit", URL: st2.URL, OAuth: st2.OAuth}, nil
			}
		}
		if passSel == "" {
			return &LoginResult{Verdict: "still on login page: no password field appeared after Next (multi-step stalled; a challenge or an extra step may be in the way); call see", URL: st.URL, OAuth: st.OAuth}, nil
		}
		// re-read submit too (the second step has its own submit button)
		st3, _ := s.readLoginStateLocked(t)
		submit := st3.SubmitSel
		if submit == "" {
			submit = st.SubmitSel
		}
		if err := s.run(t, chromedp.ActionFunc(func(ctx context.Context) error {
			if e := s.fillBySelectorLocked(ctx, t, passSel, a.Password); e != nil {
				return e
			}
			time.Sleep(150 * time.Millisecond)
			if submit != "" {
				return s.clickBySelectorLocked(ctx, t, submit)
			}
			return nil
		})); err != nil {
			return nil, fmt.Errorf("login submit (multi-step): %w", err)
		}
	}

	// 4. Wait for the login to resolve (nav or state change), then read state.
	final := s.waitForLoginSettleLocked(t, 10*time.Second)

	// 5. State-verified verdict.
	res := &LoginResult{URL: final.URL, OAuth: final.OAuth, RememberMe: st.RememberMe, ForgotPassword: st.ForgotPassword}
	// SSO redirect: the URL moved to a DIFFERENT DOMAIN (not just a path) after
	// submit, which means the login redirected to a third-party auth flow.
	if ssoDomain := domainChanged(startURL, final.URL); ssoDomain != "" && !final.Account {
		res.SSORedirect = ssoDomain
		res.Verdict = fmt.Sprintf("SSO redirect to %s: the login redirected to a third-party auth flow; call see to continue, or use act to drive the SSO page", ssoDomain)
		s.recordHistoryLocked(fmt.Sprintf("login %q", a.Username), res.Verdict, final.URL)
		return res, nil
	}
	switch {
	case final.Captcha:
		res.Verdict = "CHALLENGE: " + captchaAfterLoginMsg
	case final.TwoFA:
		res.Verdict = "2FA/mfa needed: a verification code field is present - call see to read it, then act to enter the code"
	case final.Error != "":
		res.Verdict = "error: " + final.Error
	case final.LoginGone && final.Account:
		// Server-side verified: account indicators (logout/profile/dashboard
		// links) are present. The login form is gone AND we can see
		// logged-in UI elements - high confidence.
		res.Verdict = "logged in"
	case final.LoginGone && loginURLChanged(startURL, final.URL):
		// URL changed + form gone, but NO account indicators found.
		// Some sites redirect to a session/error page even with bad
		// credentials. Don't claim success without server-side proof.
		res.Verdict = "likely logged in (URL changed, form gone; call see to confirm - no account indicators detected)"
	case final.LoginGone:
		res.Verdict = "login form gone but no account indicators found; call see to confirm"
	default:
		res.Verdict = "still on login page: the form is still present - call see to check for an error or a missed step"
	}
	s.recordHistoryLocked(fmt.Sprintf("login %q", a.Username), res.Verdict, final.URL)
	return res, nil
}

const captchaAfterLoginMsg = "captcha appeared after submit (needs a solver or a human)"

// loginURLChanged reports whether the URL moved away from a login-ish path,
// a cheap logged-in signal (Google/Microsoft land on /accounts → /home, banks
// land on /dashboard, etc.). Treats fragment/query-only changes as stable.
func loginURLChanged(before, after string) bool {
	if before == "" || after == "" || before == after {
		return false
	}
	strip := func(u string) string {
		if i := strings.Index(u, "?"); i >= 0 {
			u = u[:i]
		}
		if i := strings.Index(u, "#"); i >= 0 {
			u = u[:i]
		}
		return strings.TrimRight(u, "/")
	}
	return strip(before) != strip(after)
}

// domainChanged reports whether the URL moved to a different DOMAIN (not just
// a path change). Returns the new domain if so, "" otherwise. Used to detect
// SSO redirects (the login redirects to accounts.google.com, login.microsoftonline.com, etc.).
func domainChanged(before, after string) string {
	if before == "" || after == "" || before == after {
		return ""
	}
	extractDomain := func(u string) string {
		if i := strings.Index(u, "://"); i >= 0 {
			u = u[i+3:]
		}
		if i := strings.IndexByte(u, '/'); i >= 0 {
			u = u[:i]
		}
		if i := strings.IndexByte(u, ':'); i >= 0 {
			u = u[:i]
		}
		return strings.ToLower(u)
	}
	db, da := extractDomain(before), extractDomain(after)
	if db != "" && da != "" && db != da {
		return da
	}
	return ""
}

// waitForLoginSettleLocked polls loginStateJS until the login resolves (form
// gone / account present / captcha / 2FA / error / URL changed) or the deadline.
// Returns the last-read state. Caller holds s.mu.
func (s *Session) waitForLoginSettleLocked(t *tab, max time.Duration) loginState {
	deadline := time.Now().Add(max)
	var last loginState
	for time.Now().Before(deadline) {
		time.Sleep(400 * time.Millisecond)
		st, err := s.readLoginStateLocked(t)
		if err == nil {
			last = st
		}
		if err != nil {
			continue
		}
		if st.Captcha || st.TwoFA || st.Error != "" || st.LoginGone || st.Account {
			return st
		}
		if loginURLChanged(t.tree.URL, st.URL) {
			return st
		}
	}
	return last
}
