# Contributing to goshawk

Thanks for your interest in improving goshawk. This is a small, focused
project; the bar is "works on real sites, fewer tokens, clearer tool defs."

## Before you start

Open an issue first for anything beyond a typo or obvious bug fix. A short
proposal saves everyone time if the change is out of scope or duplicates work
in progress.

## Development setup

Requires [Go](https://go.dev) 1.26+ and Chrome/Chromium (auto-discovered).

```sh
git clone https://github.com/dondai1234/goshawk.git
cd goshawk
go build ./...          # must compile on windows/linux/macos
go vet ./...
go test ./...           # unit tests; integration tests self-skip without Chrome
```

To run the live integration suite (needs a reachable Chrome):

```sh
AGENT_BROWSER_INTEGRATION=1 go test -count=1 ./... -timeout 600s
```

## What we look for

- **One behavior per PR.** A focused diff is reviewable; a grab-bag isn't.
- **Real-site verification, not happy paths.** If you fix `act`, prove it on a
  real page (saucedemo, example.com, the-internet) with an assertion that could
  have failed. A test that passes because the code is right, not because it
  catches the bug, doesn't count.
- **Tool descriptions are the contract.** If you change a tool's behavior, update
  its description in `internal/mcpserver/tools_*.go` so the agent masters the
  tool from the def alone. An agent that has to guess is a bug.
- **Token efficiency.** The whole point. Don't add a tool or grow output without a
  concrete call-count or token win. Prefer folding capability into an existing
  tool over adding a new one.
- **No em-dashes in user-facing text** (README, CHANGELOG, tool descriptions).
  Use a colon, comma, or semicolon instead.
- **Keep a Changelog.** Add a line under the right `### Added/Changed/Fixed`
  section in `CHANGELOG.md` for the next release.

## Commit and PR conventions

- Conventional-ish commit subjects: `act: fall back to DOM name on no a11y match`.
  A short subject is enough; no body required for a small change.
- Keep the CHANGELOG entry in sync with the version in
  `internal/mcpserver/server.go` (`Version` const) and the `go.mod` module path.
- Update tests for your change. Run `gofmt -l internal/ cmd/` before pushing; an
  unformatted diff is a review round-trip.
- Mark breaking changes explicitly in the PR description and the CHANGELOG.

## What not to do

- Don't add a dependency without a reason and a pinned version.
- Don't commit `STATUS.md`, scratch files, or anything under the personal/agent
  block in `.gitignore`.
- Don't disable a test to make the suite green. Fix the behavior.
