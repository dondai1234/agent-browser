# Contributing to goshawk

Thanks for your interest. goshawk is a small, focused project: 9 tools, one Go binary, Chrome via CDP. The bar is "works on real sites, fewer tokens, clearer tool defs."

## Setup

Requires [Go](https://go.dev) 1.26+ and Chrome or Chromium installed.

```sh
git clone https://github.com/dondai1234/goshawk.git
cd goshawk
go build ./...
go vet ./...
go test ./...
```

Integration tests self-skip without a reachable Chrome. To run them:

```sh
AGENT_BROWSER_INTEGRATION=1 go test -count=1 ./... -timeout 600s
```

## Before you code

Open an issue for anything beyond a typo or obvious fix. A short proposal saves time if the change is out of scope or duplicates work in progress.

## What we look for

- **One behavior per PR.** A focused diff is reviewable. A grab-bag isn't.
- **Real-site verification.** If you fix `act`, prove it on a real page (example.com, saucedemo, the-internet) with an assertion that could have failed. A test that passes because the code is right, not because it catches the bug, doesn't count.
- **Tool descriptions are the contract.** If you change a tool's behavior, update its description in `internal/mcpserver/tools_*.go`. The agent masters the tool from the def alone. An agent that has to guess is a bug.
- **Token efficiency.** Don't add a tool or grow output without a concrete call-count or token win. Prefer folding capability into an existing tool over adding a new one.
- **Keep a Changelog.** Add a line under the right `### Added/Changed/Fixed` section in `CHANGELOG.md` for the next release.

## Commit conventions

- Conventional-ish subjects: `act: fall back to DOM name on no a11y match`. Short subject is enough.
- Keep the CHANGELOG entry in sync with the version in `internal/mcpserver/server.go` (`Version` const).
- Run `gofmt -l internal/ cmd/` before pushing. An unformatted diff is a review round-trip.
- Mark breaking changes explicitly in the PR description and the CHANGELOG.

## What not to do

- Don't add a dependency without a reason and a pinned version.
- Don't commit `STATUS.md`, scratch files, or anything under the personal/agent block in `.gitignore`.
- Don't disable a test to make the suite green. Fix the behavior.
