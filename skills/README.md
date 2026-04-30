# gmcli skills

LLM skills that wrap gmcli for assistants like Claude Code, OpenClaw, or any
SKILL.md-aware harness. Each skill is a directory containing a `SKILL.md`
whose YAML frontmatter declares the trigger language; the markdown body is
the playbook the assistant follows when the skill fires.

## google-messages

Read-only skill for answering questions about the user's text messages.
Triggers on phrasings like "check my texts", "what did <person> text me",
"search my messages for <topic>". Wraps `gmcli` with `--read-only --json`
on every call, includes a verb decision tree, and carries a strong prompt
injection preamble so untrusted message bodies cannot redirect the
assistant.

See [`google-messages/SKILL.md`](google-messages/SKILL.md) for the full
playbook.

## Installing

The exact install path depends on your harness:

- **Claude Code** — copy or symlink `skills/google-messages` into your
  user-level skills directory (typically `~/.claude/skills/`):

      mkdir -p ~/.claude/skills
      ln -s "$(pwd)/skills/google-messages" ~/.claude/skills/google-messages

- **OpenClaw** — drop the directory into your OpenClaw skills root and
  reload the agent. The frontmatter `name` (`google-messages`) is what
  shows up in the skill list.

In all cases, the assistant must be able to run `gmcli` from its `Bash`
tool. Verify with:

    which gmcli
    gmcli doctor

If the assistant runs in a sandbox, ensure `gmcli` is on the sandbox's
`PATH` and that the sandbox can read `$XDG_STATE_HOME/gmcli` (or the
directory passed via `--store`).

## Authoring more skills

To add another skill, create a sibling directory with its own `SKILL.md`.
Keep the frontmatter description concrete (the assistant matches user
input against it) and the body a short, deterministic playbook — long-form
exploration prompts produce drift.
