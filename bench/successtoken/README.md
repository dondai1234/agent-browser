# task-success-per-token benchmark

Snapshot size is the easy number. **Task success per token** is the honest one.
This harness runs a set of multi-step browser tasks against one or more MCP
browser tools and reports, per task and per tool:

- **success** — did the task's end-state assertion pass?
- **tokens** — total tool I/O chars (the JSON args the agent sends + the text the
  tool returns) divided by 4, the cost an LLM agent burns on tool round-trips for
  that task.

## Why a scripted agent (not an LLM)

The "agent" here is a **deterministic scripted policy**, not an LLM. This is
deliberate. The token cost an agent pays is the tool surface (inputs it sends +
outputs it reads), independent of which model drives it, so a fixed script makes
the comparison fair and reproducible. A real LLM agent adds its own reasoning
tokens on top, but those scale with the tool I/O it sees, so tool-I/O-per-success
is the right primitive to compare. Using an LLM would mix model reasoning cost
into a tool-surface comparison and make results non-reproducible.

## Tasks

Five multi-step flows on **local HTTP fixtures** (no network flakiness):

| task | flow |
|---|---|
| `login` | navigate, fill username + password, submit, assert "Welcome \<name\>" |
| `search-extract` | navigate, fill search, submit, assert "N results" rendered |
| `form-submit` | navigate, fill 3 fields, select a dropdown option, submit, assert summary |
| `multi-page-nav` | navigate page1, click "Next", click "Next", assert on page3 |
| `lazy-list-scroll` | navigate, scroll to load more, assert a late item present |

Each task has a script per tool (their surfaces differ). The runner just calls
`CallTool` and sums the sent-args + returned-text char counts.

## Run

```bash
# goshawk only (fast; builds the binary from source):
go run ./bench/successtoken/

# head-to-head vs playwright-mcp (needs npx + downloads @playwright/mcp on first
# run, ~1-2 min the first time):
go run ./bench/successtoken/ -compare

# inspect a tool's actual tool names + input schema before scripting it:
go run ./bench/successtoken/ -compare -list

# use a prebuilt binary + a longer per-runner budget:
go run ./bench/successtoken/ -compare -bin ./goshawk -timeout 10m
```

## Results (2026-06-23, goshawk v2.1 vs @playwright/mcp latest)

Both tools: **5/5 (100%) task success.**

| task | goshawk | playwright-mcp | ratio |
|---|---|---|---|
| login | 207 tok (5 steps) | 380 tok (6 steps) | 1.84x |
| search-extract | 152 tok (4 steps) | 361 tok (4 steps) | 2.38x |
| form-submit | 282 tok (7 steps) | 586 tok (8 steps) | 2.08x |
| multi-page-nav | 158 tok (4 steps) | 342 tok (6 steps) | 2.16x |
| lazy-list-scroll | 342 tok (8 steps) | 668 tok (8 steps) | 1.95x |
| **TOTAL** | **1142 tok** | **2337 tok** | **2.05x** |

Same success rate, ~half the tool-I/O tokens. The win comes from two places:

1. **Intent-first `act`.** goshawk resolves a control by name and acts in
   one call (`act "Username" value="alice"` returns a one-line verdict). The
   playwright-mcp path is snapshot -> type -> snapshot per action, because it
   needs a fresh snapshot's refs to address an element.
2. **Dense output.** goshawk's `read`/delta are compact text; the
   playwright-mcp `browser_snapshot` is a YAML accessibility tree with a page
   header and nested structure, so each read costs more chars.

These are the same levers as the snapshot-size table, but measured on real
multi-step tasks where the cost compounds over a flow, not a single snapshot.

## Methodology notes / honesty

- Tokens are chars/4 (the standard rough ratio). The absolute number is an
  estimate; the **ratio** between tools is what's meaningful and is exact (same
  fixtures, same assertions, deterministic scripts).
- Each tool uses its own efficient path (goshawk's `act`+`read`;
  playwright-mcp's `snapshot`+`type`+`click`). That is the point: we measure each
  tool the way an agent would actually use it, not a contrived equal-call-count
  script.
- playwright-mcp's snapshot assigns refs to **headings too**, so a label
  substring like "Search" can match a heading ("Search products") before the real
  control. The harness's `pwRef` skips non-interactive roles (heading/generic/text)
  so it addresses the actual control, the way a careful agent would.
- Results are reproducible: deterministic scripts yield identical char counts
  across runs (run twice to confirm).
