<div align="center">

# agent-browser

**A token-efficient browser-automation MCP server for AI agents.**

One Go binary &nbsp;·&nbsp; Cross-platform &nbsp;·&nbsp; Purpose-built engine over chromedp — no Playwright, no Puppeteer, no Node.

[![MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev)
[![build](https://github.com/dondai1234/agent-browser/actions/workflows/test.yml/badge.svg)](https://github.com/dondai1234/agent-browser/actions/workflows/test.yml)
[![go report](https://goreportcard.com/badge/github.com/dondai1234/agent-browser)](https://goreportcard.com/report/github.com/dondai1234/agent-browser)
[![MCP](https://img.shields.io/badge/MCP-server-6E56CF.svg)](https://modelcontextprotocol.io)

</div>

---

> Built for the **agent that uses it**, not a human. Snapshots are dense ref-lines, not aria dumps. `find` filters the cached tree for free. Every action returns a **delta** (what changed), so you don't burn a turn re-seeing after each click.

## Why

The big browser MCP servers tax the agent every step. Measured head-to-head against the two largest browser-automation MCP servers (Playwright MCP and Chrome DevTools MCP), agent-browser costs an order of magnitude fewer tokens:

|  | **agent-browser** | Playwright MCP | Chrome DevTools MCP |
|---|:--:|:--:|:--:|
| Snapshot of Hacker News | **~1,200 tok** | ~14,700 tok | ~9,800 tok |
| Snapshot of a GitHub repo | **~1,250 tok** | ~21,600 tok | ~20,800 tok |
| Tool defs (cost just to be connected) | **~2,440 tok** | ~3,650 tok | ~5,120 tok |
| Saucedemo login (real task, all succeed) | **~397 tok / 0.9 s** | ~1,714 tok / 1.3 s | ~1,483 tok / 0.9 s |

The gap **widens with page complexity** — ~4× on a simple flow, ~12–17× on HN/GitHub — because the competitors re-dump the full tree every step while agent-browser is tiered, capped, and delta-based. All three complete the same real tasks; agent-browser just costs less, ships as one binary, and gets the browser closer to a real human. **Cost to exist: ~2,363 tokens/turn** (15 tool defs + server instructions).

<sub>Measured 2026-06-19, Windows, headless, over the public internet. Each server driven through its own MCP client; tokens ≈ chars÷4 on the tool-list and snapshot responses. All three servers completed the login.</sub>

## Install

Requires [Go](https://go.dev) 1.26+ and Chrome/Chromium (auto-discovered).

```sh
go install github.com/dondai1234/agent-browser@latest
```

Verify the install, and update later with the same command:

```sh
agent-browser --version                                  # check the installed version
go install github.com/dondai1234/agent-browser@latest    # re-run to update
```

### Or: tell your agent to install it

Copy this prompt into Claude Code, Cursor, Copilot Chat, Windsurf, or any agent with shell + MCP-config access, and it does the rest:

```
Install the agent-browser MCP server and connect it to this client:
1. Run:  go install github.com/dondai1234/agent-browser@latest
2. Verify:  agent-browser --version   (expect "agent-browser v1.0.0" or newer)
3. Find out which agent harnesa re you running on currently (Opencode, openclaw, hermes agent etc), Then find the MCP config for that harness.
4. Add a stdio MCP server named "agent-browser": command "agent-browser", args ["mcp"].
5. Confirm it connects, then tell me it's ready.
```

## Connect to your agent

Add it to any MCP client:

```json
{
  "mcpServers": {
    "agent-browser": { "command": "agent-browser", "args": ["mcp"] }
  }
}
```

Cursor, Claude Code, Claude Desktop, Windsurf, VS Code Copilot, opencode, Hermes Agent, and OpenClaw all work with this shape (VS Code uses `"servers"` instead of `"mcpServers"`). Ready-to-paste configs and per-client file paths are in [`examples/`](examples/README.md). Claude Code one-liner: `claude mcp add agent-browser -- agent-browser mcp`.

> If your client reports `spawn agent-browser ENOENT`, it can't find the binary on its PATH — use the absolute path in `command`: `$(go env GOPATH)/bin/agent-browser` (append `.exe` on Windows).

## The agent workflow (fewest round-trips)

```
navigate https://example.com level=summary   →  refs in the same call
find role=button text="Sign in"              →  [r12] button "Sign in"
click r12                                    →  delta (what changed)
fill r3 "user@x.com"                         →  delta
...
```

Each action returns what changed. You rarely call `see` after an action — the delta hands you fresh refs.

## Tools (15)

`navigate` · `see` (minimal/summary/full) · `find` · `read` · `click` · `fill` · `select` · `scroll` · `wait` · `screenshot` · `eval` · `tabs` · `upload` · `press_key` · `hover`

Every tool's description is hand-crafted to tell the agent exactly what to pass, what it returns, and the gotcha. `navigate` takes a `level` so you can get refs in the same call; `press_key` fires native key events (Enter submits a form); `hover` triggers CSS `:hover`; `select` matches an option's value *or* visible text; `eval` covers anything the snapshot can't expose (canvas, computed state, history, cookies, console errors).

## Anti-bot / stealth — on by default (`--no-stealth` to disable)

- **Static tells patched**: `navigator.webdriver` false (via `--disable-blink-features=AutomationControlled` and dropping `--enable-automation`); `userAgentData`/`plugins`/`languages`/`window.chrome`/WebGL/hardware spoofed via a pre-page init script. Verified: `webdriver=false`, `plugins=5`, `languages=en-US,en`.
- **Real fingerprint**: `--headless=new` (near-real) by default; `--headless=false` for the real GPU/canvas/timing fingerprint on hard targets.
- **Behavioral realism**: a jittered smoothstep mouse path before each click; `press_key` for typed input.
- **Proxies + challenge detection**: `--proxy-server` for residential proxies (the biggest IP-reputation lever); `navigate`/`see` surface `CHALLENGE:` on Cloudflare/DataDome/reCAPTCHA/hCaptcha/Turnstile and auto-wait for managed challenges to clear.

<details>
<summary><b>Honest limits</b> — no chromedp tool beats these</summary>

- The **CDP runtime signal** (a debugger-attached timing delta) is fundamental to CDP; only a custom Chromium build (e.g. Camoufox) hides it.
- **Image-CAPTCHA solving** (reCAPTCHA grids, hCaptcha) needs a paid solver — solver integration (user-provided API key) is planned for v1.1.
- For hard targets, stack: `--headless=false` + `--proxy-server <residential>` + a solver.
- Cross-origin iframes are opaque (as for any tool); same-origin iframes work fully.

</details>

## Flags

`--headless` · `--user-data-dir` (override the profile location) · `--no-persist` (throwaway profile; by default logins persist at `<os config dir>/agent-browser`) · `--proxy-server` · `--user-agent` · `--viewport W,H` · `--no-stealth` · `--no-eval` (eval is on by default) · `--allow-insecure-schemes` · `--version`

## Status

**v1.0.0** — first public release. 18 live tests (real sites: a saucedemo full purchase + React login, iframes, upload, JS alerts, GitHub/Wikipedia extraction, a CSS `:hover` menu, Enter-to-submit) plus unit tests. `govulncheck` clean. CI: ubuntu/windows/macos.

[CHANGELOG](CHANGELOG.md) &nbsp;·&nbsp; [Example MCP configs](examples/README.md) &nbsp;·&nbsp; License: MIT
