# Security Policy

## Supported versions

Only the latest release line gets security fixes. The project is pre-1.0 in
semver terms (v3.x is a major-version module path, not a maturity claim), so
fixes land in the next patch release on the current major.

| Version | Supported |
|---------|-----------|
| 3.2.x   | Yes       |
| < 3.2   | No        |

## Reporting a vulnerability

Email the maintainer at **dondai1234@users.noreply.github.com**, or use GitHub's
private vulnerability reporting (the "Report a vulnerability" button on the
Security tab). Do **not** open a public issue for a security problem.

Include:

- A description of the issue and its impact.
- The version you tested (`agent-browser --version`).
- A minimal reproduction (config + steps).

You'll get an acknowledgement within a few days. Please give reasonable notice
before any public disclosure so a fix can ship first.

## Scope

agent-browser drives a local Chrome/Chromium instance over the Chrome DevTools
Protocol. It runs arbitrary page JavaScript via the `js` tool (disable with
`--no-eval`) and auto-accepts JS dialogs by design. Treat the MCP server as a
trusted local process: any MCP client that can call it can execute page JS in
the context of the logged-in browser profile. That is the intended capability,
not a vulnerability.

Out of scope:

- Bypassing the anti-bot/stealth fingerprinting on a target site. That is a
  feature, not a security boundary.
- Content a target page serves (XSS in a third-party site). Report that to the
  site, not here.
