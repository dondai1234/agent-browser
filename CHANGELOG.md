# Changelog

## v2.0.3 - 2026-06-21 (reliability: the NewTab fix)

v2.0.2 fixed the locked-profile crash, but a live opencode test then surfaced that **`tabs new` crashed the session** ("chrome failed to start"). Root cause: the v2.0.1 first-tab change made the first tab `NewContext(browserCtx)`, so chromedp set the `Browser` on the *first tab's* context, not on `browserCtx`. `NewTab` derived new tabs from `s.browserCtx` (whose `Browser` was nil), so chromedp thought each new tab was the "first" context and **launched a second Chrome** - which failed on the locked persistent profile.

### The fix

- **`NewTab` derives new tabs from an existing tab's context** (which carries the allocated `Browser`), not from `s.browserCtx`. So `NewContext` creates a new target on the existing browser instead of launching a second Chrome. One-line root-cause fix for the opencode "tabs new crashed" report.

### Tests

- New live test `TestReliabilityNewTabSameBrowser`: use a dedicated `--user-data-dir` (so the first Chrome locks it), then `tabs new` - a buggy NewTab launching a second Chrome on the locked dir fails with "chrome failed to start"; the fix opens a tab on the same browser. Negative-verified: reverting the fix makes the test fail with the exact opencode symptom.
- Full live suite re-verified green (reliability + act/verdict/brief/history/qol). `govulncheck`: 0 reachable.

## v2.0.2 - 2026-06-21 (reliability: the server-crash fix)

v2.0.1 bounded operations + added reset, but a live test on opencode surfaced a deeper bug: when the **persistent profile** (`<os config dir>/agent-browser`) is locked by an orphaned Chrome from a prior run, Chrome fails to start, and chromedp then **panics** (`close of closed channel` in `ExecAllocator.Allocate`) on the next op's retry - crashing the whole MCP server. Every tool then times out (the server is dead). This release makes launch + recovery bulletproof.

### The crash + the fix

- **Persistent -> temp profile fallback**: if Chrome can't start with the requested persistent profile (locked by an orphan, corrupted, or any launch error - "chrome failed to start", "websocket url timeout reached", ...), `New` tears that attempt down + relaunches with a throwaway temp profile so the server still works (no persistence, but alive). A stderr log line tells the operator persistence is off + how to restore it (kill leftover agent-browser Chrome processes). Without this, a locked profile made every tool fail.
- **Dead-session guard (the chromedp panic fix)**: a fatal browser error (chrome failed to start, the process crashed, the websocket dropped) now marks the session `dead`. `run`/`runTimeout` short-circuit on a dead session - they return the error WITHOUT calling `chromedp.Run`, so a dead browser is never retried. The panic was chromedp double-closing `c.allocated` when a second `Run` retried `Allocate` after the first failed; never retrying = no double-close = no crash.
- **`reset` is now a full browser relaunch** (not just a new tab): it tears down the whole browser + relaunches Chrome, so it recovers from a wedged TAB and from a crashed BROWSER (a new-tab reset can't - the dead session won't accept a new target). Other tabs are lost (acceptable for recovery). Bounded by the op timeout.
- **Launch timeout is separate from the op timeout** (60s): the Chrome cold-start (the first CDP op) gets its own generous budget so a slow launch (antivirus, first-run profile setup, a heavy persistent profile) doesn't fail `New` under a tight `--op-timeout`.
- **First-tab cancel no longer kills the browser** (from v2.0.1, retained): the first tab gets its own chromedp target, so `reset`/`close` on t1 closes only that tab.

### Also in this release

- The dialog auto-accept handler no longer goes through `run` (it ran in a listener goroutine without the session lock, which would have raced on the dead flag + could wrongly mark the session dead on a closing tab).

### Tests

- New live test `TestReliabilityProfileFallback`: launch the server with an invalid `--user-data-dir` (a file, not a dir) so Chrome can't start, assert the temp-profile fallback makes navigate succeed (pre-fix this crashed the server).
- All v2.0.1 reliability tests re-verified green (op-timeout, reset, combobox, press_key, tab-switch) + the existing live suite (act login, verdict, qol back/forward). `govulncheck`: 0 reachable.

## v2.0.1 - 2026-06-21 (reliability)

v2.0.0 could wedge: a single hung CDP call (a page that never finishes loading, a mid-navigation execution-context teardown, a challenge that stalls) held the session mutex forever, and EVERY tool then blocked on the lock until the MCP client timed out - the "session hung, all tools timed out" failure. This release bounds every operation, fixes the correctness gaps a live agent test surfaced, and adds an explicit recovery path.

### Reliability (the wedge fix)

- **Per-operation timeout** (`--op-timeout`, default 30s): every CDP call is bounded. A hung page returns a timeout error + releases the session lock instead of wedging every tool. Implemented with a goroutine + select (not `context.WithTimeout`, which breaks `chromedp.Navigate`'s navigation listener with a spurious "context canceled"). A genuinely wedged op leaks a goroutine until the tab is reset/closed.
- **`reset` tool**: the explicit recovery path the agent asked for ("no session management - no close/reset when it hangs"). Drops the current tab (cancelling any hung op on it) + opens a fresh one at an optional URL; other tabs are kept. Pairs with the op timeout - the timeout guarantees the lock is released, reset cleans up.
- **First-tab cancel no longer kills the browser**: the first tab now gets its own chromedp target (like `new tab`), so `reset`/`close` on t1 closes only that tab. Previously t1's cancel was the browser cancel, so resetting or closing the first tab tore down the whole browser (then every later op errored "context canceled"). This also fixes a latent `close t1` bug.

### Correctness fixes (from a live OpenCode agent test)

- **`act` on an ARIA combobox** (Google search, autocomplete widgets): a `combobox` role over a `<textarea>` has no `<option>`s, so the old `selectJS` no-op'd on it and the agent had to fall back to `eval`. `act` now probes the tag - native `<select>` -> select the option; ARIA combobox (textarea/input) -> fill (the native value setter + input/change that React/Vue autocompletes actually listen for).
- **`press_key` multi-char silent no-op**: `press_key key="weather in tokyo"` dispatched a useless keyDown with a multi-char key string (no native default, inserts nothing) and the agent couldn't tell. `press_key` now rejects anything that isn't a named key or a single character, with an error that redirects to `fill`/`act` for typing text.
- **`tabs switch`/`close` by label**: the handler passed only `id` to `SwitchTab`/`CloseTab`, so `switch label=<name>` silently failed. Both now accept the `label` field as a fallback when `id` is empty.

### Tests

- 5 new live reliability tests (gated by `AGENT_BROWSER_INTEGRATION=1`): op-timeout bound fires + session not wedged after; `reset` drops + reopens a working tab; ARIA combobox is filled not selected; `press_key` rejects a multi-char key; `tabs switch`/`close` by label works.
- New unit test `TestValidateKeyPress` (runs in CI): the press_key input rule without a browser.
- Existing live suite re-verified green (act/verdict/brief/history/qol/scroll/extract/net) - no regression from the `run` wrapper or the first-tab change.
- `govulncheck`: 0 reachable vulnerabilities.

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
