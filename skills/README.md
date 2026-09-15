# gmcli skills

LLM skills that wrap gmcli for assistants like Claude Code, OpenClaw, or any
SKILL.md-aware harness. Each skill is a directory containing a `SKILL.md`
whose YAML frontmatter declares the trigger language; optional `agents/`
metadata provides UI labels for harnesses that support it.

## Google Messages Local Archive

Archive skill for searching, summarizing, and answering questions about the
user's text messages from a local `gmcli` archive. Triggers on phrasings like
"check my texts", "summarize my recent texts", "what did {person} text me",
and "search my messages for {topic}". Its default archive queries wrap `gmcli`
with `--read-only --json`, include a verb decision tree, and carry a strong
prompt injection preamble so untrusted message bodies cannot redirect the
assistant.

See [`google-messages/SKILL.md`](google-messages/SKILL.md) for the full
playbook.

The repository keeps the folder as `skills/google-messages` for compatibility
with existing symlinks, local installs, and documentation links. The canonical
ClawHub/frontmatter slug is `google-messages-local-archive`.

## Installing

Requires OpenClaw **2026.8.1 (OpenClaw 2.0) or newer** for OpenClaw use,
and Go 1.25 or newer to install gmcli. The bundled runtime preflight checks
OpenClaw and gmcli versions before archive queries.

The exact install path depends on your harness:

- **Claude Code** — copy or symlink `skills/google-messages` into your
  user-level skills directory (typically `~/.claude/skills/`):

      mkdir -p ~/.claude/skills
      ln -s "$(pwd)/skills/google-messages" ~/.claude/skills/google-messages

- **OpenClaw** - drop the directory into your OpenClaw skills root and
  reload the agent. The frontmatter `name`
  (`google-messages-local-archive`) identifies the skill; `agents/openai.yaml`
  provides the human-facing label where supported. Install the published skill
  with `openclaw skills install @fdsouvenir/google-messages-local-archive`, then
  inspect availability with `openclaw skills check`.

In all cases, the assistant must be able to run `gmcli` from its `Bash`
tool. Verify with:

    which gmcli
    gmcli doctor

If the assistant runs in a sandbox, ensure `gmcli` is on the sandbox's
`PATH` and that the sandbox can read `$XDG_STATE_HOME/gmcli` (or the
directory passed via `--store`).

## OpenClaw 2.0 compatibility

The skill uses OpenClaw's supported YAML `metadata.openclaw` dependency and Go
installer fields. Version and descriptive compatibility information live under
`metadata`; the registry version is supplied separately with `--version`.
OpenClaw 2026.8.1's skill validator rejects a top-level `version` key and does
not implement a skill minimum-runtime-version field. The bundled
`scripts/check-runtime.sh` enforces the minimum runtime before queries, rather
than relying on an ignored field. It reads version output only.

Validate the bundle with the `skills/skill-creator/scripts/quick_validate.py`
from the official OpenClaw v2026.8.1 source, plus `go test ./skills` for the
runtime preflight and repository metadata tests. The canonical frontmatter
name is also the ClawHub installation directory name; the repository keeps its
historical folder for existing symlinks.

Sources: [OpenClaw skills](https://docs.openclaw.ai/tools/skills),
[OpenClaw 2.0 skills changes](https://docs.openclaw.ai/releases/2026.8.1/skills),
[ClawHub skill format](https://docs.openclaw.ai/clawhub/skill-format).

## Maintainer ClawHub Publishing

The gmcli repository is the canonical source for the ClawHub listing. Publish
directly from `skills/google-messages`; do not publish a staged copy.

From the repository root, after the `gmcli` release tag referenced by the
installer metadata exists. Use the absolute path because ClawHub resolves
relative publish paths through its configured skills directory:

```sh
clawhub skill publish "$(pwd)/skills/google-messages" \
  --slug google-messages-local-archive \
  --name "Google Messages Local Archive" \
  --owner fdsouvenir \
  --version 1.0.0 \
  --changelog "Replace retired QR pairing with Google Account emoji authentication; require OpenClaw 2026.8.1 or newer; validate runtime compatibility and use evidence-based archive health guidance." \
  --tags latest,gmcli,google-messages,local,archive,sms,rcs,search,summarize,privacy
```

Verify the registry metadata and files:

```sh
clawhub inspect google-messages-local-archive --version 1.0.0 --files
```

Verify install in a temporary workspace:

```sh
tmpdir="$(mktemp -d)"
clawhub --workdir "$tmpdir" install google-messages-local-archive --version 1.0.0
test -f "$tmpdir/skills/google-messages-local-archive/SKILL.md"
rm -rf "$tmpdir"
```

## Authoring more skills

To add another skill, create a sibling directory with its own `SKILL.md`.
Keep the frontmatter description concrete (the assistant matches user
input against it) and the body a short, deterministic playbook — long-form
exploration prompts produce drift.
