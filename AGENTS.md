# Repository Guidelines

## Project Structure & Module Organization

`gmcli` is a Go 1.25 CLI for Google Messages. `main.go` wires into the Cobra command tree in `cmd/`. Keep user-facing verbs and flags in `cmd/`, and reusable implementation in `internal/`:

- `internal/gm/`: session, pairing, sync events, send/react, media.
- `internal/store/`: SQLite schema, queries, aliases, message/contact persistence.
- `internal/sync/`: event-to-store synchronization.
- `internal/output/`: JSON and tabular rendering helpers.
- `internal/paths/` and `internal/logging/`: environment paths and logging setup.
- `skills/google-messages/`: bundled assistant skill; update when agent-facing CLI behavior changes.
- `docs/research/`: design notes and protocol research.

Tests live beside their packages as `*_test.go`.

## Build, Test, and Development Commands

- `go build -o gmcli .` builds the local binary.
- `go build -ldflags "-X github.com/fdsouvenir/gmcli/cmd.Version=$(git describe --tags --always --dirty)" -o gmcli .` builds with version metadata.
- `go test ./...` runs the full test suite used by CI.
- `go vet ./...` runs static checks used by CI.
- `./gmcli --help` or `./gmcli <command> --help` validates command wiring while developing.

Use `--store <tmpdir>` when exercising live commands to avoid touching real state.

## Coding Style & Naming Conventions

Use standard Go formatting: run `gofmt` on changed Go files. Package names are short lowercase nouns (`store`, `paths`, `output`). Exported identifiers need clear doc comments when part of cross-package APIs. Keep command names and flags lowercase and shell-oriented, for example `history backfill`, `--read-only`, and `--json`.

## Testing Guidelines

Prefer focused table-driven tests in the package under change. Name tests after behavior, such as `TestAliasesSetAndResolve` or `TestVersionCommandUsesInjectedVersion`. For CLI behavior, use the existing command test patterns in `cmd/*_test.go`. Any schema or query change in `internal/store/` should include coverage for migration/query behavior.

## Commit & Pull Request Guidelines

Recent history uses short imperative subjects, sometimes with Conventional Commit prefixes: `fix: harden live sync smoke paths`, `docs: fix build command`, `chore: prepare alpha release`. Keep subjects concise and specific.

Pull requests should describe the behavior change, list test commands run, and call out live-device implications for auth, sync, send, history, or media changes. Include terminal output when changing human-readable command output.

## Security & Configuration Tips

This project handles local message archives and session tokens. Do not commit generated databases, `session.json`, downloaded media, or logs. Preserve read-only defaults for commands that mutate phone state, and require explicit opt-in for sends or reactions.
