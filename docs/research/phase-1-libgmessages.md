# Phase 1 Research: libgm (mautrix/gmessages) Feasibility

**Date:** 2026-04-29
**Branch:** `claude/research-libgmessages-nF13t`
**Status:** Complete — recommendation at end.

## TL;DR

- **Package name:** `pkg/libgm` (not `libgmessages`). Importable as `go.mau.fi/mautrix-gmessages/pkg/libgm`.
- **Entanglement with the Matrix bridge:** zero. `pkg/libgm` has no imports of `maunium.net/go/mautrix` and no transitive bridge dependencies. Clean standalone library.
- **License:** AGPL-3.0 across the whole repo. `LICENSE.exceptions` only carves out Beeper and Element. gmcli must ship as AGPL-3.0.
- **Transitive deps:** all AGPL-3.0 compatible (MIT, BSD-3, Apache-2.0, ISC, MPL-2.0). One GPL-3.0 dep (`mauflag`) is bridge-only and never reached from `pkg/libgm`.
- **Prior art:** `openmessage` ([MaxGhenis/openmessage](https://github.com/MaxGhenis/openmessage)) is a mature project doing essentially what gmcli aims to do, plus more (WhatsApp/Signal/iMessage/gchat, web UI, macOS app, MCP). It is Unlicense (public domain). It uses a fork of mautrix/gmessages that diverges by a single commit (paginated conversation cursor).
- **In-tree example:** `pkg/libgm/gmtest/main.go` is a 137-line CLI demonstrating the entire libgm consumption pattern. Phase 2 can start from this skeleton.

**Verdict:** Clean extraction. Phase 2 is greenlit. Recommend a fresh AGPL-3.0 implementation that imports `pkg/libgm` directly from upstream (no fork) and borrows architectural patterns from openmessage for storage and CLI shape.

---

## 1. Repo layout (mautrix/gmessages)

```
pkg/
  libgm/            ← protocol library, what gmcli consumes
    crypto/         AES-CTR, AES-GCM, ECDSA, JWK
    events/         typed event structs
    gmproto/        generated protobuf (8 .proto files: auth, client, config,
                    conversations, events, rpc, settings, ukey)
    util/           constants, headers, URL endpoints
    gmtest/         ← reference CLI (137 lines)
    manualdecrypt/  utility
    pblitedecode/   utility
  connector/        ← Matrix bridge (do NOT consume)
cmd/
  mautrix-gmessages ← bridge daemon binary
```

`pkg/libgm` is its own importable Go package; it does not import `pkg/connector` or any Matrix code. `pkg/connector` imports libgm — the dependency graph points the right direction.

## 2. libgm public API (the consumer surface)

References use `pkg/libgm/<file>:<line>` from the cloned repo at commit pinned in
`go.mod` toolchain `go1.26.2`.

### Construction

```go
authData := libgm.NewAuthData()                          // client.go:148
client   := libgm.NewClient(authData, pushKeys, logger)  // client.go:155
client.SetEventHandler(func(evt any) { ... })            // client.go:186
```

`NewClient` takes:
- `*AuthData` — session state (cookies, tokens, device protos, crypto keys)
- `*PushKeys` — optional web-push subscription (nil is fine)
- `zerolog.Logger` — required, no abstraction

### Pairing

Two flows, both fully implemented in libgm:

| Flow              | Method                                | Output to user             |
| ----------------- | ------------------------------------- | -------------------------- |
| QR (browser-like) | `StartLogin()` → QR string            | scan QR with phone         |
| Google Account    | `DoGaiaPairing(ctx, emojiCallback)`   | confirm 2-character emoji  |

Both deliver `*events.PairSuccessful` to the event handler when complete; AuthData is mutated in place. After pairing, call `Connect()`.

### Connection / event loop

- `Connect()` — long-poll connection (`client.go:212`)
- `ConnectBackground()` — one-shot sync (`client.go:235`)
- `Disconnect()` / `Reconnect()`
- `IsLoggedIn()` / `IsConnected()`

Events flow through the registered handler as typed values:
- `*events.PairSuccessful`, `*events.ClientReady`, `*events.BrowserActive`
- `*events.AuthTokenRefreshed` — **libgm refreshes the auth token itself**; consumer just persists the new AuthData
- `*gmproto.Message` (wrapped in `*libgm.WrappedMessage` with `IsOld` flag)
- `*gmproto.Conversation`, `*gmproto.TypingData`, `*gmproto.SettingsEvent`
- Connection-state events: `PingFailed`, `ListenTemporaryError`, `ListenFatalError`, `ListenRecovered`, `PhoneNotResponding`, `PhoneRespondingAgain`, `NoDataReceived`
- `events.GaiaLoggedOut`, `events.AccountChange`

### Read methods (on-demand)

| Method                          | Returns                              |
| ------------------------------- | ------------------------------------ |
| `ListConversations(n, folder)`  | inbox/archived/spam list             |
| `ListContacts()`                | full address book                    |
| `ListTopContacts()`             | most-frequent contacts               |
| `GetConversation(id)`           | thread metadata                      |
| `FetchMessages(id, n, cursor)`  | paginated message history            |
| `GetParticipantThumbnail(...)`  | avatars                              |
| `DownloadMedia(id, key)`        | decrypts and returns bytes           |

(`FetchMessages` accepts a cursor, but at upstream the conversation list does
not — openmessage's fork adds `ListConversationsWithCursor`. See §6.)

### Write methods

`SendMessage`, `SendReaction`, `DeleteMessage`, `MarkRead`, `SetTyping`,
`UpdateSettings`, `Unpair`. gmcli's `--read-only` default means these are
gated behind explicit subcommands and an opt-in flag.

### Persistence contract

There is no `Store` interface — the consumer owns AuthData serialization. The
gmtest pattern is to JSON-encode `AuthData` to a session file:

```go
// pkg/libgm/gmtest/main.go:113
func saveSession() {
    file, _ := os.OpenFile("session.json", os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
    json.NewEncoder(file).Encode(&sess)
}
```

All proto fields on AuthData are `json:"...,omitempty"` tagged, so this works
out of the box. gmcli should follow the same pattern, gated on the
`--store DIR` flag (default `$XDG_STATE_HOME/gmcli`).

## 3. Bridge entanglement check

Searched `pkg/libgm/**/*.go` (excluding `_test.go`) for `maunium.net/go/mautrix`:
**zero results**. libgm only imports:

- stdlib (`crypto/*`, `net/http`, `encoding/*`, etc.)
- `github.com/google/uuid`, `github.com/rs/zerolog`
- `google.golang.org/protobuf/{proto,prototext,reflect,dynamicpb}`
- `go.mau.fi/util/{pblite,exslices,exsync,random}`
- `golang.org/x/crypto/hkdf`, `golang.org/x/exp/slices`

Nothing from the Matrix bridge framework. Importing `pkg/libgm` does **not**
pull mautrix-go, bridgev2, or any database dependency.

## 4. License audit

### Project license: AGPL-3.0

`/LICENSE` is the standard GNU AGPL-3.0 (Nov 2007). `/LICENSE.exceptions`
grants narrow distribution rights to Beeper (embedding in Beeper clients) and
Element (distributing compiled binaries). **No carve-out for libgm.** Any
project that imports libgm must also be AGPL-3.0 (or compatibly licensed) and
make source available under §13 if served over a network.

### Source headers

`pair_google.go:1-15` and others carry the canonical AGPL-3.0 header
attributing copyright to Tulir Asokan. Files without explicit headers fall
under the repo-level LICENSE.

### Transitive dep licenses (only deps reachable from `pkg/libgm`)

| Module                              | License      | AGPL-3.0 OK? |
| ----------------------------------- | ------------ | ------------ |
| github.com/google/uuid              | BSD-3-Clause | yes          |
| github.com/rs/zerolog               | MIT          | yes          |
| google.golang.org/protobuf          | BSD-3-Clause | yes          |
| go.mau.fi/util                      | MPL-2.0      | yes          |
| golang.org/x/crypto                 | BSD-3-Clause | yes          |
| golang.org/x/exp                    | BSD-3-Clause | yes          |
| filippo.io/edwards25519 (transitive)| BSD-3-Clause | yes          |
| github.com/coder/websocket          | ISC          | yes          |
| github.com/petermattis/goid         | Apache-2.0   | yes          |
| github.com/rs/xid                   | MIT          | yes          |
| go.mau.fi/zeroconfig                | MPL-2.0      | yes          |
| golang.org/x/{mod,net,sync,sys,text}| BSD-3-Clause | yes          |

**`maunium.net/go/mauflag` is GPL-3.0** and would be a one-way compatibility
issue if pulled in, but it is only used by `cmd/mautrix-gmessages` and the
bridge config layer — verified absent from `pkg/libgm` via grep. Safe to
ignore.

### Required attributions for gmcli

- `LICENSE` containing the full AGPL-3.0 text.
- `NOTICE` (or a section in `README`) attributing libgm to Tulir Asokan /
  mautrix and pointing at <https://github.com/mautrix/gmessages>.
- Credit to steipete/wacli for the CLI design.
- Source available wherever gmcli runs as a network service (the SQLite-only
  default isn't a network service, so §13 is dormant unless we add a server
  mode later).

No additional license texts need to be vendored beyond the upstreams Go
toolchain handles via `go.sum`.

## 5. Auth-refresh and 14-day re-pairing

The user's spec note "Session re-pairing needed ~every 14 days" is partially
correct. Two distinct token lifetimes exist:

1. **Tachyon auth token** — short-lived (~24h). libgm refreshes this
   automatically and emits `events.AuthTokenRefreshed`; consumer's only job is
   to persist the updated AuthData on that event. No user action.
2. **Browser pairing** — Google invalidates browser pairings after ~14 days
   of inactivity. Reflected in `events.GaiaLoggedOut` →`gmcli auth` must
   re-run the QR/Gaia flow. Consumer cannot prevent this; phone must be online
   periodically for libgm to keep the pairing alive.

Implementation implication for gmcli: persist AuthData on every
`AuthTokenRefreshed`. Treat `GaiaLoggedOut` as fatal in the `sync --follow`
loop and exit non-zero so the user/launcher knows to re-auth.

## 6. Prior art: openmessage

[MaxGhenis/openmessage](https://github.com/MaxGhenis/openmessage), Unlicense,
v0.2.9 as of 2026-04-21. Polyglot (Go backend + Swift macOS wrapper +
TS/React web UI + Playwright e2e). Subcommands: `pair`, `serve`, `demo`,
`send`, `send-group`, `import`, `debug-media`. Storage: SQLite via pure-Go
`modernc.org/sqlite`, FTS5 trigram tokenizer, source_platform/source_id
columns to dedup across SMS/RCS/WhatsApp/Signal/iMessage/gchat. Exposes 18
MCP tools over stdio + SSE. Defends against prompt injection by prepending
a preamble warning to all tool results that contain message bodies — worth
copying.

It uses a fork of mautrix/gmessages
([MaxGhenis/gmessages](https://github.com/MaxGhenis/gmessages)) pinned via
`replace` in go.mod. The fork is **one commit ahead of upstream**:
`a6d7f98 Add ListConversationsWithCursor for paginated conversation listing`.
That single change is small enough that gmcli should either:

- upstream it (PR to mautrix/gmessages), or
- vendor a tiny shim in our own code rather than `replace`-ing,

so we can stay on `go.mau.fi/mautrix-gmessages` directly without owning a
fork.

### What we should borrow from openmessage

- AuthData JSON persistence pattern (`internal/client/session.go`).
- SQLite schema with `(source_platform, source_id)` unique index even if we
  start single-platform (room to grow without migration pain).
- FTS5 trigram tokenizer with corruption auto-recovery
  (`internal/db/db.go:329-387`).
- MCP message-content preamble for prompt-injection defense
  (`internal/tools/tools.go:62-65`).
- `BackfillProgress` state machine for the import phase.

### What to skip

- The macOS Swift wrapper, web UI, and visualization layer — out of scope.
- WhatsApp/Signal/iMessage live bridges — gmcli is Google Messages only.
- Telemetry. Personal-archive tools should not phone home.
- Maintaining a fork of mautrix/gmessages.

### Build vs. fork

gmcli is *not* a fork or thin wrapper of openmessage. It's a leaner CLI-only
tool with tighter scope (single platform, read-first, OpenClaw integration).
But the architectural lessons translate one-for-one. Many internal files in
gmcli will look very similar to openmessage's; that's fine — we own the code,
and the Unlicense allows direct copying where it makes sense.

## 7. The in-tree gmtest reference

`pkg/libgm/gmtest/main.go` is the authoritative minimal example. 137 lines.
The skeleton shows:

- Loading or creating AuthData from `session.json` (lines 42–55).
- `cli = libgm.NewClient(&sess, nil, log)` (line 57).
- `cli.SetEventHandler(evtHandler)` and a switch on `rawEvt.(type)`
  (lines 119–136).
- `cli.DoGaiaPairing(ctx, emojiCallback)` for first-run auth (line 60).
- `cli.Connect()` for subsequent runs (line 67).
- `defer saveSession()` to persist AuthData on exit (line 83).
- A trivial stdin command loop calling `cli.ListContacts()`, `cli.ListTopContacts()`, `cli.GetConversation(id)`.

Phase 2 should treat this as the starting skeleton for the `gmcli sync` and
`gmcli auth` plumbing, then layer the SQLite store and Cobra-based CLI on top.

## 8. Open questions resolved

| Original question                                  | Answer                                                                  |
| -------------------------------------------------- | ----------------------------------------------------------------------- |
| How tightly coupled is libgm to the bridge?        | Not at all. Zero mautrix imports in `pkg/libgm`.                        |
| Does libgm handle auth refresh?                    | Yes, automatically. Emits `AuthTokenRefreshed`; consumer just persists. |
| Any existing standalone projects?                  | openmessage (mature) and the in-tree gmtest (minimal). Both AGPL-safe.  |
| All transitive deps AGPL-compatible?               | Yes. No blockers.                                                       |

## 9. Phase 2 recommendations

1. **Module path:** `github.com/fdsouvenir/gmcli`. License: AGPL-3.0.
2. **Direct upstream:** `require go.mau.fi/mautrix-gmessages vX.Y.Z` — no
   `replace` directive. If we need paginated conversation listing, upstream a
   PR to mautrix/gmessages first; if that lands slowly, paginate
   client-side using `FetchMessages` cursors.
3. **CLI framework:** Cobra (BSD-3) — matches wacli's UX and handles
   `--json`, `--read-only`, `--store DIR` cleanly.
4. **SQLite driver:** `modernc.org/sqlite` (BSD-3, pure Go, FTS5 in default
   build). Avoids cgo and matches openmessage's choice.
5. **Skeleton-from-gmtest:** stand up `internal/gm` with the gmtest pattern;
   wrap it in `cmd/auth.go` and `cmd/sync.go`.
6. **Schema sketch:**
   ```sql
   conversations(conversation_id PK, name, is_group, participants_json,
                 last_message_ts, unread_count, source_platform, source_id);
   messages(message_id PK, conversation_id FK, sender_id, body,
            timestamp_ms, status, is_from_me, media_id, mime_type,
            decryption_key, reactions_json, reply_to_id,
            source_platform, source_id);
   contacts(contact_id PK, name, e164, avatar_blob);
   CREATE UNIQUE INDEX ux_messages_source ON messages(source_platform, source_id);
   CREATE VIRTUAL TABLE messages_fts USING fts5(message_id UNINDEXED, body,
                                                tokenize='trigram');
   ```
   Single-platform today, multi-platform-ready tomorrow.
7. **Read-only by default:** all `SendMessage`/`SendReaction` paths gated
   behind `--write` (or a confirmation prompt in OpenClaw skill mode).
8. **Doctor command:** check `IsLoggedIn`, `IsConnected`, AuthData expiry,
   sqlite integrity, FTS index health. Mirrors openmessage's `get_status`
   tool.
9. **OpenClaw skill (Phase 4):** Read-only wrapper script around `gmcli`,
   borrowing openmessage's prompt-injection preamble verbatim where the
   Unlicense lets us.

## 10. Next steps

This branch carries only the research doc. Phase 2 (scaffolding the Go
module, LICENSE, README, libgm wrapper, SQLite store, and the auth/sync
commands) should land on a separate feature branch once this is reviewed.

## 11. Send settings diagnostics and fallback

Follow-up live inspection on June 3, 2026 found that the recurring upstream
unknown event is not the `gmproto.Settings`/SIM metadata used by the richer
Google Messages send request shape. Reports should stay redacted: event type,
field numbers, byte lengths, counts, and timestamps are useful; payload blobs
are not.

gmcli now has `gmcli sync send-settings`, a read-only network diagnostic that
opens the paired session, registers the sync pump, calls `SetActiveSession`
through `RequestUpdates`, and waits for a real `*gmproto.Settings` event before
marking send readiness. A live run on June 3, 2026 successfully requested the
refresh but did not receive Settings within the 60 second diagnostic window.

Because earlier gmcli sends worked with a minimal request shape, missing
Settings should not remain a hard blocker. The send path should prefer real
`Settings.SIMCards` when available, never synthesize SIM metadata, and fall
back to the legacy request shape when Settings are unavailable. Success should
still require the phone to echo the outgoing message.

Further live testing on June 3, 2026 showed the original send failure was not
caused by missing cached Settings alone. The stale pairing still allowed
read-only RPCs, but `IsBugleDefault` returned false and `GetConversation`
returned sparse metadata with no `DefaultOutgoingID` and unknown conversation
type. After removing the phone-side web pairing, updating to the current
`mautrix-gmessages` protocol library, and re-pairing with a fresh QR token,
the same phone reported Google Messages as the default SMS app, returned RCS
conversation metadata with outgoing participant `2`, and accepted a
Settings/SIM-backed send request. This makes re-pairing a first-line
remediation when sends fail with live default-SMS false while cached Settings
still report default-SMS true.
