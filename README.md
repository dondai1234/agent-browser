<div align="center">

# 🪿 goshawk

**A browser in the agent's hand. 9 tools. One verdict per action. Zero guessing.**

Navigate, read, click, fill, log in, scrape. Pure Go, single binary, no Node, no Python, no Docker.
Built on Chrome DevTools Protocol + MCP. Works with Claude Code, Cursor, OpenCode, Pi, anything that speaks MCP.

[![Go](https://img.shields.io/badge/Go-1.24+-00ADD8?logo=go)](https://go.dev)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)
[![Version](https://img.shields.io/badge/version-4.0.0-blue.svg)](CHANGELOG.md)
[![GitHub stars](https://img.shields.io/github/stars/dondai1234/goshawk?style=social)](https://github.com/dondai1234/goshawk/stargazers)

```bash
go install github.com/dondai1234/goshawk/v4/cmd/goshawk@latest
```

[Install](#-install) · [The 9 tools](#-the-9-tools) · [Self-diagnosing verdicts](#-self-diagnosing-verdicts) · [Comparison](#-comparison) · [Token cost](#-token-cost) · [Honest limits](#-honest-limits)

</div>

<br>

## Why goshawk

Most browser MCP servers give the agent a raw page snapshot and let it figure out what happened. The agent burns 3 to 5 calls investigating: *did the click work? is the page blank? what went wrong?* goshawk closes that loop.

- 🔬 **Self-diagnosing verdicts**: every action returns a confidence-scored verdict. When something goes wrong, the verdict explains why and what to do next. "did you mean Sign in?" when you type "Sgn in". "errors visible: Email is required. Fix and retry" when a form fails. Zero extra tool calls, no LLM in the loop, pure heuristics.
- 🪶 **9 tools, ~3.4K tokens**: dense a11y-tree snapshots (ref-lines, not aria dumps), intent-first actions, one verdict per call. More capability than tools shipping 6K+ tokens.
- 🦅 **Intent-first**: `act intent="Sign in"` finds the button. `act fields={"Username":"john","Password":"hunter2"}` fills a whole form in one call. Name what you want, not how to find it.
- 🛡️ **Self-healing refs**: refs from `see`/`find` survive re-renders. When React re-creates a node with the same role+name, goshawk auto-heals the ref. No stale-selector failures.
- 🍪 **Cookie banners auto-dismissed**: on every navigate. Consent redirects auto-recovered. The a11y tree comes back clean, not cluttered with "Accept cookies" overlays.
- 📄 **Blank page detection**: when a page loads empty (slow SPA, rate-limit, JS not hydrated), the verdict says `BLANK PAGE` with what to try. One call, not five.
- 🔐 **One-call login**: `login username= password=` handles single-step and multi-step, verifies the resulting state, detects 2FA, SSO redirects, remember-me, forgot-password. Reports state, not HTTP status.
- 🎭 **Named profiles**: isolated cookies, auth, storage. Switch identities in one call. Logins survive restarts with persistent profiles.
- 🦿 **Single Go binary**: 14 MB. No Node runtime, no Python venv, no Docker. `go install` and done.

> goshawk is for the agent. You install it once; the agent drives the browser through 9 tools.

---

## 🧰 The 9 tools

| Tool | One-liner |
|------|-----------|
| `nav` | Navigate to a URL and get an orientation back: page type, auth, primary actions WITH refs, counts. Auto-dismisses cookie banners, detects CHALLENGE and BLANK PAGE. `waitSelector=` for slow SPAs. |
| `see` | Inspect the current page. Levels: brief, refs, text, outline (CSS selectors for js), full, shot. |
| `act` | Interact: click, fill, select, hover, key, upload, or batch form fill (`fields={}`). Returns a self-diagnosing verdict. Don't re-see after. |
| `js` | Run JavaScript, get clean JSON. Helpers: `$(sel)`, `text(x)`, `table(sel)`, `links(sel)`, `wait(fn,ms)`, more. |
| `find` | Locate elements: `role=`/`text=` filters the a11y tree, `selector="css"` queries the DOM directly. |
| `tabs` | List, switch, close, label tabs. |
| `history` | Session action log. `errors=true` for failures, `last=N` for recent steps. |
| `session` | `reset` (relaunch), `clear` (wipe cookies+storage), `profile` (named identities: create/switch/export/import). |
| `login` | One-call login. Detects fields, fills, submits, verifies state. Handles multi-step. Reports: logged in / 2FA / CHALLENGE / error / SSO. |

---

## 🔬 Self-diagnosing verdicts

The feature that sets goshawk apart. Three layers, zero extra tool calls:

**Layer 1: "Did you mean?"**

When `act intent="Sgn in"` finds no exact match, goshawk returns the 3 closest matches by Levenshtein distance:

```
no element named "Sgn in" found; did you mean: "Sign in" (button, r4), "Sign in to GitHub" (heading, r2)?
```

The agent retries with the right name. No `see` + `find` + `see` investigation loop.

**Layer 2: "What blocked it?"**

When an action fires but confidence is uncertain or likely, goshawk scans the page for visible error messages, HTML5 validation failures, and blocking modals:

```
verdict: [uncertain] no visible effect
errors visible: "Email is required". Fix those fields and retry
modal "Login Required" opened - call see to inspect
```

**Layer 3: "What now?"**

Each diagnostic is an actionable suggestion, not just a description. The agent knows exactly what to do next without another call.

Pure heuristics. No LLM in the loop. No extra API calls. Works across React, Vue, Angular, vanilla.

---

## 🎯 Confidence-scored verdicts

Every `act` returns a verdict with a confidence tag:

| Tag | Meaning | When |
|-----|---------|------|
| `[confirmed]` | DOM changed or XHR 2xx fired | The action definitely worked |
| `[likely]` | Content shifted or XHR fired | Something happened, verify if critical |
| `[uncertain]` | No visible effect | Check with `see` or the diagnostics |

Navigation is always confirmed. Bot challenges are always uncertain. No more guessing whether a click landed.

---

## 📝 Batch form filling

Fill an entire form in one call:

```
act fields={"Username":"john","Password":"hunter2","Remember me":"true","Country":"United States"}
```

Each label is resolved to a form field (a11y name + DOM fallback). The type is auto-detected: text, checkbox, radio, select, slider, file. The right action is performed for each. One re-snapshot, validation errors reported.

---

## 🔐 One-call login

```
login username="john" password="hunter2" url="https://example.com/login"
```

Detects the username + password + submit fields, fills them, submits, and verifies the resulting state. Handles:

- Single-step (username + password on one page)
- Multi-step (username first, then password appears)
- 2FA (stops and reports what's needed)
- SSO redirects (reports the domain)
- Remember-me checkbox (reports, doesn't auto-toggle)
- Forgot-password link (reports it)
- OAuth buttons (reports, doesn't auto-click)

Verifies state, not HTTP status. A silent failure is reported, not hidden.

---

## 🛡️ Self-healing refs

Refs from `see` and `find` are stable across re-renders. When a framework re-creates a DOM node with the same role + name (React key change, Vue v-if toggle), goshawk searches the current tree and auto-heals the ref. No stale-selector failures mid-flow.

Refs are per-tab and cleared on navigation. They survive re-renders within a page.

---

## 🍪 Cookie banners and consent redirects

Cookie/consent banners are auto-dismissed on every navigate. The a11y tree comes back clean, not cluttered with overlay elements.

If a site redirects to a full-page consent statement (common for EU users), goshawk detects the redirect, tries broader dismissal, and re-navigates to the original URL. If it can't recover, the verdict says `CONSENT REDIRECT: original -> current` with what to try.

---

## 📄 Blank page detection

When a page loads but renders empty (slow SPA hydration, rate-limiting, JS error), goshawk detects it and reports clearly:

```
BLANK PAGE: no interactive elements or headings detected.
The page may be a slow SPA still loading, may require JS execution,
or may have a rendering issue. Use js to check or see in a moment.
```

Before: the agent sees `interactive: 0`, calls `see`, then `find`, then `see full`, then `js`. 4 to 5 wasted calls. After: one call, the verdict explains what happened.

`waitSelector=` on `nav` gives a native waitForSelector for slow SPAs: wait for `#dashboard` to render before returning the orientation.

---

## 🎭 Named profiles

Isolated browser identities. Different logins, cookies, storage. Switch in one call.

```
session mode=profile action=create name="work"
session mode=profile action=switch name="work"
session mode=profile action=list
session mode=profile action=export        # dump cookies+storage as JSON
session mode=profile action=import data="..."  # restore from JSON
```

By default, goshawk persists to `<os config dir>/goshawk` so logins and cookies survive server restarts. The agent doesn't re-login each run. Use `--no-persist` for throwaway sessions.

---

## 🚀 Install

```bash
go install github.com/dondai1234/goshawk/v4/cmd/goshawk@latest
```

Then add to your MCP client config:

```json
{
  "mcpServers": {
    "goshawk": {
      "command": "goshawk",
      "args": ["mcp"]
    }
  }
}
```

Requires Chrome or Chromium installed. goshawk connects via CDP.

<details>
<summary><b>CLI flags</b></summary>

| Flag | Default | Purpose |
|------|---------|---------|
| `--headless` | `true` | `--headless=false` for real GPU fingerprint (hard anti-bot) |
| `--user-data-dir` | `<config>/goshawk` | Persistent profile dir. Logins survive restarts. |
| `--no-persist` | `false` | Throwaway temp profile |
| `--proxy-server` | none | Proxy URL. Residential proxy fixes most IP-reputation blocks. |
| `--user-agent` | Chrome default | Override User-Agent |
| `--viewport` | `1366,768` | Window size W,H |
| `--no-stealth` | `false` | Disable anti-detection patches |
| `--no-cookie-dismiss` | `false` | Disable cookie banner auto-dismiss |
| `--no-eval` | `false` | Disable the `js` tool |
| `--op-timeout` | `20s` | Per-operation timeout |
| `--idle-timeout` | `10m` | Auto-close Chrome after idle period |
| `--allow-insecure-schemes` | `false` | Allow `file://`, `data:`, etc. |

</details>

---

## 📊 Comparison

| | **goshawk** | Playwright MCP | Browser-use | Puppeteer MCP |
|---|---|---|---|---|
| **Language** | Go (single binary) | Node.js | Python | Node.js |
| **Tools** | 9 | 17+ | varies | 10+ |
| **Token cost** | ~3.4K | ~6K+ | varies | ~5K+ |
| **Self-diagnosing verdicts** | yes | no | no (uses LLM) | no |
| **Confidence scoring** | yes | no | no | no |
| **Batch form fill** | yes (`fields=`) | no | no | no |
| **Named profiles** | yes | no | no | no |
| **Self-healing refs** | yes | no | no | no |
| **Cookie banner auto-dismiss** | yes | no | no | no |
| **Blank page detection** | yes | no | no | no |
| **One-call login** | yes | no | no | no |
| **Intent-first actions** | yes | no (selectors) | yes (LLM) | no (selectors) |
| **Per-call LLM** | no | no | yes | no |
| **Runtime deps** | Chrome only | Node + Playwright | Python + Playwright | Node + Puppeteer |

---

## 🪙 Token cost

Most browser MCP servers cost 5 to 8K tokens just to exist. goshawk's 9 tools cost **~3.4K tokens** at `tools/list` (measured from the live MCP JSON response, 13623 chars). The connect-time `instructions` (~66 tokens) are injected once at handshake, not repeated every turn.

A `nav` orientation on a typical page returns ~60 to 80 tokens. A `see refs` on a page with 10 interactive elements returns ~40 tokens. Dense by design: ref-lines, not aria dumps.

---

## ⚠️ Known gotchas

| Gotcha | What to know |
|---|---|
| **Chrome must be installed** | goshawk connects via CDP to Chrome/Chromium. No Chrome, no browser. |
| **One profile per process** | Chrome locks the profile dir. Run concurrent clients with separate `--user-data-dir` paths. |
| **20s op timeout** | If a page hasn't loaded in 20s, it's dead. Raise `--op-timeout` for known slow targets. |
| **Headless may trigger bot detection** | Use `--headless=false` for sites with aggressive anti-bot. A residential proxy (`--proxy-server`) is the #1 fix for IP blocks. |
| **`js` tool can be disabled** | Start with `--no-eval` to block arbitrary page JS. The other 8 tools still work. |

---

## ⚠️ Honest limits

| Limit | What happens instead |
|-------|----------------------|
| **Hard CAPTCHAs** (Turnstile, hCaptcha) | Reported as `CHALLENGE` in the verdict. The agent knows to stop, not retry blindly. |
| **Shadow DOM piercing** | `js` with `$(sel)` reaches open shadow roots. Closed shadow roots need `selector=` on `act` or `find`. |
| **Iframe access** | Same-origin iframes are reachable. Cross-origin iframes are blocked by browser security. |
| **No screenshots in headless** | `see shot` captures the page, but some sites render differently headless. Use `--headless=false` for visual fidelity. |
| **Rate-limited sites** | HN, Reddit, and others may serve "Sorry" pages under heavy headless use. The BLANK PAGE verdict catches this. |

---

<div align="center">

### If goshawk saves you tool calls, star the repo: it helps others find it.

[![GitHub stars](https://img.shields.io/github/stars/dondai1234/goshawk?style=social)](https://github.com/dondai1234/goshawk/stargazers)

**MIT** · [Changelog](CHANGELOG.md) · [Issues](https://github.com/dondai1234/goshawk/issues)

</div>
