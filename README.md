# gmcli

A standalone Go CLI that connects to Google Messages, archives conversations
into a local SQLite + FTS5 database, and exposes a query surface suitable for
shell use and LLM tool integrations.

> **Status:** beta. Pairing, session persistence, sync loop, query CLI
> (`messages`, `contacts`, `chats`), best-effort history backfill, send
> commands, media download, and an LLM skill (`skills/google-messages`) are
> wired up and covered by the automated test suite. Live-device behavior still
> depends on the unofficial Google Messages web protocol, so validate auth,
> sync, history, media, and send flows on your own account before relying on
> unattended operation. See
> [`docs/research/phase-1-libgmessages.md`](docs/research/phase-1-libgmessages.md)
> for the design notes that motivated this layout, and
> [`skills/README.md`](skills/README.md) for the skill installation guide.

## What it is

- **Standalone.** No Matrix server, no Docker, no bridge daemon. Just a Go
  binary, a SQLite file, and a phone running Google Messages.
- **Read-first.** Phone-mutating operations (sending texts and reactions) are
  gated behind explicit flags. The default is to observe, not to send.
- **Local.** Messages live in a single SQLite database under your data
  directory (XDG-compliant). Nothing is uploaded anywhere.
- **AGPL-3.0.** gmcli imports `pkg/libgm` from
  [mautrix/gmessages](https://github.com/mautrix/gmessages), which is licensed
  AGPL-3.0. That makes gmcli a derivative work and obligates the same license
  for the whole program. See `LICENSE` and `NOTICE`.

## How it works

`pkg/libgm` reverse-engineers the Google Messages web client protocol. After a
one-time QR pairing handshake, it maintains an authenticated session with
your paired phone — all messages flow through the phone, which proxies them
to Google's relay infrastructure. gmcli wraps that session with an event loop
that writes incoming messages, conversation updates, and contact data to a
local SQLite database, and exposes the database through a CLI.

The phone must be online and have Google Messages installed for the relay to
work. Pairing tokens are refreshed automatically; full re-pairing is required
roughly every 14 days of inactivity (Google's policy, not ours).

## Install

Requires Go 1.24 or newer.

```sh
git clone https://github.com/fdsouvenir/gmcli
cd gmcli
go build -o gmcli .
```

For a source build whose `gmcli version` output includes the current tag or
commit, inject it at link time:

```sh
go build -ldflags "-X github.com/fdsouvenir/gmcli/cmd.Version=$(git describe --tags --always --dirty)" -o gmcli .
```

Pre-built binary distribution and Homebrew packaging are planned after the
initial beta releases.

## Current limits

- Live-device coverage is still limited. Before relying on gmcli unattended,
  test `auth`, `sync`, query commands, `history backfill`, `media download`,
  and a deliberate send with your own Google Messages account.
- Sending prefers real phone `Settings`/SIM metadata when available, but can
  fall back to gmcli's older minimal request shape. `gmcli sync send-settings`
  is available when you want to inspect or refresh the preferred metadata path.
- History backfill is best-effort and depends on what Google Messages returns
  through the paired phone.
- The phone must be online for sync, backfill, sends, and media downloads.
- The SQLite database is local but unencrypted. Use filesystem encryption if
  you need at-rest protection.
- The protocol depends on the unofficial `libgm` reverse-engineered Google
  Messages web protocol and can break if Google changes that protocol.

## Quick start

```sh
# 1. One-time pairing (renders a QR code in the terminal — scan with the
#    Google Messages app on your phone, Settings → Device pairing → QR code).
gmcli auth

# 2. Sync messages from the phone into the local database. --follow keeps
#    the connection open and writes new messages as they arrive.
gmcli sync --follow

# 3. Query the local archive (read-only).
gmcli chats list                              # most-recent conversations
gmcli chats show <conversation-id>            # header + recent messages
gmcli messages search "dinner"                # FTS5 across all conversations
gmcli messages list --conv <conv-id>          # message list with filters
gmcli messages show <message-id>              # single message detail
gmcli messages context <message-id>           # surrounding messages
gmcli contacts search alice                   # name/number/alias substring match
gmcli contacts show <participant-id-or-num>   # contact detail

# 4. Local-only labels.
gmcli contacts alias set --id <pid> --alias "Mom"
gmcli contacts alias list                     # list all set aliases
gmcli contacts alias rm --id <pid>

# 5. Best-effort history backfill, modeled after wacli.
gmcli history backfill --chat <conv-id> --requests 10 --count 50
# JSON output reports protocol records separately from the chat message delta:
# fetched_messages, sync_records_processed, messages_before, messages_after,
# messages_added_for_chat.

# 6. Write to the phone (always requires --read-only=false).
gmcli sync send-settings
gmcli --read-only=false send text --to <conv-id> --message "on my way"
gmcli --read-only=false send react --message <msg-id> --emoji "👍"
gmcli media download --message <msg-id>
# `send text` only reports success after Google Messages echoes the outgoing
# message back with its canonical message_id.
# `sync send-settings` is a read-only network diagnostic that refreshes the
# local Settings/SIM metadata cache used by the preferred send request shape.

# Every command supports --json for machine-readable output and --full to
# disable truncation in tables.
gmcli --json chats list | jq '.[0].name'
```

## Global flags

| Flag             | Default                            | Purpose                                                  |
| ---------------- | ---------------------------------- | -------------------------------------------------------- |
| `--store DIR`    | `$XDG_STATE_HOME/gmcli`            | Where session, SQLite, and downloaded media live.        |
| `--read-only`    | `true`                             | Block commands that send texts or reactions through the phone. |
| `--json`         | `false`                            | Emit machine-readable output.                            |
| `--full`         | `false`                            | Disable truncation in tabular output.                    |
| `--log-level`    | `info`                             | Verbosity (`trace`/`debug`/`info`/`warn`).               |

## Layout

```
cmd/                  Cobra command tree (auth, sync, version, doctor,
                      messages, contacts, chats, send, media)
internal/
  gm/                 libgm wrapper — pairing, session, events, send/react,
                      WaitForReady, DownloadMedia
  store/              SQLite + FTS5 store (schema v3: aliases + send settings cache)
  sync/               Event-to-store pump
  output/             Shared JSON / tab-aligned table renderers
  paths/              XDG path resolution (XDG_STATE_HOME)
  logging/            zerolog setup
skills/
  google-messages/    LLM skill bundle - archive playbook for assistants
docs/research/        Phase 1 research notes
```

## LLM integration

The bundled OpenClaw skill lives in `skills/google-messages`. It is published
on ClawHub as
[Google Messages Local Archive](https://clawhub.ai/fdsouvenir/google-messages-local-archive)
(`google-messages-local-archive`) for searching, summarizing, and answering
questions from a local Google Messages SMS/RCS archive with read-only commands
by default.

## Privacy

- All data is local. gmcli does not phone home.
- Session tokens are stored in `$XDG_STATE_HOME/gmcli/session.json` with mode
  0600.
- Media attachments are referenced by ID in the database; bytes are not
  downloaded by default. Use `gmcli media download --message <message-id>`
  for explicit downloads.
- The SQLite file is unencrypted. If you need at-rest encryption, layer your
  own filesystem encryption (FileVault, LUKS, etc.).

## Attribution

- **libgm** — the Google Messages protocol library this CLI depends on — was
  written by Tulir Asokan and the
  [mautrix](https://github.com/mautrix/gmessages) contributors. License:
  AGPL-3.0. gmcli would not be possible without their reverse-engineering
  work.
- The CLI verb structure is inspired by Peter Steinberger's
  [wacli](https://github.com/steipete/wacli) for WhatsApp.
- Storage and MCP-tool patterns draw from
  [openmessage](https://github.com/MaxGhenis/openmessage) by Max Ghenis,
  released under the Unlicense.

## License

GNU Affero General Public License, version 3 or later. See `LICENSE` for the
full text and `NOTICE` for the third-party notices required by upstream
licenses.
