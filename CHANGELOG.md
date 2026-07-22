# Changelog

All notable changes to **goshawk** are documented here.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [4.0.0] - 2026-07-22

Major upgrade: profiles, batch form filling, confidence-scored verdicts,
self-healing refs, and login improvements. Same 9-tool surface - every new
feature folds into an existing tool via a new parameter or internal improvement.

### Added

- **Named profiles** (`session mode=profile`): create, switch, list, delete,
  export, and import isolated browser profiles. Each profile is a separate Chrome
  user-data-dir with its own cookies, localStorage, auth, and history. Switch
  in one call (the browser relaunches with the new identity). Export/import
  cookies + localStorage as JSON (Playwright storageState-compatible). Resolves
  [issue #1](https://github.com/dondai1234/goshawk/issues/1).
- **Batch form filling** (`act fields={...}`): pass a map of field labels to
  values and goshawk resolves each label to a form field, auto-detects the type
  (text input, checkbox, radio, select, custom combobox, slider, file input),
  and performs the right action in one call. Re-snapshots once and reports
  validation errors. One call instead of N for filling a whole form.
- **Confidence-scored verdicts**: every act verdict now includes a confidence
  tag - `[confirmed]` (DOM changed or XHR 2xx fired), `[likely]` (content
  shifted or XHR fired), or `[uncertain]` (no visible effect). Tells the agent
  when to trust the verdict and when to verify with `see`.
- **Self-healing refs**: when a ref is stale (the page re-rendered and the
  element's backend node is gone), goshawk auto-re-resolves by matching the
  element's role + name in the current tree. Conservative: only heals when
  exactly one match exists (no guessing). Saves a `see` round-trip in the
  common React re-render case.
- **Login improvements**: the `login` tool now detects and reports "remember me" /
  "keep me signed in" checkboxes, "forgot password" links, and SSO redirects
  (when the URL moves to a different domain after submit). The verdict includes
  all detected signals so the agent can act on them.

### Changed

- Instructions string rewritten for v4: adds profiles, form filling, confidence
  verdicts, and explicit DO NOT rules to force perfect agent usage.
- `session` tool description updated to document `mode=profile`.
- `act` tool description updated to document `fields=` for batch form filling.
- `login` tool description updated to document remember-me, forgot-password,
  and SSO redirect detection.

## [3.2.0] - 2026-07-04

Real-world fluency: the tool now handles the three things that break agents on
actual sites (login, consent overlays, custom dropdowns) plus the 2026 stealth
vectors. New `login` tool (9th) and three engine hardenings.

### Added

- `login` tool: universal one-call login. `login username= password= url=`
  detects the username + password + submit fields (heuristics from the
  logon-detector research: `type=email`, `autocomplete=username`, name/id/
  aria-label matching user/email/login/identifier, fallback the first text
  input in the password's form), fills them, submits, and reports a
  state-verified verdict: `logged in` | `2FA/mfa needed` | `CHALLENGE` |
  `error: <message>` | `still on login page` | `no login form found`. Handles
  single-step (most sites) and multi-step (Google/Microsoft/banks: username,
  then Next, then password) under one call. Detects OAuth/SSO buttons and
  reports them instead of auto-clicking. Verifies the resulting state, not the
  return status, so a silent failure is reported, not hidden.
- Cookie/consent banner auto-dismiss on every navigate. A high-confidence scan
  (OneTrust/Didomi/Quantcast/TrustArc/Cookiebot plus cookie-context scoring,
  reject preferred over accept) dismisses the banner; the orientation surfaces
  `consent: rejected cookies (onetrust)`. `--no-cookie-dismiss` disables. On by
  default, high-confidence only, so a real dialog is never dismissed.
- Custom combobox open-select. `act value=X` on a button+listbox dropdown
  (`aria-haspopup="listbox"`, the W3C pattern a native `<select>` can't express)
  opens the popup and clicks the matching `role=option`. A combobox whose
  accessible name is empty but whose value carries its label (Chrome's
  button-combobox quirk) is now addressable by intent.
- Stealth hardening for 2026 vectors: permission-API consistency
  (`Notification.permission='default'` paired with `permissions.query`
  returning `'prompt'`), nonzero `outerWidth/Height` (a headless=0 tell), and
  `navigator.connection` (undefined-in-headless tell).

### Verification

Real-site tests: `TestLoginSaucedemo` logs in to `/inventory.html`;
`TestLoginWrongPassword` returns `error: do not match` (not a silent success);
`TestLoginMultiStepLocal` completes a 2-step fixture; `TestCookieDismissLocal`
clears a OneTrust-style banner; `TestComboboxOpenSelectLocal` selects from a
W3C button+listbox; `TestStealthHardening` passes the example.com probes. Full
live suite green (398s, 0 failures). `govulncheck` 0 reachable.

## [3.1.0] - 2026-06-27

Reliability and targeting pass from a live opencode head-to-head: `see refs`
came back empty on the first call (needed a retry) and intent targeting forced a
CSS-selector fallback. Both fixed at the root.

### Fixed

- No more empty `see refs`. `pullAXLocked` now waits for
  `document.readyState === 'complete'` (bounded, non-fatal) before the AX
  signature poll, so a heavy page can't settle on a stable-but-incomplete
  skeleton. `buildTree` retries once on an empty tree, and `see`/`find` self-heal
  (re-pull if the cached tree is thin). 150 ref-lines on the first call on
  Wikipedia, where it was empty before.
- Precise intent targeting. `value=` is for fillable/combobox only (the `act`
  contract), so intent resolution now restricts to fillable/combobox when a
  value is supplied. An exact-named clickable (Wikipedia's "Search" button) can
  no longer outrank the search input. The DOM-attribute fallback got the same
  role-aware filter and scoring.

### Added

- `js` helpers: `prop(x,name)` (element property), `form(sel)` (serialize a form
  to `{name: value}`), `meta(name)` (`<meta>` content by name/property).

## [3.0.0] - 2026-06-27

Ground-up rebuild of the tool surface for the agent that uses it. v2 grew to 22
tools and the agent danced between `see`/`extract`/`read`/`find`/`eval` guessing
selectors. v3 collapses them into 8 composable tools an agent masters from the
defs alone, with a first-class JS extraction path so getting data is one call,
not a ping-pong. The chromedp engine (lazy launch, per-op timeout, dead-session
tracking, stable backend-keyed refs, AX pull + iframe merge, verdicts/deltas,
stealth) is retained unchanged.

### Changed

- 22 tools to 8: `nav`, `see`, `act`, `js`, `find`, `tabs`, `history`,
  `session`. Connect cost drops from ~3,750 to ~1,900 tokens.
- Module path bumped `/v2` to `/v3` (Go major-version requirement).

### Added

- `js` as the structured-data hero. Run JS with an injected helper API (`$`,
  `$$`, `text`, `attr`, `html`, `visible`, `data`, `table`, `links`, `rect`,
  `xpath`, `frame`, `wait`) and get clean JSON back. `await="sel"` waits first.
  Auto-detects expression vs statement-body scripts (SyntaxError retry).
  Replaces `eval` + `extract` + `collect`.
- `nav` returns an orientation: page type, auth state, top primary actions with
  refs, regions, counts. `back`/`forward`/`reload`, `newTab=true`.
- `see level=outline`: the semantic skeleton (headings/tables/lists/forms/
  regions) each with a working CSS selector, so scraping starts from the right
  selectors instead of guessing.
- `find` bridges refs and selectors: by role/text to refs, by `selector=` to
  matches, `selectors=true` for both.
- `act` unifies click/fill/select/hover/press/upload/wait into one call with
  optional `waitUrl=/waitText=/waitGone=`. Ambiguous matches return ranked
  candidates; disambiguate with `nth` or `role`.
- `session mode=reset|clear` fuses v2's reset + clear.

## [2.4.0] - 2026-06-27

Token and call efficiency for real agent workflows.

### Added

- `collect` tool: pass `fields={label:selector}`, get `{label: value}` JSON in
  one call. `attrs={label:attrName}` pulls an attribute instead of text. A
  missing selector returns `null` for that label (a partial result, not an
  error). Tool count 20 to 22.
- `clear` tool: one-call clean slate. Wipes cookies + the current origin's
  localStorage/sessionStorage and reloads (or opens a url).

### Changed

- `extract` now targets a region: `selector=` scopes table/links/list/article;
  new `text` kind returns each match's text as a JSON array; `maxChars=` caps
  the response.
- `select` takes a `selector`, bringing it to parity with click/fill/act for
  unlabeled dropdowns.
- `fill` description leads with the batched form path (`fields={ref: value}` in
  one call). `eval` description reframed as the go-to for scattered scalars.

## [2.2.2] - 2026-06-25

### Added

- Idle auto-close. A background reaper tears Chrome down after `--idle-timeout`
  (default 10m) of no browser activity; the next navigate re-launches it
  seamlessly. `--idle-timeout 0` disables. The reaper polls at idleTimeout/4
  (clamped 5-60s), only acts when the browser is up and genuinely idle, and
  never races an in-flight op (it takes the session lock).

## [2.2.1] - 2026-06-25

### Changed

- Lazy browser launch. `New()` only constructs the session; Chrome spawns on the
  first page-opening op (navigate / new tab / back-forward-reload). Read-only
  ops called before the first navigate report "no page snapshot yet; call
  navigate first" and do not spawn Chrome. `Close()` is a no-op if the browser
  was never launched.

## [2.2.0] - 2026-06-23

A live head-to-head vs charlotte surfaced three improvements.

### Added

- `nth` from the end for `act`/`find`: `nth=-1` is last match, `-2` second-last
  (wraps so `nth=-N` is first).
- CSS-selector escape hatch for `find`, `click`, `fill`, `act`: reaches elements
  the a11y tree drops (custom `div[role=widget]`, presentational spans with
  handlers) via the same verdict + delta path.

### Fixed

- Sharper verdict on URL-stable reorders. A click that reorders the same DOM
  nodes (a sort, a filter, an SPA re-render) left the element diff empty, so the
  verdict read "no visible effect". An order-sensitive content signature now
  yields "page updated (URL stable; e.g. sort/filter/SPA re-render)". Element
  changes still win when present.

## [2.1.1] - 2026-06-23

### Changed

- Decorative-junk filter: drops interactive elements that are unnamed AND not
  focusable (decorative `div[role=button]`, ad slots, `span[role=button]` with
  no handler). Named widgets and focusable native controls are kept. Uses
  `PropertyNameFocusable` from the a11y tree.
- `bench/aria_mess`: the ugly-end benchmark. Runs the snapshot against 11
  synthetic pages modeling the worst real ARIA pathologies and reports per page
  tokens, refs, named refs, non-focusable refs, landmarks.

## [2.1.0] - 2026-06-23

### Changed

- Stable, backend-keyed refs. Refs are assigned from a per-tab
  `backendNodeID -> ref` map with a monotonic counter, instead of positional
  `r{index}`. A ref the agent holds stays valid across re-renders; the map is
  cleared on navigation so a stale ref can't retarget to a different control.
  `reset` zeroes the counter for a fresh start.
- Delta is sharper: `Changed` elements keep their ref; `Added` get fresh refs;
  `Removed` carry the old (now invalid) ref so the breakage is explicit.

### Added

- `bench/successtoken`: task-success-per-token benchmark. 5 multi-step tasks vs
  `@playwright/mcp` with a deterministic scripted agent. Both 5/5 success;
  goshawk 1,142 tokens vs playwright-mcp 2,337 tokens (~2x fewer at equal
  success).

## [2.0.9] - 2026-06-22

### Added

- `act` DOM-attribute fallback: on no a11y-name match, scans name/id/
  placeholder/title/aria-label. An input with no accessible name but a `name=`
  or `id=` is now reachable by intent.
- `press_key` optional `ref`: focus a specific element first.
- `wait` default seconds: `seconds=0` defaults to 10s instead of an instant
  timeout.
- `eval` unquotes string results and serializes object results to JSON.
- `where` shows the current tab (id + label + tab count).

### Fixed

- Soft-fail post-action re-snapshot. A click/fill/`act` that lands on a hanging
  navigation now does one pull (<=8s) and returns a soft verdict ("action fired;
  page is loading or unreachable") instead of a 16s `isError` that reads like the
  action failed. The session stays usable, no `reset` needed.
- `navigate back/forward/reload` hang (latent bug). `chromedp.WaitReady("body")`
  after a JS-triggered history nav hangs on a stale execution context (bfcache
  pages don't fire the event chromedp tracks). Replaced with a `readyState` +
  `document.body` JS poll.
- `isFatalBrowserErr` hardened: "context canceled" is fatal only when the
  browser session ctx itself is done, not when a single tab's ctx is cancelled.
- `select` errors on a no-match option instead of silently no-op'ing.
- Failed actions recorded in `history` (`history errors=true`).

### Changed

- `reset` is less destructive: if the browser is alive it re-navigates the
  current tab and keeps your other tabs + logins; a full relaunch happens only
  when the browser is actually dead.

## [2.0.3] - 2026-06-21

### Fixed

- `tabs new` launching a second Chrome on a locked persistent profile. `NewTab`
  now derives new tabs from an existing tab's context (which carries the
  allocated `Browser`), not from `browserCtx` (whose `Browser` was nil).

## [2.0.2] - 2026-06-21

### Fixed

- Persistent-to-temp profile fallback: if Chrome can't start with the requested
  profile (locked by an orphan, corrupted), relaunches with a throwaway temp
  profile so the server stays alive.
- Dead-session guard (the chromedp panic fix): a fatal browser error marks the
  session dead; `run`/`runTimeout` short-circuit so a dead browser is never
  retried (fixes `close of closed channel` on retry).
- `reset` is now a full browser relaunch, recovering from a wedged tab or a
  crashed browser.
- Launch timeout separated from the op timeout (60s) so a slow Chrome cold-start
  doesn't fail `New` under a tight `--op-timeout`.

## [2.0.1] - 2026-06-21

### Added

- Per-operation timeout (`--op-timeout`, default 30s). Every CDP call is bounded;
  a hung page returns a timeout error and releases the session lock instead of
  wedging every tool.
- `reset` tool: the explicit recovery path. Drops the current tab and opens a
  fresh one at an optional url; other tabs are kept.

### Fixed

- `act` on an ARIA combobox (`<textarea>`-backed): probes the tag, fills an
  ARIA combobox instead of the no-op `selectJS`.
- `press_key` multi-char silent no-op: rejects anything that isn't a named key
  or a single character, redirecting to `fill`/`act` for text.
- `tabs switch`/`close` by label: both now accept the `label` field as a
  fallback when `id` is empty.

### Changed

- First-tab cancel no longer kills the browser: the first tab gets its own
  chromedp target, so `reset`/`close` on t1 closes only that tab.

## [2.0.0] - 2026-06-21

Cognition layer. v1 made snapshots cheap (delta act-and-see). v2 makes the agent
think in goals, not refs: the tool understands the page, acts on intent, and
reports a verdict.

### Added

- `act` (intent-first): pass a control's name; local heuristics resolve it (no
  LLM, no per-call cost) and do the default action for its role. Collapses find
  + click/fill + see into one call. Ambiguous matches return ranked candidates,
  never guesses.
- Verdicts on every action: `navigated to ...`, `dialog opened: ...`, `status:
  ...`, `changed: +N -M ~K`, `no visible effect`, `CHALLENGE: ...`. Non-nav
  actions fold in the XHR/Fetch responses that fired (`net: /api/cart 200`).
- `extract` tool: `table`, `links`, `list`, `form`, `article` kinds.
- `history` tool: a rolling action log (step/action/verdict/url), capped 200,
  queryable (`last=N`, `errors=true`).
- `see level=brief`: ~50-token page brief (type, auth, primary actions, regions,
  counts) over the a11y tree.
- `wait` semantic conditions: `url=`, `text=`, `gone=`.
- `fill` gains a `{ref: value}` map for a whole form in one call.
- Scroll awareness: `scroll` reports position (`more below` / `at bottom` /
  `fits viewport`).
- Challenge detection on every snapshot, not just navigate.

## [1.0.0] - 2026-06-19

First release. A token-efficient, anti-bot-aware browser-automation MCP server
on a purpose-built Go + chromedp engine (no Playwright, no Puppeteer, no Node).
Single static binary, cross-platform.

### Added

- 15 tools: `navigate`, `see` (minimal/summary/full), `find`, `read`, `click`,
  `fill`, `select`, `scroll`, `wait`, `screenshot`, `eval`, `tabs`, `upload`,
  `press_key`, `hover`.
- Token-efficient by design: dense ref-line snapshots, tiered observation, delta
  act-and-see. ~12-17x smaller snapshots than the major browser MCP servers.
- Anti-bot/stealth on by default (`--no-stealth`): `navigator.webdriver`
  patched, fingerprint spoofed, `--headless=new`, jittered real-mouse movement,
  `--proxy-server`, Cloudflare/captcha challenge detection and auto-wait.
- Real input where eval is unreliable: `press_key` fires native key events;
  `hover` triggers CSS `:hover` and JS mouseover.
- Persistence on by default (`--no-persist`): a Chrome profile keeps
  logins/cookies/localStorage across restarts.
- One binary, 2-command install: `go install .../cmd/goshawk@latest`.
- URL scheme allowlist, JS-dialog auto-accept, crash-aware AX rebuild, session
  mutex, `govulncheck` clean. Cross-platform: windows/amd64, linux/amd64+arm64,
  darwin/amd64+arm64.

[Unreleased]: https://github.com/dondai1234/goshawk/compare/v4.0.0...HEAD
[4.0.0]: https://github.com/dondai1234/goshawk/releases/tag/v4.0.0
[3.2.0]: https://github.com/dondai1234/goshawk/releases/tag/v3.2.0
[3.1.0]: https://github.com/dondai1234/goshawk/releases/tag/v3.1.0
[3.0.0]: https://github.com/dondai1234/goshawk/releases/tag/v3.0.0
[2.4.0]: https://github.com/dondai1234/goshawk/releases/tag/v2.4.0
[2.2.2]: https://github.com/dondai1234/goshawk/releases/tag/v2.2.2
[2.2.1]: https://github.com/dondai1234/goshawk/releases/tag/v2.2.1
[2.2.0]: https://github.com/dondai1234/goshawk/releases/tag/v2.2.0
[2.1.1]: https://github.com/dondai1234/goshawk/releases/tag/v2.1.1
[2.1.0]: https://github.com/dondai1234/goshawk/releases/tag/v2.1.0
[2.0.9]: https://github.com/dondai1234/goshawk/releases/tag/v2.0.9
[2.0.3]: https://github.com/dondai1234/goshawk/releases/tag/v2.0.3
[2.0.2]: https://github.com/dondai1234/goshawk/releases/tag/v2.0.2
[2.0.1]: https://github.com/dondai1234/goshawk/releases/tag/v2.0.1
[2.0.0]: https://github.com/dondai1234/goshawk/releases/tag/v2.0.0
[1.0.0]: https://github.com/dondai1234/goshawk/releases/tag/v1.0.0
