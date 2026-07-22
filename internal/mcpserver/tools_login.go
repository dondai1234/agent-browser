package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/dondai1234/goshawk/v3/internal/browser"
)

func registerLogin(srv *mcp.Server, sess *browser.Session) {
	type args struct {
		Username string `json:"username" jsonschema:"the username/email to type into the username field"`
		Password string `json:"password" jsonschema:"the password to type into the password field"`
		URL      string `json:"url,omitempty" jsonschema:"optional: navigate to this login page first (http/https); if omitted, log in on the current page"`
	}
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "login",
		Description: "Log in to the current page (or url= first) in ONE call. Detects the username + password + submit fields, fills them, submits, and reports a state-verified verdict: 'logged in' | '2FA/mfa needed: ...' | 'CHALLENGE: ...' | 'error: <message>' | 'still on login page: ...' | 'no login form found: ...' | 'SSO redirect to <domain>: ...'. Handles single-step (username+password on one page) AND multi-step logins (Google/Microsoft/banks: username -> Next -> password appears -> submit) under one call. Detects OAuth/SSO buttons ('Sign in with Google/Apple/...') and REPORTS them in the result instead of auto-clicking. Also detects and reports: a 'remember me' / 'keep me signed in' checkbox (use act to toggle it), a 'forgot password' link, and SSO redirects (when the URL moves to a different domain after submit). Verifies the RESULTING STATE, not the return status, so a silent failure is reported, not hidden. Safe default for any standard login form; for captcha/2FA it stops and tells you what to do next.",
		Annotations: openWorld(),
	}, func(ctx context.Context, req *mcp.CallToolRequest, a args) (*mcp.CallToolResult, any, error) {
		if strings.TrimSpace(a.Username) == "" || strings.TrimSpace(a.Password) == "" {
			return errResult(fmt.Errorf("login needs username and password")), nil, nil
		}
		res, err := sess.Login(browser.LoginArgs{Username: a.Username, Password: a.Password, URL: a.URL})
		if err != nil {
			return errResult(err), nil, nil
		}
		out := res.Verdict
		if res.URL != "" {
			out += "\nurl: " + res.URL
		}
		if len(res.OAuth) > 0 {
			out += "\nSSO buttons present (use act to click one): " + strings.Join(res.OAuth, " | ")
		}
		if res.RememberMe {
			out += "\nremember me: a \"remember me\" / \"keep me signed in\" checkbox was detected (use act to check/uncheck it)"
		}
		if res.ForgotPassword != "" {
			out += "\nforgot password link: " + res.ForgotPassword
		}
		if res.SSORedirect != "" {
			out += "\nsso redirect to: " + res.SSORedirect
		}
		return textResult(out), nil, nil
	})
}
