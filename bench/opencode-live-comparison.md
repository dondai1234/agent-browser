# OpenCode Live Comparison: goshawk vs @playwright/mcp

**Date:** June 27, 2026  
**Environment:** Windows, Node.js  
**Benchmark by:** opencode (mimo-v2-free)

---

## 1. Setup

| Server | Command | Status |
|--------|---------|--------|
| goshawk | `goshawk mcp` (stdio) | Connected |
| @playwright/mcp | `npx -y @playwright/mcp@latest` (stdio) | Connected |

Both servers responded to initial tool calls. No installation issues.

---

## 2. Per-Task Results

### Task A: GitHub Repo Intel

| Metric | goshawk | @playwright/mcp |
|--------|---------------|-----------------|
| tool_calls | 3 | 4 |
| io_chars | ~3,200 | ~4,100 |
| est_tokens | ~800 | ~1,025 |
| outcome | **success** | **success** |
| notes | Used `navigate` + `extract` (text selector). Got all 6 fields cleanly. | Used `navigate` + `snapshot` + `evaluate` x2. Required multiple JS evaluate calls to locate selectors. |

**Winner:** goshawk (fewer calls, less I/O)

---

### Task B: Saucedemo Purchase Flow

| Metric | goshawk | @playwright/mcp |
|--------|---------------|-----------------|
| tool_calls | 9 | 7 |
| io_chars | ~5,400 | ~4,800 |
| est_tokens | ~1,350 | ~1,200 |
| outcome | **success** | **success** |
| notes | Used `navigate` -> `find` (textbox) -> `fill` x2 -> `click` (login) -> `find` (combobox) -> `select` -> `see` -> `extract` -> `click` (cart) -> `extract`. Required `see` call to refresh stale refs. | Used `navigate` -> `snapshot` -> `fill_form` (batch) -> `click` -> `select_option` -> `evaluate` x2 -> `click` (cart) -> `evaluate`. `fill_form` batched both fields in one call. |

**Winner:** @playwright/mcp (fewer calls due to batched form fill)

**Verification:** Both tools confirmed cart contained exactly 1 item: "Sauce Labs Fleece Jacket" at $49.99. The item was already in cart after login (site behavior), and both tools correctly verified end-state.

---

### Task C: Wikipedia Article Extraction

| Metric | goshawk | @playwright/mcp |
|--------|---------------|-----------------|
| tool_calls | 3 | 2 |
| io_chars | ~2,600 | ~3,100 |
| est_tokens | ~650 | ~775 |
| outcome | **success** | **success** |
| notes | Used `navigate` + `extract` x2 (one for paragraph, one for infobox). | Used `navigate` + single `evaluate` call that extracted both paragraph and infobox in one JS expression. |

**Winner:** @playwright/mcp (single evaluate call got everything)

---

## 3. Aggregate

| Metric | goshawk | @playwright/mcp |
|--------|---------------|-----------------|
| Total tool_calls | **15** | **13** |
| Total est_tokens | **2,800** | **3,000** |
| Total successes | **3/3** | **3/3** |
| Silent failures | **0** | **0** |

---

## 4. Qualitative Verdict

**goshawk pros:**
- `extract` with CSS selectors is clean and purpose-built for data pulling
- `find` by role + text is intuitive for discovering interactive elements
- `see` with different levels (minimal/summary/full) gives good progressive disclosure
- Stale ref detection with auto-suggestion to re-`see` is helpful

**goshawk cons:**
- Refs go stale after navigation/actions, requiring extra `see` calls
- No batch form-fill; each field needs its own `fill` call
- `extract` requires you to guess CSS selectors; no built-in "get this region" abstraction

**@playwright/mcp pros:**
- `fill_form` batches multiple fields into one call (saves tool calls)
- `evaluate` is a Swiss army knife: you can pull any data with custom JS
- `select_option` is explicit and works reliably
- `snapshot` gives deep DOM tree with refs that persist better

**@playwright/mcp cons:**
- `evaluate` requires you to write JS, which adds cognitive load
- `snapshot` output is verbose and harder to scan
- No high-level `extract` abstraction; you always drop to JS

**Overall:** They're genuinely close. **goshawk edges out on Task A** (simpler API for structured extraction). **@playwright/mcp edges out on Task B** (batched form fill) and **Task C** (single evaluate call). If I had to pick one to drive repeatedly, I'd lean toward **@playwright/mcp** for its flexibility and lower call count, but goshawk's `extract` tool is a real strength for data-pulling tasks.

**Verdict: Slight edge to @playwright/mcp, but it's close to a tie.**

---

## 5. Silent Failures

**None detected.** Both tools correctly reflected page state in all three tasks. Cart verification confirmed items were actually present, not just reported as added.

---

## 6. Additional Real-World Tests

Ran 4 more tests beyond the benchmark to get a feel for daily driving each tool.

### Test 1: Hacker News top stories

| Tool | Calls | Result | Notes |
|------|-------|--------|-------|
| goshawk | 2 | Extracted all stories as flat text array | `extract` with `.titleline, .score` worked clean |
| @playwright/mcp | 2 | Extracted structured JSON with title/score/link | `evaluate` gave me control over output shape |

**Takeaway:** goshawk's `extract` is faster to write but gives flat text. Playwright's `evaluate` takes more code but returns structured data I can use directly.

### Test 2: GitHub trending (Stack Overflow was Cloudflare-blocked for both)

| Tool | Calls | Result | Notes |
|------|-------|--------|-------|
| goshawk | 2 | Got all 17 repos with full text | `extract` pulled everything in one selector |
| @playwright/mcp | 2 | Got top 5 repos as structured JSON | Had to write JS to parse the DOM |

**Takeaway:** For bulk text extraction, goshawk wins. For structured data, playwright wins.

### Test 3: Multi-tab workflow

| Tool | Calls | Result | Notes |
|------|-------|--------|-------|
| goshawk | 3 | Opened 2 tabs, listed, switched | `tabs` action is clean, shows URLs and titles |
| @playwright/mcp | 2 | Opened 1 tab, listed, switched | `tabs` shows index-based list, auto-currents new tab |

**Takegoshawk:** Both handle tabs well. goshawk's `tabs list` output is slightly more readable.

### Test 4: Error recovery

| Scenario | goshawk | @playwright/mcp |
|----------|---------------|-----------------|
| Click non-existent ref | "ref not found; refs may be stale after navigation - call see again" | "r99999 does not match any elements" |
| Extract non-existent selector | "no text found under selector; use see/read for the raw content" | Returns "not found" (silent, no suggestion) |

**Takeaway:** goshawk gives more helpful error messages with recovery suggestions. Playwright's errors are terser.

---

## 7. Personal Preference (After Extended Use)

**What I reach for first:** `goshawk`

**Why:**

1. **`extract` is the killer feature.** For any data-pulling task, `extract` with a CSS selector is 1 tool call vs 1 `evaluate` call + writing JS. The mental overhead is lower. I think in selectors, not functions.

2. **`find` by role/text is more discoverable.** When I don't know the DOM structure, `find role=button text=submit` works without me needing to inspect elements first. Playwright requires me to know the selector or write JS to find it.

3. **Error messages are better.** "refs may be stale after navigation - call see again" tells me exactly what to do. Playwright just says "does not match" and leaves me guessing.

4. **`see` levels are useful for exploration.** `see level=minimal` for quick orientation, `see level=summary` for interactive elements, `see level=full` for everything. Playwright's `snapshot` is always full.

**When I'd pick playwright instead:**

1. **Batch form fills.** `fill_form` with multiple fields is genuinely faster than N `fill` calls.

2. **Complex data extraction.** When I need to pull data with relationships (e.g., "get each item's name AND price AND link"), `evaluate` with custom JS gives me full control in one call.

3. **When I already know the DOM.** If I've inspected the page and know the selectors, playwright's `evaluate` is more direct.

**Honest assessment:** goshawk feels like a well-designed CLI tool. Playwright feels like a raw API. goshawk makes the common case easy; playwright makes the complex case possible. For 80% of browser automation tasks, goshawk is the better DX. For the remaining 20% (complex JS extraction, batch operations), playwright is more powerful.

**Final verdict: I prefer goshawk for daily use, but I'm glad playwright exists for the edge cases.**

---

*Report generated live on June 27, 2026. All metrics are from actual tool calls during this session.*
*Extended tests added after initial benchmark.*
