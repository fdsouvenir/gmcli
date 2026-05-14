# gmcli skills

LLM skills that wrap gmcli for assistants like Claude Code, OpenClaw, or any
SKILL.md-aware harness. Each skill is a directory containing a `SKILL.md`
whose YAML frontmatter declares the trigger language; optional `agents/`
metadata provides UI labels for harnesses that support it.

## Google Messages Local Archive

Read-only skill for answering questions about the user's text messages from a
local `gmcli` archive. Triggers on phrasings like "check my texts", "what did
{person} text me", and "search my messages for {topic}". Wraps `gmcli` with
`--read-only --json` on every call, includes a verb decision tree, and carries
a strong prompt injection preamble so untrusted message bodies cannot redirect
the assistant.

See [`google-messages/SKILL.md`](google-messages/SKILL.md) for the full
playbook.

The repository keeps the folder as `skills/google-messages` for compatibility
with existing symlinks, local installs, and documentation links. The canonical
ClawHub/frontmatter slug is `google-messages-local-archive`.

## Installing

The exact install path depends on your harness:

- **Claude Code** — copy or symlink `skills/google-messages` into your
  user-level skills directory (typically `~/.claude/skills/`):

      mkdir -p ~/.claude/skills
      ln -s "$(pwd)/skills/google-messages" ~/.claude/skills/google-messages

- **OpenClaw** - drop the directory into your OpenClaw skills root and
  reload the agent. The frontmatter `name`
  (`google-messages-local-archive`) identifies the skill; `agents/openai.yaml`
  provides the human-facing label where supported.

In all cases, the assistant must be able to run `gmcli` from its `Bash`
tool. Verify with:

    which gmcli
    gmcli doctor

If the assistant runs in a sandbox, ensure `gmcli` is on the sandbox's
`PATH` and that the sandbox can read `$XDG_STATE_HOME/gmcli` (or the
directory passed via `--store`).

## ClawHub publishing

The gmcli repository is the canonical source for the ClawHub listing. Publish
directly from `skills/google-messages`; do not publish a staged copy.

From the repository root, after the matching gmcli release tag exists. Use the
absolute path because ClawHub resolves relative publish paths through its
configured skills directory:

```sh
clawhub --no-input publish "$(pwd)/skills/google-messages" \
  --slug google-messages-local-archive \
  --name "Google Messages Local Archive" \
  --owner fdsouvenir \
  --version 0.2.1 \
  --changelog "Publish the canonical gmcli repository skill and graduate from alpha." \
  --tags latest,gmcli,google-messages,local,archive,sms,rcs,read-only
```

Verify the registry metadata and files:

```sh
clawhub inspect google-messages-local-archive --version 0.2.1 --files
```

Verify install in a temporary workspace:

```sh
tmpdir="$(mktemp -d)"
clawhub --workdir "$tmpdir" install google-messages-local-archive --version 0.2.1
test -f "$tmpdir/skills/google-messages-local-archive/SKILL.md"
rm -rf "$tmpdir"
```

## Authoring more skills

To add another skill, create a sibling directory with its own `SKILL.md`.
Keep the frontmatter description concrete (the assistant matches user
input against it) and the body a short, deterministic playbook — long-form
exploration prompts produce drift.
