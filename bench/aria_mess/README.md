# ugly-ARIA benchmark

Snapshot size on a clean docs page is the easy number. The honest test is what
the role whitelist keeps on the **ugly end** - the messy SPAs where junk that
used to live in dead markup moves UP into the semantic tree (a static whitelist
can't tell a meaningful control from a mislabeled one).

This harness runs the goshawk snapshot against 11 synthetic pages that
model the worst real ARIA pathologies and reports, per page, what the whitelist
keeps:

- **tokens** - chars in the summary render (/4 ~ tokens the agent pays)
- **total refs** - interactive + heading elements kept
- **named refs** - refs with a non-empty a11y name (a "useful" proxy)
- **non-focusable** - kept interactive refs that are NOT focusable (decorative
  `div[role=button]` with no tabindex - the junk a static whitelist can't
  otherwise drop)
- **landmarks** + **duplicate main**

## Pathologies

| page | what it models |
|---|---|
| `clean-docs` | a clean docs page (the contrast / easy case) |
| `generic-soup` | 10 nested `div[role=generic]` wrapping 3 real buttons |
| `decorative-role-button` | `div[role=button]` with no name/tabindex/handler + 3 real buttons |
| `duplicate-main` | 3 elements claiming `role=main` |
| `mislabeled-controls` | 10 buttons all `aria-label="Click here"` (named but low-signal) |
| `nameless-icon-buttons` | 5 `<button>` with only an svg + 3 named buttons |
| `link-soup-footer` | 60 links in a footer |
| `aria-on-noninteractive` | `span[role=button]` x10 (no tabindex) |
| `landmark-soup` | 5 nav + 3 main + 4 complementary |
| `ad-slot-divs` | `div[role=button]` ad placeholders (no name/tabindex) x8 |
| `messy-spa` | the composite ugly end: generic soup + duplicate main + 20 nav links + decorative ad divs + nameless icon buttons + a few real controls |

Synthetic (not a live crawl) so it's reproducible; each models a named real-world
failure mode. Use `-real` to also snapshot a few live sites for contrast.

## Run

```sh
go run ./bench/aria_mess/                # synthetic pathologies
go run ./bench/aria_mess/ -real          # also a few real sites (best-effort)
```

## Result (2026-06-23, after the v2.1.1 decorative-junk filter)

```
page                        tokens   refs  named nonfocus   lms dupmain
----------------------------------------------------------------------
generic-soup                    15      3      3        0     0       0
decorative-role-button          15      3      3        0     0       0
mislabeled-controls             65     10     10        0     0       0
clean-docs                      27      5      5        1     2       1
link-soup-footer               276     61     61        1     2       1
ad-slot-divs                     7      1      1        1     1       1
nameless-icon-buttons           33      8      3        0     0       0
duplicate-main                  17      3      3        3     3       3
landmark-soup                   38      8      8        3    12       3
messy-spa                      165     30     25        2     3       2
aria-on-noninteractive          43     10     10       10     0       0
```

### What this shows

- **generic soup does not inflate.** `generic` is not in the whitelist, so 10
  nested wrappers add zero refs/tokens. The most common SPA noise is handled.
- **decorative `role=button` junk is dropped** (v2.1.1 filter): an interactive
  element with no name AND no focus is removed. `decorative-role-button` 8->3
  refs, `ad-slot-divs` 9->1, `messy-spa` 36->30.
- **real icon-only buttons + unlabeled inputs stay**: native `<button>`/`<input>`
  are focusable, so `nameless-icon-buttons` keeps all 8 (the filter only drops
  the unnamed + unfocusable).
- **the honest limit - named junk stays.** `mislabeled-controls` (10x "Click
  here") and `aria-on-noninteractive` (named `span[role=button]`) are kept: a
  name is a name, and judging name quality / disambiguating duplicates is the
  agent's job, not the whitelist's. The filter targets the detectable decorative
  layer, not mislabeled-but-named controls.
- **duplicate/landmark soup is cheap**: landmarks are orientation-only (no refs),
  so 12 landmarks cost ~38 tokens, not 12 refs.

## Methodology / honesty

- "useful" is proxied by "has a non-empty a11y name" - an imperfect but
  measurable proxy. The real judgment of usefulness is the agent's; this measures
  what the whitelist *keeps*, which is the question.
- Synthetic pathologies model the named real-world failure modes reproducibly;
  they are not a live crawl of 20 random sites. `-real` adds a few live sites for
  contrast, but live ARIA drifts, so the synthetic set is the reproducible
  benchmark.
