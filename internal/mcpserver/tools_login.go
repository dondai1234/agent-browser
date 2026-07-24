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
		Description: "Log in in ONE call. Detects username+password+submit fields, fills, submits, verifies the resulting state. Verdict: logged in / 2FA needed / CHALLENGE / error / still on login page / SSO redirect / no login form. Handles single-step and multi-step (username -> Next -> password). Detects OAuth buttons (reports, doesn't auto-click), remember-me checkbox, forgot-password link, SSO redirects. Verifies state, not return status - silent failures are reported.",
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
