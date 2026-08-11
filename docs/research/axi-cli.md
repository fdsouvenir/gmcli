# Agent-oriented CLI design

**Date:** 2026-08-11
**Status:** v0.4 compatibility improvements implemented; structured output
work proposed for v0.5.

This note maps the ten [Agent eXperience Interface (AXI)](https://github.com/kunchenguid/axi)
principles onto gmcli. AXI is design guidance, not a runtime dependency.

## Constraints specific to gmcli

- Message bodies, contact names, phone numbers, session cookies, and local
  archive paths are sensitive. No command should reveal live inbox content
  merely because the binary was invoked without a verb.
- Existing shell scripts and the bundled skill depend on the current JSON
  arrays and objects. A compact format must be additive rather than silently
  changing `--json`.
- Human-readable tables are useful at a terminal; agent-oriented output must
  not make them worse.
- Phone mutations stay read-only by default regardless of output format.

## v0.4 adaptations

The Gaia release adopts the compatible parts of AXI without changing JSON:

- authentication is fully flag-driven and non-interactive;
- unexpected positional arguments and unknown flags fail loudly;
- invalid usage exits 2 while operational failures exit 1;
- empty human results explicitly say `0` on stdout;
- removing an already-absent alias is an idempotent successful no-op;
- core subcommands include concise examples;
- `-v`, `-V`, and `--version` are bare fast paths;
- existing `--full` behavior remains the truncation escape hatch; and
- `doctor` precomputes archive health and now identifies Gaia versus legacy QR
  session state without exposing credentials.

Two AXI recommendations are intentionally not adopted:

- `gmcli` with no arguments continues to show help, not live conversations.
- no ambient hook injects message content into every agent session. The
  existing opt-in skill provides on-demand discovery without a privacy or
  per-session token cost.

## v0.5 structured-output proposal

Add `--format human|json|toon`, keeping `--json` as a compatibility alias for
the existing JSON contract. `--json` and a conflicting `--format` value must
fail as usage errors.

The new TOON path should:

1. use command-specific 3–4 field list schemas;
2. support `--fields` for explicit expansion;
3. wrap lists with `returned`, `total`, and `truncated` metadata;
4. include body previews plus original character counts;
5. emit definitive empty collections;
6. translate failures to structured `error`, `code`, and `help` fields; and
7. keep progress and debug logging on stderr.

TOON must be implemented from the published specification and tested with
round-trip fixtures. Do not hand-roll an approximate dialect.

## Benchmark gate

Before v0.5 ships, compare current JSON, compact JSON, and TOON on these seeded
archive tasks:

- list 50 recent conversations and select one by participant;
- search 100 message hits and pull context for five;
- inspect one 10,000-character message body with and without `--full`;
- diagnose an empty archive;
- diagnose an unpaired archive; and
- inspect send readiness without sending.

Record serialized bytes, tokenizer counts for at least two current model
tokenizers, command latency, and the number of follow-up calls needed to answer
the task. Ship TOON only if it materially lowers tokens or round trips without
losing identifiers, timestamps, safety state, or empty-result certainty.

## Compatibility tests

- Golden fixtures lock the existing `--json` shape.
- Every human, JSON, and TOON command returns the same records and ordering.
- Structured modes never contain progress text.
- `--full` affects large content but not unrelated fields.
- Secret fields (`decryption_key`, `raw_proto`, cookies, tokens) remain absent.
- No-argument invocation never opens the store or prints archive content.
