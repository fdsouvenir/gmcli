# Evidence-aware health and Google-account pairing

## Health semantics (schema 4)

`doctor` is offline. `paired: true` means the saved session contains a browser
identity, **not** that Google accepts it or the phone is reachable. `pairing_mode`
is `gaia` when the session has Google's account destination registration identity,
otherwise `legacy_qr`. This is the upstream `IsGoogleAccount()` predicate;
`HasCookies()` also returns true for QR sessions and does not identify pairing mode.

The new `health` object persists separate receipt timestamps (Unix milliseconds):

- `process_heartbeat_ms`: sync started / five-minute process timer only.
- `transport` / `transport_observed_ms`: connection setup, interruption, recovery,
  or shutdown. Setup success alone is not proof of a responsive phone.
- `protocol_evidence_ms`: an actual protocol event or successful phone RPC.
  Relay token refresh counts here, never as a phone response.
- `data_evidence_ms`: a non-nil conversation/message with usable IDs. Settings,
  contact responses and phone pings do not qualify.
- `snapshot_verified_ms`: a successful, non-nil, structurally validated conversation
  list response, consistent with the existing archive. Empty pages with continuation
  are rejected. This proves visible-inbox access, not full historical completeness.
- `archive_state`: unknown, observed, or unverified after missing data; separate from
  transport status. `contacts_snapshot_empty` and `conversations_snapshot_empty`
  retain discrepancies until a corrected snapshot of the matching kind arrives.
- `phone_response_ms` / `phone`: actual phone data, successful list RPC (including
  empty responses), or upstream's phone-recovered event.
- `invalidation`: terminal error, logout, disconnect, or pairing change.
  It remains latched through late events until the next connection attempt,
  which clears old evidence before collecting new observations.

`health_status` is `recently_verified` only with a process heartbeat and real
phone/protocol **and meaningful archive data or a validated snapshot** evidence within 15 minutes and an observed transport. It is never
named `healthy`: an offline read cannot guarantee current connectivity.
Known failures are `unhealthy`; absent/stale proof is `unknown`. Both add issues
and make doctor exit nonzero, **including JSON mode** (JSON is still printed).
All timestamps describe observations, not process/service status probes.

Schema migration does not promote old `sync_state.updated_at` into evidence.
`last_sync_activity_time` remains for compatibility but is explicitly legacy and
must not be used for health. The five-minute timer no longer updates it.
Message creation dates never determine connection health: an old inbox
with recent meaningful archive data can be recently verified. An empty fresh store
can be verified by an explicit successful validated empty conversation snapshot;
phone/contact responses alone leave it unknown. Conversely, a running
process with no phone evidence cannot be green.

Successful initial snapshots are compared with the archive before import. Empty
contacts against stored contacts, or empty conversations against stored conversations
or messages, persist a snapshot-discrepancy reason and warning. This stays unknown
after later pings/data, until a corrected snapshot of the same kind is received.
A real nonempty conversation snapshot can repair a conversation discrepancy in
the same run; a contact response cannot. It is
not proof of QR retirement or disconnection: account selection or inbox changes
can also explain it. No archive rows are deleted.

`NoDataReceived` is recoverable: upstream longpoll.go emits it and sends GET_UPDATES,
not a terminal auth failure. It clears prior archive proof; actual data or a validated
conversation snapshot restores archive proof. Transport interruption clears old
phone/protocol/data/snapshot evidence; transport recovery alone cannot reuse it.
After terminal invalidation, late positive events and snapshots cannot mutate health
at all until an explicit new connection attempt.

The pinned libgm emits phone-recovered events after failures, but does not expose
all successful routine pings. A quiet connection can therefore become **unknown**
after 15 minutes even while connected. That means insufficient observable proof,
not stale messages or a diagnosed disconnection. This patch adds no recurring
network probes. Graceful disconnect invalidates health; an unclean process kill
can leave bounded recent evidence until the heartbeat expires.

## Gaia implementation and limitations

No dependency upgrade: `go.mau.fi/mautrix-gmessages v0.2605.0` already provides
`StartGaiaPairing` and `FinishGaiaPairing`. The implementation uses those two phases,
not `DoGaiaPairing`, avoiding its automatic reconnect after pairing. It saves the
session only after successful completion with a browser identity and pairing ID.
Failed pairing preserves the prior session file; health is conservatively
invalidated before pairing. Session writes use an unpredictable 0600 temporary
file followed by atomic rename, rather than a predictable `.tmp` path.

This is upstream's cookie-based Gaia flow, **not** an OAuth device-code login or
automated browser login. It needs a signed-in Google Messages browser session and
a phone on the same Google account. Upstream chooses the most recently seen
eligible device when an account has multiple devices; confirm the prompt on the
intended phone, and cancel otherwise. No live Google/phone validation was performed.

The caller supplies a five-minute context and disconnects on normal return. Upstream's
initial long-poll wait uses a WaitGroup without a context-aware wait; therefore
that phase is not guaranteed to honor the deadline. If it hangs, interrupt the
human process with Ctrl-C: Gaia deliberately does **not** use signalContext, leaving
SIGINT/SIGTERM at OS defaults. A subprocess regression test simulates a blocked
upstream startup and proves SIGINT terminates it. This limitation does not justify claiming an end-to-end verified
pairing flow or upgrading the dependency without further evidence.

## Human-only handoff (do not run through an agent)

After parent review and separate approval for live changes:

1. Back up the intended store/session and arrange for the existing sync process
   to be stopped by its operator before pairing. Never pair while another process
   may rewrite `session.json`. Keep a rollback copy for schema migration too.
2. Open a **private, unrecorded terminal outside OpenClaw/shared terminals** and
   your own browser at `https://messages.google.com/web/`. Sign into the account
   used by Google Messages on the intended phone.
3. In browser developer tools, Network, inspect a request to messages.google.com
   and privately copy its **Cookie request header value**. Required cookie names,
   matching upstream's connector: SID, HSID, OSID, SSID, APISID, SAPISID. Optional:
   `__Secure-1PSIDTS`. OSID is scoped to messages.google.com; the others to
   .google.com. If missing, revisit Google sign-in; do not invent values.
4. In that private terminal, run (store path must be selected by the human):

   ```sh
   /home/fredbot/repos/gmcli/artifacts/gmcli-health-gaia --store /path/to/intended-store auth gaia --human-terminal
   ```

   Paste the header into the hidden prompt, not the shell. Select the displayed
   matching emoji on the phone. Never share the cookie header, emoji, session file,
   screenshots, or terminal recording with an agent or in chat. Clear the clipboard
   afterward. No credentials belong in arguments, environment variables, files
   exported for an agent, logs, or shell history.
5. Pairing success only saves auth. A separately authorized sync run and genuine
   phone/data evidence are required before claiming connection recovery. The parent
   owns that live validation, installation, service handling, and rollback.

The command requires explicit human-terminal confirmation and terminal stdin,
stdout and stderr; JSON and pipes are rejected before reading credentials or
starting network work. Gaia upstream logging is disabled regardless of global
log level; returned upstream errors are replaced with fixed safe messages. The
emoji goes only to `/dev/tty`. TTY checks cannot detect recording software or an
agent-owned PTY: the explicit human-only operational boundary is essential.
Legacy QR auth remains available and is not automatically attempted as a fallback.

## Local validation

```sh
go test ./...
go vet ./...
go test ./internal/store ./internal/sync ./internal/gm ./cmd -run 'Health|Gaia|Doctor' -count=1 -v
go build -ldflags '-X github.com/fdsouvenir/gmcli/cmd.Version=9008b1c-health-gaia-candidate' -o artifacts/gmcli-health-gaia .
```

Tests use temporary stores and fake Gaia clients, never the real session or phone.

`sync send-settings` also no longer calls cached SIM metadata send-ready when the
current refresh times out. Cache availability is reported separately from a
successful response in this run.
