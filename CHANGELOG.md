# Changelog

## v2.4.0 - 2026-06-27 (agent-efficiency: targeted extract, collect, clean slate, select-by-selector, batched fill)

A pass on token + call efficiency for real agent workflows. Five changes:

- **New `collect` tool.** Pass `fields={label:selector}`, get `{label:value}` JSON in one call - the multi-value pull without writing JS. Get a repo's stars + language + issues + latest release, or an article's title + first paragraph + infobox, in one call. `attrs={label:attrName}` pulls an attribute instead of text (e.g. a link's href). A selector that doesn't match returns `null` for that label (a partial result you can branch on, not an error). One call replaces N `extract` calls or a custom `eval`. Tool count 20 -> 22.
- **`extract` now targets a region.** `selector=` scopes table/links/list/article to a CSS-matched element; a new `text` kind returns each matched element's text as a JSON array (the targeted value pull, no JS); `maxChars=` caps the response. `extract article selector="#firstHeading"` returns just the heading instead of the whole `<main>`.
- **New `clear` tool - one-call clean slate.** Wipes cookies + the current origin's localStorage/sessionStorage and reloads (or opens a url), instead of removing leftover state item by item.
- **`select` now takes a `selector`.** Brings select to parity with click/fill/act (all already had a selector escape hatch) - the path for unlabeled dropdowns the a11y tree has no name for (a sort `<select>` with only a class).
- **`fill` description flipped** to lead with the batched form path (`fields={ref:value}` in one call) - the efficient default for a form. **`eval` description reframed** as the go-to for scattered scalars (return several fields as one object).

### Verification

- `TestRealWorldCollect` / `CollectMiss` / `CollectRequiresFields` / `CollectFormFlow` (live): one-call multi-value pull returns `{label:value}` JSON; `attrs` returns an attribute (a link's absolute href); a missing selector yields `null` (not an error); empty `fields` is an isError; a batched `fill fields={}` + `act "Login"` reaches the inventory.
- `TestRealWorldExtractSelectorScope` / `ExtractText` / `ExtractTextRequiresSelector` / `ExtractMaxChars` / `ExtractSelectorMiss` (live): selector scoping shrinks output, `text` returns a JSON array of matches, `text` without a selector is an isError, `maxChars` caps + marks truncated, a non-matching selector is an isError with a pointer.
- `TestRealWorldSelectSelector` (live): `select selector=".product_sort_container" value="Price (high to low)"` applies the sort - the priciest item lands before the cheapest in the inventory.
- `TestRealWorldClear` / `ClearNavigate` (live): after login, `clear` wipes the session - saucedemo redirects to the login page; `clear url=` lands on the new page.
- Full live suite green (1063s, 0 failures). `go build`/`go vet`/`gofmt` clean; `govulncheck` 0 reachable.

## v2.2.2 - 2026-06-25 (idle auto-close - Chrome tears down when not in use)

v2.2.1 stopped Chrome spawning on startup, but once you navigated once, Chrome stayed alive for the whole MCP session - so a one-shot browser use left Chrome running for the rest of the chat. v2.2.2 adds an **idle auto-close**: a background reaper tears Chrome down after `--idle-timeout` (default **10m**) of no browser activity. The next navigate re-launches it seamlessly via the lazy-launch path (page state is lost - a fresh tab - so the agent re-navigates; read-only ops before that report "no page snapshot yet; call navigate first"). `--idle-timeout 0` disables it. The reaper polls at idleTimeout/4 (clamped 5-60s), only acts when the browser is actually up + genuinely idle, and never races an in-flight op (it takes the session lock, so it can only tear down between ops). Browser-touching ops reset the idle timer via `touchLocked` (in `curTabLocked` + `ensureBrowserLocked`); pure context ops (`history` with no browser use) don't keep Chrome alive.

### Verification

- `TestIdleAutoClose` (live, 6s test timeout): Chrome launches on navigate, **auto-closes after ~6s idle** (0 debug-chrome processes), `where` reports "no page snapshot yet", the next navigate **re-launches** Chrome + the page is reachable.
- Full live suite green (743s, 0 failures) - the `touchLocked` in the hot path + the background reaper regress nothing (the 10m default never fires during the suite's frequent ops). `govulncheck` 0 reachable.

## v2.2.1 - 2026-06-25 (lazy browser launch - no Chrome on startup)

The MCP server used to launch Chrome the moment it started (`browser.New` ran `launchBrowserLocked` eagerly), so a Chrome process spawned as soon as your agent client connected - even before any tool was called. If you connected the server but never drove it, Chrome sat idle eating resources. v2.2.1 makes the launch **lazy**: `New()` only constructs the session; Chrome spawns on the first **page-opening** op (navigate / new tab / back-forward-reload) via a new `ensureBrowserLocked`. Read-only ops called before the first navigate (`where`, `find`, `see`, `read`, ...) report "no page snapshot yet; call navigate first" and do NOT spawn Chrome. The persistent->temp profile fallback + the dead-session guard carry over unchanged. `Close()` is a no-op if the browser was never launched (no orphan process).

### Verification

- `TestLazyBrowserLaunch` (live): right after server connect, **0** debug-Chrome processes (no eager launch); `where`/`find` before navigate return "no page snapshot yet" without launching; the first navigate launches Chrome + the page is reachable.
- Full live suite green (858s, 0 failures) - no regression: every op that needs a page still gets it (navigate/new-tab/back-forward-reload call `ensureBrowserLocked`; actions run against the cached tab and naturally error "no snapshot" if called before navigate). `govulncheck` 0 reachable.

## v2.2.0 - 2026-06-23 (reliability + optimization: sharper verdicts, nth-from-end, CSS-selector escape hatch)

A live head-to-head vs charlotte (the closest direct competitor - same token-efficient, AX-tree-first thesis) surfaced three concrete improvements. This release ships all three, verified against a clean re-run of the charlotte comparison.

### Sharper verdict on URL-stable reorders (the sort/SPA complaint)

A click that reorders the SAME DOM nodes (a product sort, a filter, an SPA re-render) leaves the backend-id set unchanged, so the element diff saw no add/remove/changed and the verdict read "no visible effect" - misleading on a sort that clearly happened. v2.2 adds an order-sensitive content signature (FNV-1a over the kept elements' role+name+value) to each tree; `Diff` sets `ContentChanged` when the signature shifts but no element was added/removed, and `InferVerdict` now returns **"page updated (URL stable; e.g. sort/filter/SPA re-render) - call see to refresh refs"** instead of "no visible effect". Element-level changes still win when present (signature is the fallback, not an override). The signature ignores refs, so a re-snapshot of an unchanged page never false-positives.

### `nth` from the end (the identical-buttons complaint)

`act`/`find` disambiguation was 1-based from the top only. On N identical "Add to cart" buttons you had to know N to pick the last. v2.2 accepts negatives: `nth=-1` = last match, `-2` = second-last (wraps so `nth=-N` = first). The saucedemo "add the priciest after a price sort" case is now `act "Add to cart" nth=-1` with no counting.

### CSS-selector escape hatch (closes charlotte's one real edge)

The one place a pure a11y-tree tool loses to a CSS-selector tool: elements the a11y tree drops (custom `div[role=widget]`, presentational `span`s with handlers, shadow-exposed controls). v2.2 adds a `selector` param to `find`, `click`, `fill`, and `act`:
- `find selector=".btn-x"` runs `querySelectorAll` and returns `[css]` lines with a `sel=` you pass back.
- `click`/`fill`/`act selector="..."` acts directly on `querySelector(sel)`, reusing the existing real-mouse click + native-value-setter fill machinery (same React/Vue reliability as the ref path). `act selector` auto-detects tag/type (click buttons/links, fill text inputs, select `<select>`).

This reaches off-tree widgets without adding a parallel action surface - the same verdict + delta path, just a different resolver.

### Verification

- `TestInferVerdictContentChangedReorder` / `TestInferVerdictNoChangeNoSignature` / `TestInferVerdictChangedBeatsContentChanged` / `TestSignatureStableAcrossRefReassign` (unit): the signature detects a pure reorder, ignores identical content, yields to element-level changes, and is ref-independent.
- `TestResolveIntentNthNegative` (unit): nth=-1 last, -2 second-last, -N wraps to first, out-of-range errors.
- `TestSelectorEscapeHatch` + `TestVerdictSortReorder` (live, hermetic data-URL pages): the off-tree span is unreachable by a11y find but reachable + clickable/fillable via selector; a real reorder verdict says "page updated" not "no visible effect".
- Full live suite green (651s, 0 failures). `govulncheck` 0 reachable.

### charlotte vs agent-browser (the live comparison, corrected)

A live head-to-head (8 scenarios: HN orientation, saucedemo login, saucedemo multi-step purchase, Wikipedia search+extract, dense find, read content, dynamic/wait, agent-friendliness) confirmed the v2.1 findings and corrected two errors in the initial report:
- **Corrected:** agent-browser DOES ship tab management (`tabs`) and screenshots (`screenshot`) - the initial report wrongly credited these to charlotte alone. charlotte's real exclusive edges shrink to: structural `diff`, session/cookie management, and drag-and-drop.
- **Confirmed (re-verified cleanly):** charlotte's click on saucedemo's React "Add to cart" buttons returns success but has NO effect - a fresh navigation to `/cart.html` after the click shows an empty cart. This is a silent failure (the agent thinks it worked), worse than an error. agent-browser's real-mouse + synthetic `click()` fires the handler reliably.
- **Now closed by v2.2:** charlotte's CSS-selector `find` edge (the selector escape hatch) and the agent-browser "no visible effect on sort" waffle (the content signature).

The full corrected report ships at `charlotte-vs-agent-browser-report.md`.

## v2.1.1 - 2026-06-23 (ugly-ARIA whitelist hardening + the ugly-end benchmark)

A reviewer pointed out the role whitelist is only as clean as the page's ARIA: on a messy SPA, decorative `div[role=button]` ads, `span[role=button]` with no handler, and other junk that used to live in dead markup moves UP into the semantic tree, where a static whitelist can't tell a meaningful control from a mislabeled one. This release adds a principled filter for the detectable junk, a benchmark that runs the snapshot against the ugly end, and the honest numbers.

### Decorative-junk filter (the headline)

- **Drop interactive elements that are unnamed AND not focusable.** A native `<button>`/`<a>`/`<input>` is always focusable, so real icon-only buttons and unlabeled inputs stay (the latter is what the `act` DOM fallback targets). A named custom widget stays even if unfocusable. Only an interactive role on an element with NO accessible name AND NO focus (a decorative `div[role=button]` / ad slot / `span[role=button]` with no handler) is dropped - the agent can't address it by intent (no name) and clicking it by ref would be a guess at a probably-decorative node.
- **What it does NOT drop (honest limit):** named junk stays. A `span[role=button]` with `aria-label="x"`, or ten buttons all labeled "Click here", are kept - a name is a name, and judging name quality / disambiguating duplicates is the agent's job, not the whitelist's. The filter targets the detectable decorative layer, not mislabeled-but-named controls.
- `PropertyNameFocusable` from the a11y tree is the signal.

### `bench/aria_mess` - the ugly-end benchmark

- Runs the snapshot against 11 synthetic pages that model the worst real ARIA pathologies (generic soup, decorative role=button, duplicate main, mislabeled controls, nameless icon buttons, link soup, landmark soup, ad-slot divs, a composite messy SPA) and reports per page: tokens, total refs, named refs, non-focusable refs, landmarks, duplicate main.
- Reproducible (synthetic pathologies, local fixtures). See `bench/aria_mess/README.md`.

### Measured (before -> after the filter)

| page | refs before | refs after | tokens before | tokens after |
|---|---|---|---|---|
| decorative-role-button | 8 | 3 | 31 | 15 |
| ad-slot-divs | 9 | 1 | 35 | 7 |
| messy-spa (composite) | 36 | 30 | 186 | 165 |
| generic-soup | 3 | 3 | 15 | 15 (already clean) |
| nameless-icon-buttons (real) | 8 | 8 | 33 | 33 (native buttons, focusable, kept) |

The filter removes the decorative junk the reviewer named; real icon-only buttons + unlabeled inputs + named custom widgets are preserved.

### Verification

- `TestBuildTreeDropsDecorativeUnfocused` (unit): drops unnamed+unfocusable, keeps named+unfocusable, keeps unnamed+focusable, keeps unlabeled inputs.
- Full live suite green (636s, 0 failures) - the filter drops no real control on real sites (Saucedemo, HN, Wikipedia, go.dev). `govulncheck` 0 reachable.

## v2.1.0 - 2026-06-23 (stable refs + task-success-per-token benchmark)

Refs were positional (r1..rN in tree order), reassigned on every snapshot. A page re-render that shifted tree order silently retargeted an old ref to a DIFFERENT control - the agent clicks the wrong element three steps ago and never knows. This release makes refs stable, adds a task-success-per-token benchmark (the honest metric, vs the snapshot-size table), and verifies both.

### Stable, backend-keyed refs (the headline reliability fix)

- **Same DOM node keeps the same ref across re-renders.** Refs are now assigned from a per-tab `backendNodeID -> ref` map with a monotonic counter (`snapshot.Tree.AssignRefs`), instead of positional `r{index}`. A ref the agent holds from a previous snapshot still points to the right element after the page mutates - eliminating the positional-collision failure where `r5` silently retargets to a different control after a re-render shifts tree order.
- **No stale-ref collision across navigation.** The ref map is cleared on navigation (a new document assigns fresh backend ids), but the counter stays monotonic, so a stale ref from an earlier page (a lower number) can't be reused for a different element on the new page. A confused agent holding an old ref gets a clean `ref not found; call see` instead of acting on the wrong control.
- **`reset` is a clean slate.** Reset zeroes the counter (the tool tells the agent all refs are reset + returns a fresh orientation), so post-reset refs restart low. Normal navigation keeps the counter monotonic (the bulletproof path); reset is the explicit fresh-start escape hatch.
- **Delta is sharper.** `Changed` elements keep their ref (same node, name/value differs), so a ref the agent holds for a changed control is still valid. `Added` get fresh refs; `Removed` carry the old ref (now invalid) so the breakage is explicit.
- Proven live: `TestStableRefsAcrossReRender` (a re-render that prepends an element before existing ones - old refs stay stable, the new one gets a fresh ref, and reading the old ref still hits the original control, not the newly-prepended one) + unit `TestAssignRefsStable`. Negative-verified: disabling `AssignRefs` makes the live test fail with the exact silent-retarget symptom (`ref r3 text: Item 3 (new)` instead of `Item 1`).

### Task-success-per-token benchmark (`bench/successtoken`)

- A new harness runs 5 multi-step tasks (login, search+extract, form fill+select+submit, multi-page nav, lazy-list scroll) on local fixtures against agent-browser AND `@playwright/mcp` head-to-head, measuring task success + total tool-I/O tokens (sent args + returned text, /4). The "agent" is a deterministic scripted policy so the comparison is fair + reproducible (token cost is the tool surface, independent of the LLM).
- Result (2026-06-23): both tools 5/5 (100%) success; **agent-browser 1142 tokens vs playwright-mcp 2337 tokens** (~2.05x fewer tokens at equal success). The win is intent-first `act` (resolve + act + verdict in one call, no per-action snapshot) + dense read/delta output vs the snapshot-type-snapshot pattern + verbose YAML snapshot. See `bench/successtoken/README.md`.

### Verification

- Full live suite green (zero failures); unit tests green; `govulncheck` 0 reachable; `go vet` + `go build ./...` clean (including the new bench package).
- Benchmark reproducible (identical char counts across two runs - deterministic scripts).

## v2.0.9 - 2026-06-22 (flawless act + hardened click + agent QoL)

A live-agent test (opencode) found `act` timing out on certain form layouts and `click` timing out enough to need `reset`. This release makes `act` flawless on poorly-labeled forms, hardens every action against wedging pages, fixes a latent `navigate back/forward/reload` hang, and adds agent QoL across the tool surface. 12 new live regression tests (gated by `AGENT_BROWSER_INTEGRATION=1`); full live suite green; `govulncheck` 0 reachable.

### `act` flawless (the headline)

- **DOM-attribute fallback.** `act` matched only the a11y name (what Chrome's AX tree computed from the label/placeholder/aria-label). An input with NO a11y name (no `<label>`, no placeholder, no aria-label - only a `name=`/`id=` the agent knows from HTML or `extract form`) was unreachable by intent. `act` now falls back to a DOM scan over `name`/`id`/`placeholder`/`title`/`aria-label` (and button/link text) on no a11y match - one extra CDP round-trip, only on no-match (the hot path is unchanged). Reproduced + fixed: `act "custcode"` on a name-only input now resolves `[dom] textbox "custcode" (fill)`.
- **Combobox with no value** now errors clearly ("pass a value to select an option") instead of clicking the dropdown open.

### Action hardening (the `click` wedge fix)

- **Soft-fail post-action re-snapshot.** A click/fill/`act` that fires but lands on a hanging navigation used to spend ~16s (8s AX-pull x 2 with retry) then return an `isError` that read like the click failed. The re-snapshot now does ONE pull (<=8s) and, on failure, returns a SOFT verdict ("action fired; page is loading or unreachable - call see to refresh refs") - the action succeeded; the agent re-sees for fresh refs. Reproduced: a click into a never-responding endpoint now returns in ~8s as a soft verdict (was 16.9s `isError`), and the session stays usable (a fresh navigate works immediately, no `reset` needed).
- **`navigate back/forward/reload` fixed (latent bug).** `chromedp.WaitReady("body")` after a JS-triggered `history.back()/forward()/location.reload()` hangs on a stale execution context (bfcache-cached pages don't fire the document-updated event chromedp tracks). Replaced with a `readyState` + `document.body` JS poll that re-resolves the target's context each call. This was failing reliably (30s timeout) on both local servers and real sites; now ~10-20s and correct.
- **`isFatalBrowserErr` hardened.** "context canceled" is now only fatal when the browser session ctx itself is done, not when a single tab's ctx is cancelled (a tab close/reset, a mid-nav context tear-down). The old classification could mark a healthy browser dead + force a needless `reset`. This is also what lets the back/forward poll survive a transient mid-nav context error.

### Correctness

- **`select` errors on a no-match option** instead of silently no-op'ing (the old `selectJS` returned the old value with no error; the agent couldn't tell the option wasn't found).
- **Failed actions are recorded in `history`.** `history errors=true` now shows blocked (CHALLENGE) and failed (`error:`) actions - a click that timed out, an `act` that found nothing, a fill that threw - not just CHALLENGE verdicts.

### Less-destructive `reset`

- `reset` no longer always relaunches the whole browser. If the browser is alive (the common case: a tool timed out, an SPA is unresponsive), it re-navigates the CURRENT tab to a fresh page (`url`, or `about:blank`) and KEEPS your other tabs + their logins. It only does a full Chrome relaunch (other tabs lost) when the browser is actually dead. Best of the v2.0.1 new-tab swap + the v2.0.2 full-relaunch recovery.

### Agent QoL

- **`press_key` optional `ref`**: focus a specific element first, so `press_key Enter ref=r3` submits that input's form without a separate click/fill.
- **`wait` default seconds**: `wait url=/dashboard` with `seconds=0` defaults to 10s instead of an instant timeout (a common agent slip).
- **`eval` unquotes string results** (`document.title` -> `Title`, not `"Title"`) and now serializes object results to JSON (objects previously returned empty, by reference).
- **`where` shows the current tab** (id + label + tab count) for multi-tab orientation.

### Hygiene

- Fixed a comment typo in the `dead`-field doc (a literal `\t//` baked into the comment text).

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
