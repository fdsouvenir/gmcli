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

`pkg/libgm` reverse-engineers the Google Messages web client protocol. gmcli
uses Google Account (Gaia) pairing: it validates browser-exported Google
cookies, shows the matching emoji, and waits for confirmation in Google
Messages on the phone. After pairing it maintains an authenticated session
with the phone — all messages flow through the phone, which proxies them to
Google's relay infrastructure. gmcli wraps that session with an event loop
that writes incoming messages, conversation updates, and contact data to a
local SQLite database, and exposes the database through a CLI.

The phone must be online and have Google Messages installed for the relay to
work. Pairing tokens are refreshed automatically; full re-pairing is required
roughly every 14 days of inactivity (Google's policy, not ours).

## Install

Requires Go 1.25 or newer.

Install a published version using its module path (replace `VERSION` with an
existing release tag):

```sh
GOBIN="$HOME/.local/bin" go install github.com/fdsouvenir/gmcli@VERSION
```

Keep `$HOME/.local/bin` on your PATH. See the
[latest release](https://github.com/fdsouvenir/gmcli/releases/latest) for a
versioned install command and upgrade notes. For local development:

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
- Account pairing requires short-lived access to Google Account cookies from a
  private browser window. They are as sensitive as a signed-in browser session.
- The SQLite database is local but unencrypted. Use filesystem encryption if
  you need at-rest protection.
- The protocol depends on the unofficial `libgm` reverse-engineered Google
  Messages web protocol and can break if Google changes that protocol.

## Connection health

`doctor` is an offline evidence report, not a live connectivity test. Saved
pairing and legacy activity timestamps cannot establish health. Reports distinguish
`recently_verified`, `unknown`, and `unhealthy`; issues return a nonzero exit
status in both text and JSON mode. Quiet connections can become `unknown` after
15 minutes without observable archive confirmation, even while connected.
See [health semantics and schema compatibility](docs/connection-health.md).

## Quick start

### Pair with a Google Account

Run:

```sh
gmcli auth
```

1. gmcli opens a temporary Chrome, Chromium, or Edge window.
2. Sign in with the same Google Account selected under **Google Messages →
   Device pairing** on your phone. Complete any Google verification prompts.
3. The window closes automatically. For a new pairing, select the matching
   emoji shown in the terminal on your phone.

No cookie file, clipboard export, or developer tools are needed. gmcli uses a
separate temporary browser profile, captures only the credentials needed for
Google Messages, and removes the profile when finished or cancelled. It never
reads your existing browser profile. The saved session is private (mode 0600).

Chrome, Chromium, or Edge must be installed on the machine running `gmcli auth`,
and a desktop display must be available. To select a browser or allow more time:

```sh
gmcli auth --browser /path/to/chrome
gmcli auth --browser-timeout 10m
```

If `session.json` already contains a Gaia pairing, `gmcli auth` checks the account
and refreshes the session without a new emoji prompt. Pass `--new` to deliberately
create a new phone pairing. A failed or cancelled attempt leaves the existing
session file unchanged. Close the sign-in window or press Ctrl-C to cancel.
Then run `gmcli sync --follow` to refresh the archive.

QR pairing has been retired by Google. Existing QR sessions can still be queried
offline; running `auth` migrates to Google Account pairing.

#### Manual sign-in and remote servers

The browser flow runs on the same machine as gmcli. For a server without a
desktop, a browser Google refuses to sign into, or an automated input workflow,
`--cookies-file` still accepts a copied cURL request or cookie JSON. You can pipe
the clipboard directly without creating a file:

1. Open a private Firefox window and visit
   <https://accounts.google.com/AccountChooser?continue=https://messages.google.com/web/config>.
2. Sign into the account selected on the phone. Do not navigate elsewhere.
3. Open developer tools, reload once, select the `/web/config` network request,
   and choose **Copy as cURL** (bash format).
4. Pipe the clipboard to gmcli on the machine where you keep the archive:

```sh
# macOS
pbpaste | gmcli auth --cookies-file -
# Linux/Wayland
wl-paste --no-newline | gmcli auth --cookies-file -
# Linux/X11
xclip -selection clipboard -o | gmcli auth --cookies-file -
# Example for an SSH server (replace archive-host with your host)
pbpaste | ssh archive-host 'gmcli auth --cookies-file -'
```

The copied cURL is parsed as text, never executed. An existing private file is
also accepted: `gmcli auth --cookies-file /path/to/cookies.txt` (mode 0600).
JSON input must contain `SID`, `HSID`, `SSID`, `OSID`, `APISID`, and `SAPISID`;
`__Secure-1PSIDTS` is accepted when Google supplies it. Never put cookie values
in shell arguments, issue reports, logs, or chat messages. Clear the clipboard
and close the private window after pairing.

Google's Device Bound Session Credentials can prevent cookies being used
outside Chrome. The automatic flow disables that feature only for its temporary
browser process. The manual flow uses Firefox to avoid that restriction.
See the [upstream authentication documentation](https://docs.mau.fi/bridges/go/gmessages/authentication.html).

### Sync and query

```sh
# 1. Sign in in the browser and confirm on your phone.
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
gmcli send preflight
gmcli send inspect --to <conv-id>
gmcli --read-only=false send text --to <conv-id> --message "on my way"
gmcli --read-only=false send react --message <msg-id> --emoji "👍"
gmcli media download --message <msg-id>
# `send text` only reports success after Google Messages echoes the outgoing
# message back with its canonical message_id.
# `sync send-settings` is a read-only network diagnostic that refreshes the
# local Settings/SIM metadata cache used by the preferred send request shape.
# `send preflight` and `send inspect` are read-only diagnostics for live phone
# send state, default-SMS status, conversation send mode, and SIM/RCS metadata.

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
  browserauth/        Temporary browser sign-in and credential capture
  gm/                 libgm wrapper — pairing, session, events, send/react,
                      WaitForReady, DownloadMedia
  store/              SQLite + FTS5 store (schema v4: separate connection health evidence)
  sync/               Event-to-store pump
  output/             Shared JSON / tab-aligned table renderers
  paths/              XDG path resolution (XDG_STATE_HOME)
  logging/            zerolog setup
skills/
  google-messages/    LLM skill bundle - archive playbook for assistants
docs/research/        Phase 1 research notes
```

## Development checks

```sh
go test ./...
go vet ./...
# Optional real-browser test; Chrome/Chromium must be installed.
GMCLI_BROWSER_TEST=1 go test -race ./internal/browserauth
```

The browser test intercepts navigation and supplies synthetic cookies. It checks
capture and temporary-profile cleanup without a Google account or phone. Live
Google sign-in and phone confirmation still require a manual test.

## LLM integration

The bundled OpenClaw skill lives in `skills/google-messages`. It is published
on ClawHub as
[Google Messages Local Archive](https://clawhub.ai/fdsouvenir/skills/google-messages-local-archive)
(`google-messages-local-archive`) for searching, summarizing, and answering
questions from a local Google Messages SMS/RCS archive with read-only commands
by default.

## Privacy

- All data is local. gmcli does not phone home.
- Google cookies, pairing keys, and session tokens are stored together in
  `$XDG_STATE_HOME/gmcli/session.json` with mode 0600. Treat the file like a
  signed-in browser session; never commit, upload, or paste it into chat.
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
- Agent-facing CLI ergonomics are informed by the
  [Agent eXperience Interface](https://github.com/kunchenguid/axi) principles;
  gmcli adapts them around stable JSON compatibility and message privacy. See
  [`docs/research/axi-cli.md`](docs/research/axi-cli.md).

## License

GNU Affero General Public License, version 3 or later. See `LICENSE` for the
full text and `NOTICE` for the third-party notices required by upstream
licenses.
