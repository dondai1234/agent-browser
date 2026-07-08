## What does this PR change and why?

<!-- One or two sentences. If there's an open issue, link it ("Closes #123"). -->

## Verification

<!-- How did you prove it works on a real page, not just the happy path? -->
<!-- e.g. AGENT_BROWSER_INTEGRATION=1 go test -count=1 ./internal/mcpserver/ -run TestX -->

- [ ] `go build ./...` compiles on my OS
- [ ] `go vet ./...` is clean
- [ ] `gofmt -l internal/ cmd/` prints nothing
- [ ] Added/updated a test that could actually fail if the change were reverted
- [ ] Ran the relevant live test with `AGENT_BROWSER_INTEGRATION=1`

## Surface

- [ ] If a tool's behavior changed, its description in `internal/mcpserver/tools_*.go` was updated (the def is the contract)
- [ ] `CHANGELOG.md` has an entry under the next release's `### Added/Changed/Fixed`
- [ ] No em-dashes in README, CHANGELOG, or tool descriptions
- [ ] No `STATUS.md` / scratch files / secrets staged (`git status` clean of them)
- [ ] No new dependency without a pinned version and a reason
