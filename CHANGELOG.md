# Changelog

## Unreleased (v2.1, planned)

- **intercept** (block/mock/redirect network rules): live use hits a chromedp Fetch-event concurrency deadlock when an action triggers a matching paused request; the fix path is a dedicated second target context for fetch responses.
- **Credential vault**: encrypted per-site credential storage (OS keychain) so the agent can log in to a fresh site without hand-fed passwords. (v1.0's persistent profile already covers the common "stay logged in" case.)
- **Per-ref region tags**: tag each ref with its landmark region (nav/main/sidebar/footer) via an AX-tree parent walk, so the agent can reason about layout ("the buy button is in main"). Deferred from v2.0 - needs a two-pass BuildTree; the brief's `regions:` line covers the layout shape for now.
- **Stale-ref auto-recovery**: when a by-ref action targets a ref that's gone after a re-render, re-target to the closest live match by name/role and surface it. Deferred from v2.0 - needs a prevRefs map; the existing error names the next move ("refs may be stale - call see again").

## v2.0.0 - 2026-06-21 (cognition layer)

v1 made snapshots cheap (delta act-and-see). v2 makes the agent **think in goals, not refs**: the tool understands the page, acts on intent, and reports a verdict. The agent stops interpreting raw DOM and stops re-seeing to confirm. Additive - all v1 by-ref tools still work.

### New tools (15 -> 18)

`act` (intent-first), `extract` (structured data), `history` (session memory).

### Headline: `act` + verdicts

- **`act <intent>`**: pass a control's name (`act "Sign in"`, `act "Username" value=x`); local heuristics resolve it on the cached snapshot (no LLM, no per-call cost), perform the default action for its role (click buttons/links, fill textbox/searchbox, select combobox), return a verdict + delta. Collapses find + click/fill + see into one call. Ambiguous matches return ranked candidates (never guesses) - disambiguate with `nth`/`role` or fall back to `click`/`fill` by ref.
- **Verdicts on every action** (click/fill/select/scroll/press_key/hover/act): a one-line semantic outcome - `navigated to ...`, `dialog opened: ...`, `status: ...`, `changed: +N -M ~K`, `no visible effect`, `CHALLENGE: ...`. For non-navigation actions it also folds in the XHR/Fetch responses that fired (`net: /api/cart 200`) via a read-only per-tab network listener (no Fetch-domain pausing, so no deadlock risk).

### Cognition layer

- **`see level=brief`**: a ~50-token page brief - page type (login form / list / article / dialog), auth state (logged in / anonymous / blocked), top primary actions with refs, regions, counts. Pure heuristics over the a11y tree.
- **`extract`**: `table` (rows, JSON; objects if the first row is headers), `links` (`[{text,href}]`), `list`, `form` (`[{ref,role,name,value}]` from the cached tree), `article` (main content text). One targeted DOM eval for the DOM kinds; `form` is free.
- **`history`**: a rolling action log (step / action / verdict / URL), capped at 200, queryable (`last=N`, `errors=true`), offloaded from the agent's context so long tasks don't bloat it. Step numbers stay monotonic across trims.

### Extensions + polish

- **`wait` gains semantic conditions**: `url=` (URL contains), `text=` (body contains), `gone=` (body no longer contains) - returns what satisfied it, or an error on timeout.
- **`fill` gains a `{ref: value}` map**: fill a whole form in one call (one round-trip + one delta).
- **Scroll awareness**: `scroll` reports the position (`more below` / `at bottom` / `fits viewport`) so the agent knows whether to keep scrolling.
- **Challenge detection on every snapshot** (not just navigate): a click that lands on a Cloudflare wall reports `verdict: CHALLENGE: ...`.

### Honest tradeoffs

- **Cost to connect rose** ~1,150 tok (v1 ~2,363 -> v2 ~3,500) to fund 3 new tools + verdicts + richer descriptions. Roughly tied with Playwright MCP (~3,442 tok) and under Chrome DevTools MCP (~5,000 tok), but with 3 capabilities neither has. Per-task cost dropped ~2.6x vs v1 (~397 -> ~154 tok on a saucedemo login) and ~10x vs the competitors.
- The intent resolver / verdict / extract heuristics are best-effort; `act` never guesses (returns candidates), `extract` says "no X found" when absent, `see level=summary` is always available for raw refs.
- Speed is unchanged (same chromedp mechanics); the win is tokens + round-trips + capability, not latency.

### Tests

- Unit tests for the intent resolver (matching/scoring/exact-wins/ambiguity/nth/role/value-prefers-fillable), verdict engine (priority order, signal diff, Backend=0 edge), brief heuristics (page-type/auth/primary-actions), history (cap/monotonic/last/errors filters), network (summarize/filter/shortURL).
- Live tests against real sites: intent-first saucedemo login, ambiguous + nth + no-match `act`, extract table/links/list/form/article, history recall, wait url/text/gone/timeout, fill-map whole-form, scroll-awareness, network-aware verdict (Wikipedia autocomplete), verdict on a real navigation + add-to-cart.
- `govulncheck` clean. CI: ubuntu/windows/macos.

## v1.0.0 - 2026-06-19 (first release)

A token-efficient, anti-bot-aware browser-automation MCP server on a purpose-built Go + chromedp engine (no Playwright, no Puppeteer, no Node). Single static binary, cross-platform.

### Tools (15)

`navigate`, `see` (minimal/summary/full), `find`, `read`, `click`, `fill`, `select`, `scroll`, `wait`, `screenshot`, `eval`, `tabs`, `upload`, `press_key`, `hover`.

### Highlights

- **Token-efficient by design**: dense ref-line snapshots, tiered observation (`see minimal` ~27 tok, `find` ~4 tok), and **delta act-and-see** - actions return only what changed, so the agent rarely re-snapshots. ~12-17x smaller snapshots than the major browser MCP servers on real pages; ~2,363 tokens/turn to exist.
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
