---
name: vteam
description: Context injection for the vteam framework — a virtual development team that orchestrates AI agents via Claude Code to autonomously review codebases and implement fixes. This skill provides no interactive flow; it loads background knowledge so you can assist with vteam configuration, agent authoring, troubleshooting, and usage.
---

You now have full context on the **vteam** framework. Use this knowledge to assist the user with any vteam-related task — writing agents, configuring runs, troubleshooting, extending the framework, or understanding how it works. Do NOT ask the user questions from this skill; it is purely contextual.

---

## What vteam is

vteam is an npm package that orchestrates AI agents powered by `claude -p` (Claude Code's headless mode). The user defines agent prompts, triages findings, and reviews pull requests. vteam handles everything else: scheduling agent runs, managing task lifecycle, isolating work in git worktrees, and creating branches and pull requests.

**Core separation of concerns:** the TypeScript orchestrator owns all state (files, git, aven tasks, PRs). Claude owns all intelligence (reading code, finding issues, implementing fixes). If Claude crashes mid-run, no state is corrupted — the orchestrator applies state transitions only after Claude finishes and returns structured output.

## Prerequisites

- **Node.js** >= 20
- **Claude Code** CLI — `claude` must be on PATH
- **Git** — the project must be a git repository
- **aven** — local-first SQLite task manager CLI; `aven` must be installed and on PATH. All vteam tasks live in aven.
- **`gh`** (GitHub CLI) or **`glab`** (GitLab CLI) — for automatic PR creation. Without it, branches are pushed but PRs must be created manually.

## CLI commands

```
vteam init                              # scaffold vteam/ directory in the current project
vteam run <agent>                       # run a specific agent
vteam run <agent> --focus "..."         # run with priority context injected into the prompt
vteam run                               # (no agent) list available agents
vteam status                            # show aven project/label, counts per aven status, active/todo tasks with refs + retries, worktrees
vteam clean                             # prune orphaned worktrees, break stale locks
vteam loop start                        # start long-lived scheduler for agents with cron patterns
vteam loop status                       # show agents with cron schedules and next fire times
vteam skill install                     # symlink skills from skills/ into ~/.claude/skills/
vteam skill uninstall                   # remove skill symlinks from ~/.claude/skills/
```

## Project structure after `vteam init`

```
vteam/
├── vteam.config.json               # Project-level configuration
├── agents/
│   ├── improver/
│   │   ├── AGENT.md                # Agent prompt (frontmatter + system prompt)
│   │   ├── MEMORY.md               # Optional: memory curation strategy
│   │   └── ON_FINISH.md            # Optional: post-run hook
│   ├── coder/
│   │   ├── AGENT.md
│   │   ├── MEMORY.md
│   │   └── ON_FINISH.md
│   ├── review-responder/
│   │   ├── AGENT.md
│   │   ├── MEMORY.md
│   │   └── ON_FINISH.md
│   └── test-writer/
│       ├── AGENT.md
│       ├── MEMORY.md
│       └── ON_FINISH.md
```

`vteam init` sets `tasks.project` to the current directory name and gitignores `.vteam-worktrees/` plus `vteam/.locks/`, `vteam/.logs/`, `vteam/.runs/`, `vteam/.memory/`. Tasks live in aven, not in the repo — the real shared artifacts are the PRs.

## Configuration

### vteam.config.json

```json
{
  "baseBranch": "main",
  "platform": "github",
  "worktreeDir": ".vteam-worktrees",
  "tasks": {
    "maxRetries": 3,
    "project": "my-app",
    "label": "vteam",
    "defaultActor": "coder",
    "workspace": "optional"
  }
}
```

| Field              | Description                                                                             |
| ------------------ | --------------------------------------------------------------------------------------- |
| `baseBranch`       | Branch to create worktrees from and target PRs against                                  |
| `platform`         | `"github"` or `"gitlab"` — determines which CLI (`gh` / `glab`) is used for PR creation |
| `worktreeDir`      | Where worktrees are created (relative to repo root). Gitignored.                        |
| `tasks.maxRetries` | How many times the coder retries a failing task before skipping it                      |
| `tasks.project`    | Required, non-empty. aven project for vteam's tasks; aven auto-creates it on the first task |
| `tasks.label`      | Default `"vteam"`. Opt-in label — vteam only sees/picks up tasks carrying it. Auto-created |
| `tasks.defaultActor` | Required. Agent that works on tasks with no `actor` metadata (incl. human-created tasks) |
| `tasks.workspace`  | Optional. aven workspace, passed as `--workspace` to every `aven` call                  |

## Agent configuration (AGENT.md)

Agents are defined at `vteam/agents/<name>/AGENT.md`. The YAML frontmatter declares orchestrator behavior. The markdown body becomes the agent's system prompt. Add custom agents by creating a new directory with an `AGENT.md` — no config changes needed.

### Frontmatter fields

```yaml
---
model: sonnet # Optional: alias ("sonnet", "opus", "haiku") or full model ID; omit for Claude Code's default
cron: "0 */6 * * *" # Optional: cron schedule (5 fields)
worktree: true # Run in an isolated git worktree
readOnly: true # Worktree but no commit/push/PR (requires worktree: true)
input: task # "task" (pick from the aven todo queue) or "pr" (respond to PR reviews)
output: task # "task" to produce findings as aven tasks
assignTo: coder # requires output: task — sets aven metadata actor; omit to route to tasks.defaultActor
autoPR: true # Create a PR after pushing
prTriggerLabel: "vteam:changes-requested" # Label to discover PRs needing work
prCreateLabels: [vteam] # Labels applied to created PRs
allowedTools: [...] # Claude Code tools the agent may use
disallowedTools: [...] # Claude Code tools the agent may NOT use
---
```

### Validation rules (enforced by the orchestrator)

- `input: "pr"` requires `worktree: true`
- `readOnly: true` requires `worktree: true`
- `readOnly: true` is incompatible with `autoPR: true`
- `output: "task"` is incompatible with `autoPR: true`
- `output: "task"` is incompatible with `readOnly: true`

### Common agent archetypes

| Archetype                | worktree | input  | output | autoPR | Description                                                   |
| ------------------------ | -------- | ------ | ------ | ------ | ------------------------------------------------------------- |
| Scanner / improver       | —        | —      | `task` | —      | Scans codebase, returns findings as tasks                     |
| Implementer / coder      | `true`   | `task` | —      | `true` | Picks a task, fixes it, creates a PR                          |
| PR responder             | `true`   | `pr`   | —      | —      | Responds to review comments on PRs                            |
| Read-only analyzer       | `true`   | —      | —      | —      | Reads code in a worktree, returns text (set `readOnly: true`) |
| Generic reporter         | —        | —      | —      | —      | Returns freeform text output                                  |

## How agents run

Each agent is invoked as a `claude -p` subprocess. The orchestrator assembles a layered prompt:

1. **System prompt** — the AGENT.md markdown body
2. **Priority focus** — optional `--focus` text, injected first so Claude prioritizes it
3. **Agent memory** — accumulated memory from previous runs (if MEMORY.md exists)
4. **Existing tasks** — every non-deleted labelled task in the aven project, all statuses, as `- [status] severity | title | files` (for deduplication)
5. **Task content** — if `input: "task"`, the specific task to implement
6. **PR review comments** — if `input: "pr"`, the unresolved review threads
7. **Output format instructions** — JSON schema the agent must return

Claude returns structured JSON matching the unified `AgentOutput` schema:

```json
{
  "status": "completed|partial|blocked|failed",
  "summary": "what you did",
  "content": { "type": "task|generic", "body": "..." },
  "filesChanged": ["path/to/file.ts"],
  "commitMessage": { "subject": "vteam: ...", "body": "..." },
  "blockerReason": "only if blocked/failed",
  "memoryUpdate": "optional notes for future runs"
}
```

The orchestrator then:

- Creates an aven task from `content` when `type: "task"`
- Commits + pushes from `commitMessage` when the agent modified files
- Creates a PR if `autoPR: true`
- Sets the task to `done` (metadata `completed`, `branch`, `pr-url`) if a commit was pushed
- Returns the task to `todo` and increments `retry-count` metadata if the agent produced no changes
- Returns the task to `todo` without incrementing on any other ending (crash, error, readOnly or non-worktree agent)
- Passes `content` through to the ON_FINISH hook

## Task lifecycle

```
improver finds issue → aven task in todo
coder picks task → active → implements fix → branch + PR → done
no changes / crash / error → active → back to todo
```

Tasks live in aven, scoped to `tasks.project` + `tasks.label`. Refs look like `APP-7KQ9` (recorded as `taskRef` in `vteam/.runs/*.json`).

- **Creating** (`output: "task"`): `aven add` with `--project`, `--status todo`, `--label`, priority from severity (critical→urgent, high→high, medium→medium, low→low), metadata `found-by=<agent>`, `actor=<assignTo>` (only if `assignTo` is set), and `files=<comma-separated>`, and a markdown description (Description / Suggested Fix / Affected Files). Aven priority `none` reads back as `low`.
- **Routing**: metadata `actor` names the agent that should work on a task. An `input: "task"` agent picks tasks with `actor` equal to its own name; the `tasks.defaultActor` agent also picks tasks with no `actor`. Reassign with `aven edit <ref> --metadata actor=<agent>`; `--remove-metadata actor` sends it back to the default actor. `found-by` is provenance only.
- **Picking** (`input: "task"`): `aven list --project P --label L --status todo --ready` filtered by `actor` as above, so aven dependency blocking and `available_at` deferral are respected. Tasks with `retry-count` >= `maxRetries` are skipped. Sorted by severity (`critical` > `high` > `medium` > `low`), then oldest first. The picked task is set to `active` immediately.
- **Branch names**: `vteam/<slugified-title>-<runId>` — aven refs are local and are kept out of remote systems.
- **Human triage**: users can manage tasks in aven (`aven tui` or CLI) — move findings to backlog/canceled, reprioritize, add dependencies, or add the `vteam` label to their own tasks to queue them for the coder. Canceled tasks still appear in the dedup list, so canceling marks a finding as rejected/false positive.
- **Hard kill**: if the vteam process is killed, the task may remain `active`; reset it with `aven edit <ref> --status todo`.

## Optional agent files

### MEMORY.md — persistent memory across runs

Place alongside AGENT.md. Defines how memory is curated between runs. The orchestrator:

1. Injects accumulated memory into the agent's prompt before each run
2. Extracts `memoryUpdate` from the agent's output
3. Spawns a curation agent that merges new updates into existing memory per the MEMORY.md rules
4. Writes the curated result to `vteam/.memory/<agent-name>/store.md`

```yaml
---
model: haiku
---
Keep a running list of areas scanned. Record false positives.
Maximum 30 lines — drop oldest entries first.
```

### ON_FINISH.md — post-run hooks

Place alongside AGENT.md. After the agent finishes (success or failure), the orchestrator spawns a second `claude -p` call with the run outcome. Use for notifications, logging, or follow-up actions.

```yaml
---
model: haiku
allowedTools: ["Bash(curl *)", "mcp__slack__send_message"]
---
Post the run result to the #eng-prs Slack channel.
```

The hook receives: agent name, status, timestamps, task details, branch, PR URL, content output, and error (if any). It runs in the main project directory. Hook failure does not affect the agent run's exit status.

## Worktrees

Agents with `worktree: true` get an isolated git worktree. Claude edits files there but does NOT commit — it returns a commit message in its JSON output. The orchestrator runs `git add -A` + `git commit`, pushes the branch, and optionally creates a PR. Worktrees are cleaned up after each run. Force-push is used on `vteam/*` branches (they are ephemeral and tool-owned).

## Locking

Advisory file locking via atomic `mkdir`. Each lock contains PID + timestamp. Stale locks (dead process or >30 minutes) are auto-detected. Same-agent runs are serialized; different agents can run concurrently. Run `vteam clean` to break stuck locks.

## Scheduling

Agents with a `cron` field run on schedule via `vteam loop start` (foreground process, stop with Ctrl+C). If an agent is still running when its next tick fires, the tick is skipped.

## The `--focus` option

All `vteam run` invocations accept `--focus <text>` to inject priority context. The text appears as the first section of the user prompt.

```bash
vteam run improver --focus "only look at the auth module"
vteam run coder --focus "the issue about XYZ"
```

## Default agents

| Agent                | Purpose                           | Key traits                                      |
| -------------------- | --------------------------------- | ----------------------------------------------- |
| **improver**         | Scans codebase for issues         | Read-only, `output: task`, no worktree          |
| **coder**            | Picks a task and implements a fix | `input: task`, `worktree: true`, `autoPR: true` |
| **review-responder** | Addresses PR review comments      | `input: pr`, `worktree: true`, `prTriggerLabel` |
| **test-writer**      | Adds tests for untested code      | `worktree: true`, `autoPR: true`                |

## Creating a custom agent

1. Create `vteam/agents/<name>/AGENT.md` with YAML frontmatter + system prompt
2. Optionally add `MEMORY.md` and/or `ON_FINISH.md` alongside it
3. Run with `vteam run <name>`

No config changes needed — the orchestrator discovers agents by scanning the `vteam/agents/` directory.

## Tool permission patterns

Common `allowedTools` presets:

- **Read-only**: `["Read", "Glob", "Grep"]`
- **Read-write**: `["Read", "Edit", "Write", "Glob", "Grep", "Bash(npm *)", "Bash(npx *)", "Bash(cat *)", "Bash(ls *)"]`
- **Read-write + git platform**: above plus `"Bash(gh *)"` and/or `"Bash(glab *)"`

## Key design principles

1. **Orchestrator owns state, Claude owns intelligence** — strict separation. Claude never creates tasks, commits, or pushes. The orchestrator never reasons about code.
2. **Stateless agents** — every `claude -p` call starts fresh. All context is injected via the prompt (task content, memory, existing task titles).
3. **Atomic state transitions** — if Claude crashes, no state is corrupted. The orchestrator applies changes only after Claude finishes.
4. **Local-only workflow state** — tasks live in local aven; locks, logs, run state, and memory are gitignored. The shared artifacts are PRs and branches.
5. **Title-based deduplication** — the orchestrator injects all existing task titles into every agent's prompt so Claude avoids reporting duplicates.
6. **Minimal dependencies** — YAML parsing, slug generation, and utilities are internal modules with no external dependencies.

## Troubleshooting

- **Stuck lock**: Run `vteam clean` or wait 30 minutes for stale detection.
- **Orphaned worktree**: Run `vteam clean` to remove it.
- **Agent produced no changes**: Check the task's `retry-count` metadata (`aven show <ref>`). After `maxRetries` failures, the task is skipped.
- **PR creation failed**: Verify `gh` or `glab` is installed and authenticated. The branch is still pushed — create the PR manually.
- **No eligible tasks**: Run the improver first to create todo tasks, or add the `vteam` label to existing aven tasks in the project. Tasks blocked by dependencies or deferred are not ready.
- **Task stuck in `active`**: The vteam process was killed mid-run. Reset with `aven edit <ref> --status todo`.
- **`aven CLI not found on PATH`**: Install aven and make sure `aven` is on PATH.
