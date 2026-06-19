# Changelog

## Unreleased (v1.1, planned)

- **intercept** (block/mock/redirect network rules): live use hits a chromedp Fetch-event concurrency deadlock when an action triggers a matching paused request; the fix path is a dedicated second target context for fetch responses.
- **Credential vault**: encrypted per-site credential storage (OS keychain) so the agent can log in to a fresh site without hand-fed passwords. (v1.0's persistent profile already covers the common "stay logged in" case.)

## v1.0.0 - 2026-06-19 (first release)

A token-efficient, anti-bot-aware browser-automation MCP server on a purpose-built Go + chromedp engine — no Playwright, no Puppeteer, no Node. Single static binary, cross-platform.

### Tools (15)

`navigate`, `see` (minimal/summary/full), `find`, `read`, `click`, `fill`, `select`, `scroll`, `wait`, `screenshot`, `eval`, `tabs`, `upload`, `press_key`, `hover`.

### Highlights

- **Token-efficient by design**: dense ref-line snapshots, tiered observation (`see minimal` ~27 tok, `find` ~4 tok), and **delta act-and-see** — actions return only what changed, so the agent rarely re-snapshots. ~12–17× smaller snapshots than the major browser MCP servers on real pages; ~2,363 tokens/turn to exist.
- **Agent-POV**: free `find` on the cached tree, errors that name the next move, JS dialogs auto-accepted, same-origin iframes surfaced with refs and an `in "..."` annotation, every action returns fresh refs via the delta.
- **Anti-bot / stealth on by default** (`--no-stealth`): `navigator.webdriver` patched, fingerprint spoofed (userAgentData/plugins/languages/window.chrome/WebGL/hardware), `--headless=new`, jittered real-mouse movement before clicks, `--proxy-server`, Cloudflare/captcha challenge detection and auto-wait. Honest limits documented (CDP runtime signal; image-captcha solving needs a paid solver).
- **Real input where eval is unreliable**: `press_key` fires native key events (Enter submits a form, Escape closes, chars insert); `hover` triggers CSS `:hover` and JS mouseover.
- **Persistence on by default** (`--no-persist`): a Chrome profile at `<os config dir>/agent-browser` keeps logins/cookies/localStorage across server restarts, so the agent doesn't re-login every run. One profile per process (Chrome locks it); concurrent clients use separate `--user-data-dir` paths.
- **One binary, 2-command install**: `go install github.com/dondai1234/agent-browser@latest`; `--version` reports the build (injected at release-build time).

### Security and robustness

- URL scheme allowlist (`file://`/`javascript:`/`data:`/`about:`/`blob:` blocked by default; `--allow-insecure-schemes` to opt in); relative URLs rejected.
- `eval` on by default with `--no-eval` opt-out.
- JS dialogs auto-accepted (so `alert()` doesn't hang the agent).
- Crash-aware AX rebuild: a content-signature stable-poll (FNV over node count + every node's role/name/value) returns as soon as the tree actually settles, with a retry on failure.
- `sync.Mutex` on session state for concurrent MCP calls; crash-aware error wrapping.
- `govulncheck`: 0 reachable vulnerabilities (Go 1.26.4).
- Cross-platform: windows/amd64, linux/amd64, linux/arm64, darwin/amd64, darwin/arm64. CI matrix: ubuntu/windows/macos.
