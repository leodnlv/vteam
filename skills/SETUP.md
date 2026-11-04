# Setting up Claude Code skills from this repo

Skills in `skills/` (e.g. `create-vteam-agent`) need to be available as **user-scoped** Claude Code skills so they work from any project.

## Install

From the root of this repo:

```bash
vteam skill install
```

This symlinks every skill directory under `skills/` into `~/.claude/skills/`. Claude Code follows symlinks, so changes you make here are immediately available everywhere.

## Verify

From any project directory, run Claude Code and type `/<skill-name>` (e.g. `/create-vteam-agent`). The skill should appear in the skill list.

## How it works

- `~/.claude/skills/` is where Claude Code loads user-scoped skills from
- Each skill is a directory containing a `SKILL.md` file
- The symlink makes Claude Code see `~/.claude/skills/<skill-name>/SKILL.md` which points to the real file in this repo
- Edits in this repo are reflected immediately — no copy step, no rebuild

## If you move this repo

The symlinks use absolute paths. If you relocate the repo, re-run:

```bash
vteam skill install
```

## Removing skills

```bash
vteam skill uninstall
```

This only removes the symlinks — the source files in this repo are untouched.
