# Evidence-aware connection health

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

## Compatibility and operating limits

Migration from schema 3 is additive: it creates the full schema-4
`connection_health` table without changing archive rows or session contents.
Existing full schema-4 stores use the same columns and retain their observations.
Opening a store, including through offline `doctor`, applies pending migrations;
back up the database and session before a version change. Read-only means no
phone mutations, not a promise that SQLite files remain byte-for-byte unchanged.

Re-pairing conservatively invalidates old health before the existing QR flow.
Session persistence uses an unpredictable mode-0600 temporary file and atomic
rename; write or rename failure does not replace an existing session. No new
pairing command, dependency upgrade, or Google-account authentication flow is
included. Google-account pairing remains separate in [PR #4](https://github.com/fdsouvenir/gmcli/pull/4),
with its real-device release gate unchanged.

`sync send-settings` no longer labels cached SIM metadata send-ready when the
current refresh times out. Cache availability remains separate from a successful
response during this run. Send/reaction opt-in protections are unchanged.

The archive skill is independently versioned and is not republished here.
Older skill instructions that infer health from pairing, process locks, message
age, or `last_sync_activity_time` are outdated; use these evidence semantics.
No live-device validation or reconnection is claimed by this patch.

## CLI output changes

An offline empty-store report includes:

```text
  health:           unknown (offline evidence only)
  paired:           false
  legacy sync activity (not health): (none yet)
```

Both text and JSON doctor reports return a nonzero exit status when issues exist.
JSON remains on stdout; callers must handle the nonzero status rather than discard
the report. Sync startup now says "Connection requested" rather than "Connected";
the initial pass finishing is explicitly not a connectivity verdict.

## Offline validation

```sh
go test ./...
go vet ./...
```

Regression tests use temporary databases and synthetic protocol events, not a
saved user session or a live phone. They cover migration integrity, existing
schema-4 evidence, empty/populated snapshot distinctions, corrected snapshots,
recoverable versus terminal failures, invalid events, mode detection, JSON error
status, atomic session persistence, and cached-settings refresh timeouts.
