# vteam

A virtual development team framework that orchestrates AI agents powered by [Claude Code](https://docs.anthropic.com/en/docs/claude-code) to autonomously review codebases and implement fixes.

You define agent prompts, triage findings, and review pull requests. vteam handles everything else: scheduling agent runs, managing task lifecycle, isolating work in git worktrees, and creating branches and pull requests.

## How it works

vteam runs Claude in headless mode (`claude -p`) as a subprocess. Each agent invocation is stateless — all memory is external (tasks in [aven](#task-lifecycle), per-agent memory files) and injected into the prompt.

```
┌─────────────────────────────────────────────────────────────┐
│                      vteam (TypeScript)                     │
│                                                             │
│  Owns all state: tasks, worktrees, PRs                      │
│                                                             │
│  ┌───────────────────┐ ┌────────────┐ ┌──────────────────┐  │
│  │ Agent definitions │ │ Task mgr   │ │ Worktree mgr     │  │
│  └───────────────────┘ └────────────┘ └──────────────────┘  │
│  ┌───────────────────┐ ┌─────────────────────────────────┐  │
│  │ Lock mgr (mkdir)  │ │ PR integration (gh / glab CLI)  │  │
│  └───────────────────┘ └─────────────────────────────────┘  │
└────────────────────────────┬────────────────────────────────┘
                             │ spawns claude -p
                             ▼
┌─────────────────────────────────────────────────────────────┐
│                    Claude (headless)                        │
│                                                             │
│  Owns all intelligence: reads code, finds issues,           │
│  implements fixes, returns structured JSON                  │
└─────────────────────────────────────────────────────────────┘
```

The orchestrator never reasons about code. Claude never creates tasks, commits, or pushes branches. This separation means if Claude crashes mid-run, no state is corrupted — the orchestrator applies state transitions only after Claude finishes and returns structured output.

## Prerequisites

- **Node.js** >= 20
- **Claude Code** CLI — `claude` must be on your PATH ([install guide](https://docs.anthropic.com/en/docs/claude-code/getting-started))
- **Git** — the project must be a git repository
- **aven** — the local-first task manager CLI. `aven` must be installed and on your PATH; vteam stores all tasks in it.
- **`gh`** (GitHub CLI) or **`glab`** (GitLab CLI) — required for automatic pull request creation. Without it, branches are pushed but PRs must be created manually.

## Installation

vteam is not yet published to npm. Run it from source:

```bash
git clone git@github.com:Infonautica/vteam.git
cd vteam
npm install
npm run build
npm link
```

## Quick start

```bash
cd your-project

# 1. Scaffold the vteam directory
vteam init

# 2. Edit the config
$EDITOR vteam/vteam.config.json

# 3. Run the improver — finds issues, creates them as aven tasks in todo
vteam run improver

# 4. Run the coder — picks a task, fixes it, pushes a branch, opens a PR
vteam run coder

# 5. Review the PR as you would any other
```

### Running on a schedule

Agents with a `cron` field in their AGENT.md frontmatter can be scheduled via the loop command:

```bash
# Start the scheduler — runs agents on their cron schedules
vteam loop start

# Check which agents are scheduled and when they fire next
vteam loop status
```

The scheduler is a foreground process — stop it with Ctrl+C. If an agent is still running when its next cron tick fires, the tick is skipped.

## Commands

### `vteam init`

Scaffolds a `vteam/` directory in the current project:

```
vteam/
├── vteam.config.json               # Project-level configuration
├── agents/
│   ├── improver/
│   │   ├── AGENT.md                # Improver prompt/personality
│   │   └── MEMORY.md               # Memory curation strategy
│   ├── coder/
│   │   ├── AGENT.md                # Coder prompt/personality
│   │   └── MEMORY.md               # Memory curation strategy
│   ├── review-responder/
│   │   ├── AGENT.md                # Review responder prompt/personality
│   │   └── MEMORY.md               # Memory curation strategy
│   └── test-writer/
│       ├── AGENT.md                # Test writer prompt/personality
│       └── MEMORY.md               # Memory curation strategy
```

Sets `tasks.project` in the config to the current directory name. Also adds `.vteam-worktrees/` and vteam's local state directories (`vteam/.locks/`, `vteam/.logs/`, `vteam/.runs/`, `vteam/.memory/`) to `.gitignore`. Tasks live in aven, not in the repo — the real shared artifacts are the PRs that the coder creates.

Will refuse to run if `vteam/` already exists.

### `--focus` option

All `vteam run` invocations accept `--focus <text>` to inject priority context into the agent's prompt. The text is added as the first section of the user prompt so Claude prioritizes it above all other considerations.

```bash
vteam run coder --focus "the issue about XYZ"
vteam run test-writer --focus "invite user functionality"
vteam run improver --focus "only look at the auth module"
```

### `vteam run improver`

1. Acquires an advisory lock (`vteam/.locks/improver.lock`)
2. Reads the agent prompt from `vteam/agents/improver/AGENT.md`
3. Lists all vteam-labelled tasks in the aven project (every status) and injects them into the prompt so Claude knows what's already been found
4. Spawns `claude -p` — Claude scans the codebase and returns findings as structured JSON
5. The orchestrator creates an aven task (status `todo`) from each finding via `aven add`
6. Releases the lock

Claude is read-only — it scans and reports. The orchestrator owns all file creation.

### `vteam run coder`

1. Acquires an advisory lock (`vteam/.locks/coder.lock`)
2. Picks the highest-severity ready task routed to it from aven (`aven list --status todo --ready` filtered by `actor`, see [Routing tasks to agents](#routing-tasks-to-agents); oldest first within a severity; skips tasks that have failed `maxRetries` times) and sets it to `active`
3. Creates a git worktree at `.vteam-worktrees/vteam/<task-title-slug>-<runId>` branched from the base branch
4. Builds the prompt with the task description, existing task titles, and agent instructions
5. Spawns `claude -p` in the worktree — Claude implements the fix and returns a commit message as structured JSON
6. If Claude reported changes:
   - The orchestrator commits (`git add -A` + `git commit` using Claude's commit message)
   - Force-pushes the branch to origin
   - Creates a pull request via `gh` or `glab` (falls back gracefully if CLI is missing or labels don't exist)
   - Sets the task to `done` with completion metadata
7. If Claude reported no changes: returns the task to `todo` and increments its `retry-count` metadata
8. On any other ending (crash, error): returns the task to `todo` without incrementing retries
9. Cleans up the worktree
10. Releases the lock

### `vteam run review-responder`

1. Acquires an advisory lock (`vteam/.locks/review-responder.lock`)
2. Discovers open PRs that have the `prTriggerLabel` (e.g. `vteam:changes-requested`) applied
3. Checks out the PR branch into an isolated git worktree
4. Builds a prompt containing all unresolved review comments from the PR
5. Spawns `claude -p` in the worktree — Claude addresses the feedback, replies to each comment thread, and returns a commit message as structured JSON
6. If Claude reported changes:
   - The orchestrator commits and force-pushes the branch to origin
   - Posts a summary comment on the PR
   - Removes the `prTriggerLabel` so the agent won't re-process the PR on the next run
7. Cleans up the worktree
8. Releases the lock

The intended loop: a reviewer leaves comments on a PR, adds the `vteam:changes-requested` label, and runs `vteam run review-responder`. The agent addresses the feedback, pushes an updated commit, and replies to threads. The reviewer re-reviews the updated PR.

### `vteam status`

Displays the aven project and label, task counts per aven status, lists active and todo tasks with their refs and retry counts, shows active worktrees, and prints recent run IDs.

### `vteam clean`

Removes orphaned worktrees and breaks stale locks. Run this after a crash or if a lock is stuck.

### `vteam skill install`

Symlinks all Claude Code skills from the project's `skills/` directory into `~/.claude/skills/`, making them available globally. Each subdirectory of `skills/` that contains a `SKILL.md` file is treated as a skill.

Idempotent — re-running updates stale symlinks and skips already-installed ones.

### `vteam skill uninstall`

Removes the symlinks created by `vteam skill install`. Only removes symlinks — the source files in the repo are untouched.

## Configuration

### `vteam.config.json`

Global settings live in `vteam/vteam.config.json`:

```json
{
  "baseBranch": "main",
  "platform": "github",
  "worktreeDir": ".vteam-worktrees",
  "tasks": {
    "maxRetries": 3,
    "project": "my-app",
    "label": "vteam",
    "defaultActor": "coder"
  }
}
```

| Field              | Description                                                                             |
| ------------------ | --------------------------------------------------------------------------------------- |
| `baseBranch`       | Branch to create worktrees from and target PRs against                                  |
| `platform`         | `"github"` or `"gitlab"` — determines which CLI (`gh` / `glab`) is used for PR creation |
| `worktreeDir`      | Where worktrees are created (relative to repo root). Gitignored.                        |
| `tasks.maxRetries` | How many times the coder retries a failing task before skipping it                      |
| `tasks.project`    | Required. The aven project vteam's tasks live in. aven creates it on the first task. `vteam init` sets it to the current directory name. |
| `tasks.label`      | Default `"vteam"`. vteam only sees and picks up tasks carrying this label. Created automatically. |
| `tasks.defaultActor` | Required. Agent that works on tasks with no `actor` (see [Routing tasks to agents](#routing-tasks-to-agents)). `vteam init` sets `coder`. |
| `tasks.workspace`  | Optional. aven workspace, passed to every `aven` call as `--workspace`.                 |

### Agent configuration (AGENT.md frontmatter)

Agent behavior is configured via YAML frontmatter in each agent's `AGENT.md`. The markdown body (after the frontmatter) becomes the agent's system prompt.

```yaml
---
model: sonnet
worktree: true
input: task
autoPR: true
prCreateLabels: [vteam]
---
```

| Field             | Default    | Description                                                                                                      |
| ----------------- | ---------- | ---------------------------------------------------------------------------------------------------------------- |
| `model`           | `"sonnet"` | Claude model (`"sonnet"`, `"opus"`, `"haiku"`)                                                                   |
| `worktree`        | `false`    | Run in an isolated git worktree; push branch on commit                                                           |
| `readOnly`        | `false`    | Run in a worktree but skip commit/push/PR (requires `worktree: true`, incompatible with `autoPR`). Agent runs freely — `readOnly` only prevents the orchestrator from committing/pushing afterward. |
| `output`          | —          | `"task"` to produce findings that the orchestrator creates as aven tasks. When omitted, the agent uses generic content output. All agents share a unified output schema — `output` controls only the `content` field type. |
| `assignTo`        | —          | Requires `output: "task"`. Agent that should work on the created tasks (sets aven metadata `actor`). Omit to route to `tasks.defaultActor`. |
| `input`           | —          | `"task"` to pick from the aven `todo` queue; `"pr"` to respond to PR review comments (requires `worktree: true`)         |
| `prTriggerLabel`  | —          | Label signalling "this PR needs work" (e.g. `vteam:changes-requested`); the orchestrator discovers PRs by this label and removes it after the agent pushes |
| `autoPR`          | `false`    | Create a pull request after pushing                                                                              |
| `prCreateLabels`  | —          | Labels applied to created PRs (auto-created if they don't exist)                                                 |
| `allowedTools`    | —          | Claude Code tools the agent may use (same syntax as `--allowedTools` CLI flag, e.g. `["Read", "Bash(git *)"]`)   |
| `disallowedTools` | —          | Claude Code tools the agent may NOT use (same syntax as `--disallowedTools` CLI flag)                            |

### On-finish hooks (ON_FINISH.md)

Place an `ON_FINISH.md` file alongside `AGENT.md` to run a post-run hook. After an agent completes (success or failure), the orchestrator spawns a second `claude -p` call with the run outcome injected as context.

This lets you add notifications, logging, or any other post-run action without modifying vteam itself — Claude handles the integration using whatever MCPs or CLI tools are available in your environment.

```yaml
---
model: haiku
allowedTools: ["Bash(curl *)", "mcp__slack__send_message"]
---

You are a notification bot. Post the run result to the #eng-prs Slack channel.
If the run failed, also ping @oncall.
```

The user prompt is auto-generated from the run outcome and includes: agent name, status (completed/failed), timestamps, task details, branch name, PR URL, reviewed PR info, agent content output (review text, analysis, etc.), and error message (if any). When the agent returns `content` in its output, the hook receives it in a `## Content` section — this allows hooks to act on the agent's primary deliverable (e.g. post a PR review as a GitHub comment).

| Field             | Default | Description                        |
| ----------------- | ------- | ---------------------------------- |
| `model`           | —       | Claude model override for the hook |
| `allowedTools`    | —       | Tools the hook may use             |
| `disallowedTools` | —       | Tools the hook may NOT use         |

The hook runs in the main project directory (not the worktree). Hook failures are logged but do not affect the agent run's exit status. The hook is opt-in — agents without an `ON_FINISH.md` skip it entirely.

## Task lifecycle

Tasks live in [aven](#prerequisites), a local-first task manager backed by SQLite. vteam scopes everything to one aven project (`tasks.project`) and one label (`tasks.label`, default `vteam`) — it only sees tasks that carry the label. The real output of vteam is the PRs created by the coder.

```
improver finds issue
        │
        ▼
    ┌──────┐  coder picks       ┌────────┐  branch + PR  ┌──────┐
    │ todo │ ─────────────────▶ │ active │ ────────────▶ │ done │
    └──────┘                    └────────┘               └──────┘
        ▲                           │
        └───────────────────────────┘
          no changes (retry-count + 1),
          crash or error (no increment)
```

Any ending other than a pushed commit or "no changes" — a crash, an error, or a `readOnly` / non-worktree agent with `input: "task"` — returns the task to `todo` without incrementing `retry-count`.

### Routing tasks to agents

Every task can carry an `actor` metadata field naming the agent that should work on it:

- An agent that creates tasks sets `actor` from its `assignTo` frontmatter (e.g. a improver with `assignTo: test-writer` hands its findings to the test-writer). Without `assignTo`, no `actor` is set.
- An `input: "task"` agent only picks tasks whose `actor` is its own name. The agent named in `tasks.defaultActor` also picks every task with no `actor` — including tasks you create yourself in aven.
- Reassign a task with `aven edit <ref> --metadata actor=<agent>`, or hand it back to the default actor with `aven edit <ref> --remove-metadata actor`.
- `found-by` records which agent found the task; it does not affect routing.
- `vteam status` shows each open task's actor and warns when an actor has no matching `vteam/agents/<name>/`.

Agents only pick tasks returned by `aven list --status todo --ready`, so aven's dependency blocking and deferral (`available_at`) are respected. If the vteam process is killed hard mid-run, the task may stay `active` — reset it manually with `aven edit <ref> --status todo`.

Because tasks live in aven, you can triage them with `aven tui` or the `aven` CLI: move findings to `backlog` or `canceled`, change priority, add dependencies, or add the `vteam` label to your own tasks to queue them for the coder.

### How a task is represented in aven

| aven field             | Set by vteam                                                                    |
| ---------------------- | ------------------------------------------------------------------------------- |
| title                  | The finding's title                                                             |
| project / label        | `tasks.project` / `tasks.label`                                                 |
| status                 | `todo` on creation; `active` while being worked on; `done` on success           |
| priority               | From severity: `critical`→`urgent`, `high`→`high`, `medium`→`medium`, `low`→`low` (aven `none` reads back as `low`) |
| description            | Markdown with `## Description`, `## Suggested Fix` (optional), `## Affected Files` |
| metadata `found-by`    | Name of the agent that created the task (provenance only)                       |
| metadata `actor`       | Agent that should work on the task; absent means `tasks.defaultActor`           |
| metadata `files`       | Comma-separated affected files                                                  |
| metadata `retry-count` | Incremented each time the coder finishes with no changes                        |
| metadata `completed`, `branch`, `pr-url` | Added when the task is set to `done` (`pr-url` only if a PR was created) |

Tasks are referenced by their aven ref (e.g. `APP-7KQ9`); run state files in `vteam/.runs/` record it as `taskRef`. Refs are local to aven, so vteam keeps them out of branch names and PRs.

### Severity levels

`critical` > `high` > `medium` > `low`

The coder picks the highest-severity task first.

## Memory and deduplication

There is no separate overview file. At prompt-build time, the orchestrator lists every non-deleted task in the aven project carrying the vteam label — all statuses (inbox, backlog, todo, active, done, canceled) — and injects them into the agent's prompt as `- [status] severity | title | files`. Claude uses this list to avoid reporting duplicate findings. Canceling a task in aven therefore marks it as rejected (e.g. a false positive): the reviewer sees it and won't report it again.

Claude returns findings as structured JSON; the orchestrator creates the actual aven tasks.

### Per-agent memory (MEMORY.md)

Agents can maintain persistent memory across runs. Place a `MEMORY.md` file alongside `AGENT.md` to define a memory curation strategy — natural language rules for how the agent's memory should be maintained.

```yaml
---
model: haiku
---

Keep a running list of areas scanned and key patterns observed.
Record false positives to avoid repeating them.
Maximum 30 lines — drop oldest entries first.
```

**How it works:**

1. Before each run, the orchestrator reads the agent's memory file (`vteam/.memory/<agent-name>/store.md`) and injects it into the prompt
2. The agent includes a `memoryUpdate` field in its structured JSON output with observations from the current run
3. After the run, the orchestrator spawns a small curation agent that merges the new update into existing memory according to the MEMORY.md rules
4. The orchestrator writes the curated result back to the memory file (full replacement)

Memory files are stored in `vteam/.memory/` and are gitignored — they are local workflow state. The curation agent failure is non-fatal and does not affect the agent run's exit status.

| Field             | Default | Description                                    |
| ----------------- | ------- | ---------------------------------------------- |
| `model`           | —       | Claude model override for the curation agent   |
| `allowedTools`    | —       | Tools the curation agent may use               |
| `disallowedTools` | —       | Tools the curation agent may NOT use           |

Memory is opt-in — agents without a `MEMORY.md` skip it entirely. `vteam init` scaffolds a default MEMORY.md for each built-in agent.

## Agents

vteam ships with four default agents (improver, coder, review-responder, test-writer). Add custom agents by creating `vteam/agents/<name>/AGENT.md` — no config changes needed.

### Improver

- Read-only — scans the codebase, never modifies any files
- Returns findings as structured JSON — the orchestrator creates them as aven tasks in `todo`
- Does not use a worktree or commit — findings stay local until the coder acts on them
- Limited to 1 finding per run (configurable in the AGENT.md prompt)
- Prioritizes severity: security bugs > performance > code quality

### Coder

- Picks one ready task per run from the aven `todo` queue
- Works in an isolated git worktree (never touches the main working tree)
- Makes minimal, focused changes following existing code style
- Does not commit or push — the orchestrator handles all git operations and PR creation

### Review responder

- Triggered by the `prTriggerLabel` label on an open PR (e.g. `vteam:changes-requested`)
- Checks out the PR branch in an isolated worktree
- Reads all unresolved review comments and addresses the feedback
- Replies to each comment thread with an explanation
- Does not commit or push — the orchestrator handles all git operations

### Test writer

- Scans the codebase for untested or under-tested code
- Picks one function or feature per run — keeps PRs small and reviewable
- Studies existing test files to match project conventions (runner, helpers, mocking patterns)
- Works in an isolated git worktree
- Runs tests, type-check, and lint before reporting completion
- Does not modify source code — only adds or updates test files
- Does not commit or push — the orchestrator handles all git operations

All agents return a unified JSON output (`AgentOutput`). The orchestrator validates it, creates aven tasks from `content` when type is `"task"`, commits from `commitMessage` when present, and passes `content` through to ON_FINISH hooks. All agents receive existing task titles in their prompt, giving them full context of past and present work.

## Caveats and known limitations

### Locking

vteam uses advisory file locking via atomic `mkdir` (POSIX guarantees this is atomic). Each lock directory contains an `info.json` with the holding PID and timestamp.

- **Stale detection**: A lock is considered stale if the holding process is dead (checked via `kill -0`) or the lock is older than 30 minutes.
- **Not distributed**: Locks are local to the filesystem. If you run vteam from multiple machines against the same repo (e.g., via NFS), locks won't protect against concurrent runs.
- **Crash recovery**: If vteam crashes while holding a lock, the lock may persist. Run `vteam clean` to break stale locks, or wait 30 minutes for automatic stale detection.
- **Same agent concurrency**: Two runs of the same agent are serialized by the lock. Different agents (improver + coder) can run concurrently.

### Worktrees

- **Crash cleanup**: If the coder crashes after creating a worktree but before cleanup, the worktree and its branch persist. Run `vteam clean` to remove orphans.
- **Stale branches**: Task branch names include the run ID (`vteam/<title-slug>-<runId>`), so a retried task never reuses an earlier attempt's branch. If a local branch with the same name exists, `createWorktree` deletes it before creating a new worktree.
- **Force push**: vteam force-pushes to its own branches (`vteam/*`). This is safe because these branches are ephemeral and owned by the tool — no human should be pushing to them. But if you manually commit to a `vteam/*` branch, those commits will be lost on the next coder run.
- **Disk space**: Each worktree is a full checkout. On large repos, this can be significant. Worktrees are cleaned up after each run.

### PR creation

- Requires `gh` (GitHub) or `glab` (GitLab) CLI installed and authenticated.
- If the CLI is missing, the branch is still pushed — you just need to create the PR manually.
- Labels are auto-created in the repository if they don't already exist.
- Set `autoPR: false` in the agent's AGENT.md frontmatter to skip PR creation entirely.

### General

- **No budget caps**: There's no `--max-budget-usd` on agent runs. A single improver or coder invocation uses one Claude session with no spending limit. Monitor usage via your Anthropic dashboard.
- **No rollback**: If the coder's changes break something, you close the PR. There's no automatic revert mechanism.
- **Title-based dedup only**: Duplicate detection relies on Claude reading existing task titles in its prompt. Similar but differently-worded findings may slip through.
- **One `aven show` per task**: `aven list --json` doesn't include metadata or descriptions, so building the task index runs `aven show --full --json` for every labelled task (8 at a time). Large projects make prompt building slower.
- **Stuck `active` tasks**: If the vteam process is killed hard, the picked task stays `active` and won't be retried until reset with `aven edit <ref> --status todo`.

## Claude Code skills

Skills live in `skills/` and are symlinked into `~/.claude/skills/` so they work from any project. Install with:

```bash
vteam skill install
```

See `skills/SETUP.md` for details.

### `/vteam` — context injection

A non-interactive skill that injects full vteam knowledge into the current Claude Code session. Invoke it with `/vteam` when working in a project that uses vteam — Claude will understand the framework's architecture, CLI commands, agent configuration, and design principles without needing to read the docs.

### `/create-vteam-agent` — agent scaffolding

An interactive skill that scaffolds new vteam agents. It asks clarifying questions about the agent's purpose, input/output modes, worktree needs, tool permissions, memory, and hooks — then generates the `AGENT.md` (and optionally `MEMORY.md` / `ON_FINISH.md`) files.

Invoke from any project with `/create-vteam-agent`.

## Development

```bash
just build          # tsc + copy templates to dist/
just dev <args>     # run CLI via tsx (e.g., just dev run improver)
just lint           # tsc --noEmit + eslint
just test           # vitest
just clean          # rm -rf dist/
```

### Dogfooding

This project has its own `vteam/` directory pointing the agents at `src/`. Run `just dev run improver` to have vteam review itself.

### Dependencies

vteam aims to keep external dependencies to a minimum. YAML frontmatter parsing, slug generation, and other utilities are handled by internal modules with no external dependencies.

### System dependencies

| Tool     | Required   | Purpose                                  |
| -------- | ---------- | ---------------------------------------- |
| `claude` | Yes        | Claude Code CLI — all agent intelligence |
| `git`    | Yes        | Worktree management, branch operations   |
| `aven`   | Yes        | Task storage and lifecycle               |
| `gh`     | For GitHub | Pull request creation (`gh pr create`)   |
| `glab`   | For GitLab | Pull request creation (`glab mr create`) |

## License

MIT
