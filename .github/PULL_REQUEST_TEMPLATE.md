<!-- Link related issue(s), e.g., Closes #123 -->

## Behavior change

<!-- Describe what changed and why. -->

## Files

<!-- List modified files and the rationale for each, e.g., `- cmd/sync.go — ...` -->

## Tests run

- [ ] `gofmt -l .` (no output)
- [ ] `go vet ./...`
- [ ] `go test -race ./...`

<!-- Note any additional checks run (e.g., `go build -o gmcli .`, `./gmcli <command> --help`, `GMCLI_BROWSER_TEST=1 go test -race ./internal/browserauth`, `shellcheck`, `govulncheck ./...`). -->

## Live-device notes

<!-- Call out live-device implications and what was or was not exercised for auth, sync, send, history, or media changes (use `--store <tmpdir>` to avoid touching real state), or state `N/A` if no live-device paths are affected. -->

## Terminal output

<!-- Include sample or before/after terminal output when changing human-readable CLI output or `--help` text, or state `N/A`. -->

## Checklist

- [ ] Read-only defaults (`--read-only`) preserved for commands that mutate phone state; explicit opt-in required for sends or reactions (or N/A)
- [ ] Bundled assistant skill (`skills/google-messages/`) updated if agent-facing CLI behavior changed (or N/A)
- [ ] Migration and query tests included if `internal/store/` schema or queries changed (or N/A)
- [ ] No generated databases, `session.json`, cookie files, downloaded media, logs, or personal archive data committed
