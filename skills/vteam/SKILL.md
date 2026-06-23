---
name: vteam
description: Context injection for the vteam framework — a virtual development team that orchestrates AI agents via Claude Code to autonomously review codebases and implement fixes. This skill provides no interactive flow; it loads background knowledge so you can assist with vteam configuration, agent authoring, troubleshooting, and usage.
---

You now have full context on the **vteam** framework. Use this knowledge to assist the user with any vteam-related task — writing agents, configuring runs, troubleshooting, extending the framework, or understanding how it works. Do NOT ask the user questions from this skill; it is purely contextual.

---

## What vteam is

vteam is an npm package that orchestrates AI agents powered by `claude -p` (Claude Code's headless mode). The user defines agent prompts, triages findings, and reviews pull requests. vteam handles everything else: scheduling agent runs, managing task lifecycle, isolating work in git worktrees, and creating branches and pull requests.

**Core separation of concerns:** the TypeScript orchestrator owns all state (files, git, tasks, PRs). Claude owns all intelligence (reading code, finding issues, implementing fixes). If Claude crashes mid-run, no state is corrupted — the orchestrator applies state transitions only after Claude finishes and returns structured output.

## Prerequisites

- **Node.js** >= 20
- **Claude Code** CLI — `claude` must be on PATH
- **Git** — the project must be a git repository
- **`gh`** (GitHub CLI) or **`glab`** (GitLab CLI) — for automatic PR creation. Without it, branches are pushed but PRs must be created manually.

## CLI commands

```
vteam init                              # scaffold vteam/ directory in the current project
vteam run <agent>                       # run a specific agent
vteam run <agent> --focus "..."         # run with priority context injected into the prompt
vteam run                               # (no agent) list available agents
vteam status                            # show task board overview (todo/done counts, retries, worktrees)
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
│   ├── code-reviewer/
│   │   ├── AGENT.md                # Agent prompt (frontmatter + system prompt)
│   │   ├── MEMORY.md               # Optional: memory curation strategy
│   │   └── ON_FINISH.md            # Optional: post-run hook
│   ├── refactorer/
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
└── tasks/                          # Local only — gitignored
    ├── todo/                       # Findings waiting for implementation
    └── done/                       # Completed tasks
```

`vteam/tasks/` and `.vteam-worktrees/` are gitignored. Task files are local workflow state — the real shared artifacts are the PRs.

## Configuration

### vteam.config.json

```json
{
  "baseBranch": "main",
  "platform": "github",
  "worktreeDir": ".vteam-worktrees",
  "tasks": {
    "maxRetries": 3
  },
  "taskManager": {
    "provider": "filesystem"
  }
}
```

| Field                  | Description                                                                             |
| ---------------------- | --------------------------------------------------------------------------------------- |
| `baseBranch`           | Branch to create worktrees from and target PRs against                                  |
| `platform`             | `"github"` or `"gitlab"` — determines which CLI (`gh` / `glab`) is used for PR creation |
| `worktreeDir`          | Where worktrees are created (relative to repo root). Gitignored.                        |
| `tasks.maxRetries`     | How many times the refactorer retries a failing task before skipping it                 |
| `taskManager.provider` | Task storage backend. Currently only `"filesystem"`.                                    |

## Agent configuration (AGENT.md)

Agents are defined at `vteam/agents/<name>/AGENT.md`. The YAML frontmatter declares orchestrator behavior. The markdown body becomes the agent's system prompt. Add custom agents by creating a new directory with an `AGENT.md` — no config changes needed.

### Frontmatter fields

```yaml
---
model: sonnet # Claude model: "sonnet", "opus", "haiku"
cron: "0 */6 * * *" # Optional: cron schedule (5 fields)
worktree: true # Run in an isolated git worktree
readOnly: true # Worktree but no commit/push/PR (requires worktree: true)
input: task # "task" (pick from todo/) or "pr" (respond to PR reviews)
output: task # "task" to produce findings as task files
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
| Scanner / code reviewer  | —        | —      | `task` | —      | Scans codebase, returns findings as tasks                     |
| Implementer / refactorer | `true`   | `task` | —      | `true` | Picks a task, fixes it, creates a PR                          |
| PR responder             | `true`   | `pr`   | —      | —      | Responds to review comments on PRs                            |
| Read-only analyzer       | `true`   | —      | —      | —      | Reads code in a worktree, returns text (set `readOnly: true`) |
| Generic reporter         | —        | —      | —      | —      | Returns freeform text output                                  |

## How agents run

Each agent is invoked as a `claude -p` subprocess. The orchestrator assembles a layered prompt:

1. **System prompt** — the AGENT.md markdown body
2. **Priority focus** — optional `--focus` text, injected first so Claude prioritizes it
3. **Agent memory** — accumulated memory from previous runs (if MEMORY.md exists)
4. **Existing tasks** — titles/severities/statuses of all tasks in todo/ and done/ (for deduplication)
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

- Creates task files from `content` when `type: "task"`
- Commits + pushes from `commitMessage` when the agent modified files
- Creates a PR if `autoPR: true`
- Moves the task from todo/ to done/ if successful
- Increments `retry-count` if the agent produced no changes
- Passes `content` through to the ON_FINISH hook

## Task lifecycle

```
code-reviewer finds issue → task file in todo/
refactorer picks task → implements fix → branch + PR → task moves to done/
```

Task files are markdown with YAML frontmatter. Named `YYYY-MM-DD-HH-mm-ss-<slugified-title>.md`. Severity levels: `critical` > `high` > `medium` > `low`. The refactorer picks the highest-severity task first. Tasks that fail 3+ times (configurable) are skipped.

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
vteam run code-reviewer --focus "only look at the auth module"
vteam run refactorer --focus "the issue about XYZ"
```

## Default agents

| Agent                | Purpose                           | Key traits                                      |
| -------------------- | --------------------------------- | ----------------------------------------------- |
| **code-reviewer**    | Scans codebase for issues         | Read-only, `output: task`, no worktree          |
| **refactorer**       | Picks a task and implements a fix | `input: task`, `worktree: true`, `autoPR: true` |
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

1. **Orchestrator owns state, Claude owns intelligence** — strict separation. Claude never creates task files, commits, or pushes. The orchestrator never reasons about code.
2. **Stateless agents** — every `claude -p` call starts fresh. All context is injected via the prompt (task files, memory, existing task titles).
3. **Atomic state transitions** — if Claude crashes, no state is corrupted. The orchestrator applies changes only after Claude finishes.
4. **Local-only workflow state** — task files are gitignored. The shared artifacts are PRs and branches.
5. **Title-based deduplication** — the orchestrator injects all existing task titles into every agent's prompt so Claude avoids reporting duplicates.
6. **Minimal dependencies** — YAML parsing, slug generation, and utilities are internal modules with no external dependencies.

## Troubleshooting

- **Stuck lock**: Run `vteam clean` or wait 30 minutes for stale detection.
- **Orphaned worktree**: Run `vteam clean` to remove it.
- **Agent produced no changes**: Check retry count in the task frontmatter. After `maxRetries` failures, the task is skipped.
- **PR creation failed**: Verify `gh` or `glab` is installed and authenticated. The branch is still pushed — create the PR manually.
- **No eligible tasks**: Run the code-reviewer first to populate `todo/`.
