# vteam

A virtual development team framework that orchestrates AI agents powered by [Claude Code](https://docs.anthropic.com/en/docs/claude-code) to autonomously review codebases and implement fixes.

You define agent prompts, triage findings, and review pull requests. vteam handles everything else: scheduling agent runs, managing task lifecycle, isolating work in git worktrees, and creating branches and pull requests.

## How it works

vteam runs Claude in headless mode (`claude -p`) as a subprocess. Each agent invocation is stateless — all memory is external (tasks in [aven](#task-lifecycle), per-agent memory files) and injected into the prompt.

```
┌─────────────────────────────────────────────────────────────┐
│                         vteam (Go)                          │
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

- **Go** 1.27+ (to install from source)
- **Claude Code** CLI — `claude` must be on your PATH ([install guide](https://docs.anthropic.com/en/docs/claude-code/getting-started))
- **Git** — the project must be a git repository
- **aven** — the local-first task manager CLI. `aven` must be installed and on your PATH; vteam stores all tasks in it.
- **`gh`** (GitHub CLI) or **`glab`** (GitLab CLI) — required for automatic pull request creation. Without it, branches are pushed but PRs must be created manually.

## Installation

```bash
go install github.com/leodnlv/vteam/cmd/vteam@latest
```

Or from a checkout:

```bash
git clone git@github.com:leodnlv/vteam.git
cd vteam
just install        # installs vteam into $(go env GOBIN), usually ~/go/bin; put that on your PATH
```

Run `vteam skill install` from the root of a checkout: it symlinks the `skills/` directory there into `~/.claude/skills/`, so keep the clone if you want the skills.

## Quick start

```bash
cd your-project

# 1. Scaffold the vteam directory
vteam init

# 2. Edit the config
$EDITOR vteam/vteam.config.json

# 3. Run the improver — finds issues, creates them as aven tasks in inbox
vteam run improver

# 3b. Triage — move the findings you want fixed to todo
aven edit <ref> --status todo

# 4. Run the coder — picks a task, fixes it, pushes a branch, opens a PR
vteam run coder

# 5. Review the PR as you would any other
```

### Running on a schedule

Crons are opt-in per agent. The `cron` in an agent's `agent.json` (and `mergeEnsurer.cron` for the [merge ensurer](#vteam-ensure-merge)) is only the default pattern; nothing runs on a schedule until you enable it. The easiest way is the [TUI](#vteam-interactive-tui): select an agent in the Agents tab, press `space`, and pick "Run on cron…". That edits the pattern, enables it, and starts the scheduler in the background. From the shell:

```bash
# Enable a cron (the pattern defaults to the one in agent.json)
vteam cron enable improver
vteam cron enable coder '*/10 * * * *'

# Start the scheduler for the enabled crons (foreground)
vteam cron start

# Check whether the scheduler is running, which crons are enabled, and when they fire next
vteam cron status

# Disable a cron
vteam cron disable improver
```

Enabled crons live in `vteam/.cron/schedules.json`. That file is local to your machine, and its directory has its own `.gitignore`. The scheduler rereads it every 2 seconds, so enabling, changing, or disabling a cron takes effect without a restart. When no crons are left enabled and no scheduled run is still going, the scheduler stops on its own. Enabled crons survive a scheduler stop or a reboot: they show as paused until the scheduler starts again (`vteam cron start`, or `S` in the TUI).

`vteam cron start` is a foreground process — stop it with Ctrl+C. A scheduler the TUI starts runs in the background and writes its output to `vteam/.cron/scheduler.log`. While a scheduler runs it writes its pid to `vteam/.locks/cron.pid`; a second `vteam cron start` in the same project refuses to start, and `vteam status` uses the file to report whether the scheduler is up. If an agent is still running when its next cron tick fires, the tick is skipped. Cron expressions have 5 fields (minute hour day month weekday) and support ranges, steps, lists, month and weekday names, and `7` for Sunday; `L` and `#` are not supported. When both day-of-month and day-of-week are set, either one matching fires the job. Each scheduled run writes its own log file, the same as a manual `vteam run` (see [Logs](#logs)). The scheduler prints the job table from `vteam status`, then one line per job start, finish, failure, or skipped tick, stamped with the time of day; starts and failures include the path to that run's log file.

## Commands

### `vteam init`

Scaffolds a `vteam/` directory in the current project:

```
vteam/
├── vteam.config.json               # Project-level configuration
├── agent.schema.json               # JSON Schema for agent.json (editor autocomplete/validation)
├── agents/
│   ├── improver/
│   │   ├── agent.json              # Agent configuration
│   │   ├── AGENT.md                # Improver prompt/personality
│   │   └── MEMORY.md               # Memory curation strategy
│   ├── security-analyst/
│   │   ├── agent.json
│   │   ├── AGENT.md                # Security analyst prompt/personality
│   │   └── MEMORY.md
│   ├── coder/
│   │   ├── agent.json
│   │   ├── AGENT.md                # Coder prompt/personality
│   │   └── MEMORY.md
│   └── test-writer/
│       ├── agent.json
│       ├── AGENT.md                # Test writer prompt/personality
│       └── MEMORY.md
```

Sets `tasks.project` in the config to the current directory name. Also adds `.vteam-worktrees/` and vteam's local state directories (`vteam/.locks/`, `vteam/.logs/`, `vteam/.runs/`, `vteam/.memory/`) to `.gitignore`. Tasks live in aven, not in the repo — the real shared artifacts are the PRs that the coder creates.

Will refuse to run if `vteam/` already exists.

### `--focus` option

All `vteam run` invocations accept `--focus <text>` to inject priority context into the agent's prompt. The text is added as the first section of the user prompt so Claude prioritizes it above all other considerations.

```bash
vteam run coder --focus "the issue about XYZ"
vteam run test-writer --focus "invite user functionality"
vteam run improver --focus "only look at the auth module"
vteam run security-analyst --focus "file upload endpoints"
```

### Logs

Every `vteam run <agent>` writes its full output to a new file at `vteam/.logs/<agent>/<ISO-timestamp>.log`, whether you start it by hand, from the TUI, or the scheduler starts it. The output still goes to your terminal; the log file gets a copy of it. This includes Claude's tool calls, orchestrator messages, the ON_FINISH hook and memory curation, plus the error if the run crashes. The path is printed when the run starts.

The output is grouped into sections (Setup, Agent, Result, hooks, Cleanup), and each line starts with the time since the run started. Glyphs mark the kind of line: `•` orchestrator step, `❯` Claude text, `⚙` tool call, `✓` success, `!` warning, `✗` error (including failed tool calls). In a terminal the output is colored and tool inputs are cut to one line. The log file has no colors and keeps the full tool input. Set `NO_COLOR=1` to turn colors off, or `FORCE_COLOR=1` to force them on.

### `vteam run improver`

1. Acquires an advisory lock (`vteam/.locks/improver.lock`)
2. Reads the agent prompt from `vteam/agents/improver/AGENT.md`
3. Lists all vteam-labelled tasks in the aven project (every status) and injects them into the prompt so Claude knows what's already been found
4. Spawns `claude -p` — Claude scans the codebase and returns findings as structured JSON
5. The orchestrator creates an aven task (status `inbox`) from each finding via `aven add`. Findings wait in inbox until a human moves them to `todo`
6. Releases the lock

Claude is read-only — it scans and reports. The orchestrator owns all file creation.

### `vteam run coder`

1. Acquires an advisory lock (`vteam/.locks/coder.lock`)
2. Under a short-lived claim lock shared by all agents (`vteam/.locks/task-claim.lock`), picks the highest-severity ready task routed to it from aven (`aven list --status todo --ready` filtered by its `taskFilter`, see [Routing tasks to agents](#routing-tasks-to-agents); oldest first within a severity; skips tasks that have failed `maxRetries` times) and sets it to `active` with `claimed-*` metadata
3. Fetches the base branch and creates a git worktree at `.vteam-worktrees/vteam/<task-title-slug>-<runId>` branched from `origin/<baseBranch>`, so the branch starts from the latest remote code and never picks up unpushed local commits
4. Builds the prompt with the task description, the task's aven notes (including summaries from earlier attempts), existing task titles, and agent instructions
5. Spawns `claude -p` in the worktree — Claude implements the fix and returns a commit message as structured JSON
6. If Claude reported changes:
   - The orchestrator commits (`git add -A` + `git commit` using Claude's commit message)
   - Pushes the branch to origin with `--force-with-lease`, retrying network errors
   - Creates a pull request via `gh` or `glab` (falls back gracefully if CLI is missing or labels don't exist)
   - Sets the task to `done` with completion metadata
7. If Claude reported no changes: returns the task to `todo` and increments its `retry-count` metadata
8. On any other ending (crash, error): returns the task to `todo` without incrementing retries
9. Unless it crashed or errored, adds an aven note to the task with Claude's summary (and blocker reason or PR URL, if any)
10. Cleans up the worktree
11. Releases the lock

#### Following up on review comments

When review comments on a coder PR need work, add the `mergeEnsurer.label` label (default `vteam:changes-requested`) to the PR. The next [`vteam ensure-merge`](#vteam-ensure-merge) moves the task back to `todo`. You can also reopen the task in aven yourself and leave a note saying which comments to apply:

```sh
aven edit <ref> --status todo
aven note <ref> "Apply the null-check and retry comments. Ignore the rename suggestion."
```

The next coder run sees the task's `pr-url` metadata and works on the existing PR instead of starting fresh:

- If the PR is merged or closed, the task moves to `backlog` with a note and Claude does not run. To start over on a new branch, remove the metadata (`aven edit <ref> --remove-metadata pr-url`) and move the task back to `todo`.
- Otherwise the PR branch is checked out into the worktree, and the prompt gets the task, all its notes, and the PR's unresolved review threads. Resolved threads and top-level PR comments are left out. Claude applies the threads your note names, or all of them if the note doesn't say.
- If the PR conflicts with its base branch, vteam merges `origin/<base>` into the worktree before Claude runs and lists the conflicted files in the prompt. Claude resolves the conflicts and vteam commits the merge with Claude's commit message. If any conflict marker is left, nothing is committed and the task's `retry-count` goes up. If the merge applies cleanly, it is pushed even when Claude changes nothing else.
- Changes are pushed to the PR branch, no new PR is opened, and the task goes back to `done`.
- Claude returns a reply for each thread it addressed. After the push, vteam posts each reply in its thread, marked `🤖 **vteam coder:**` and ending with `Fixed in <sha>`. Threads Claude skipped get no reply, and nothing is posted if no commit was pushed. Set `"resolveThreads": true` in the agent's `agent.json` to also resolve the threads it replied to. The run's aven note records how many replies were posted.

### `vteam ensure-merge`

A coder task is `done` once its PR is open, but the PR still has to get through review. An agent that opens a PR without a task (like the test writer) gets a `done` task recording the PR, so every vteam PR is tracked. The merge ensurer checks every `done` task that has `pr-url` metadata and no `merged=true` metadata. It does not run Claude:

| PR state | What happens |
| --- | --- |
| merged | Sets metadata `merged=true`. The task stays `done` and is not checked again |
| closed without merging | Sets the task to `canceled` with a note. Canceled tasks stay in the dedup list, so the finding is not reported again |
| open, with the `mergeEnsurer.label` label | Removes the label, moves the task to `todo`, clears `retry-count`, and adds a note. The coder then follows up on the PR |
| open, conflicting with its base branch | Moves the task to `todo` with a note. The coder [merges the base branch and resolves the conflicts](#following-up-on-review-comments) |
| open, otherwise | Nothing. Review comments alone never reopen a task; only the label you add does |

The label is removed before the task is reopened. If that removal fails, the task stays `done` and the next run tries again. Removing it first means a label left behind can never reopen the task over and over. While GitHub or GitLab is still working out whether a PR is mergeable, conflicts are skipped until a later run. To run the ensurer on a schedule, enable its cron (`vteam cron enable merge-ensurer`, or the TUI). `mergeEnsurer.cron` is its default pattern. Each run writes a log to `vteam/.logs/merge-ensurer/`.

### `vteam status`

Shows what is happening now and what will happen next. It reads local state and the aven project; it makes no GitHub/GitLab calls.

- **Running** — each agent run (and the merge ensurer) whose lock is held by a live process: pid, elapsed time, the task it claimed, and its worktree.
- **Scheduler** — whether the scheduler is running, and each enabled cron with its next fire time.
- **Up next** — for each `input: "task"` agent, the task it would pick if it ran now (same routing, retry limit, and ordering as `vteam run`), marked `(PR follow-up)` when it would continue an existing PR. `input: "pr"` agents show the PR label they watch.
- **Awaiting merge** — `done` tasks whose PR has not been recorded as merged.
- **Needs attention** — tasks in `inbox` awaiting triage, `todo` tasks no agent's `taskFilter` matches, `todo` tasks at `maxRetries`, `active` tasks whose claiming process on this machine is dead or that have no vteam claim, and worktrees no running agent is using.

For the full task list, use aven (`aven list --label vteam`).

### `vteam` (interactive TUI)

Running `vteam` with no arguments inside a project opens a full-screen, keyboard-driven view with three tabs. Outside a terminal, or in a directory without `vteam/`, it prints help instead.

- **Overview** — the `vteam status` view, refreshed every 2 seconds. A refresh starts only after the previous one finishes, so a large aven project refreshes less often instead of piling up work. If a refresh fails, the last good view stays and the error appears in the footer.
- **Agents** — a sidebar of your agents (plus the merge ensurer) with a live/idle marker and details for the selected one, next to its newest run log. The log pane follows the log while the agent runs; scroll up to pause, `G` to follow again. `p` / `n` step to the previous / next run, and the `run i/n` counter in the title turns yellow when you are looking at an older run. `w` toggles line wrap.
  - **Running agents from the TUI** — `space` (or `a`) opens an action menu for the selected agent: run once, run on cron or edit its cron, stop its cron, start the scheduler, open the log. Each action also has its own key (`x`, `c`, `s`, `S`).
    - **Run once** asks for confirmation, then starts `vteam run <agent>` in the background. The run keeps going after you quit the TUI.
    - **Run on cron** opens a dialog prefilled with the enabled pattern, or else the `agent.json` default. As you type it shows the schedule in words, the next three fire times, or the parse error. `tab` toggles saving the pattern as the default in `agent.json` (`vteam.config.json` for the merge ensurer). Confirming enables the cron and, if no scheduler is running, starts one in the background (see [Running on a schedule](#running-on-a-schedule)).
    - **Cron markers** — an agent on cron shows `◷` in the sidebar, its pattern and next run in the details, and a line above its log. All three turn yellow with "paused" when the scheduler is not running.
- **Info** — the project, `vteam.config.json`, whether the scheduler is running, and each agent's settings, including the tools it is allowed.

The top-right corner shows the aven project, task label, and git branch.

| Keys | Action |
|---|---|
| `1` `2` `3`, `tab` / `shift+tab` | switch tabs |
| `j` `k` / `↓` `↑` | scroll; in the Agents sidebar, select an agent |
| `ctrl+d` `ctrl+u` / `pgdown` `pgup` | half page down / up |
| `gg` / `G` | top / bottom |
| `l` `enter` `→` / `h` `esc` `←` | Agents tab: focus the log / the sidebar |
| `p` / `n` | Agents tab: previous (older) / next (newer) run |
| `f` / `w` | Agents tab: toggle follow / line wrap |
| `space` `a` | Agents tab: action menu for the selected agent |
| `x` / `c` / `s` / `S` | Agents tab: run once / run on cron or edit its cron / stop its cron / start the scheduler |
| `r` | refresh now |
| `?` | help |
| `q` `ctrl+c` | quit |

### `vteam clean`

Removes the git worktrees under `worktreeDir` that no running agent is using; a worktree belongs to a running agent when that agent's lock is held by a live process and its current run state in `vteam/.runs/` points at it. It also breaks stale locks (held by a dead process, or missing their info file and older than 30 minutes) and returns orphaned tasks to `todo`: tasks that are `active` with a claim from this machine whose process is no longer running. Tasks claimed on another machine and `active` tasks without a vteam claim are listed but left alone. Run this after a crash or if a lock is stuck.

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
    "label": "vteam"
  },
  "mergeEnsurer": {
    "cron": "*/15 * * * *",
    "label": "vteam:changes-requested"
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
| `tasks.workspace`  | Optional. aven workspace, passed to every `aven` call as `--workspace`.                 |
| `mergeEnsurer.cron` | Optional. Default cron pattern for [`vteam ensure-merge`](#vteam-ensure-merge), used when you enable its cron (see [Running on a schedule](#running-on-a-schedule)). |
| `mergeEnsurer.label` | Default `"vteam:changes-requested"`. The PR label you add to approve the review feedback on a coder PR. Create it in GitHub/GitLab once. |

### Agent configuration (agent.json)

Each agent lives in `vteam/agents/<name>/`. `AGENT.md` is required and holds only the system prompt; its presence is what makes the directory an agent. Orchestrator behavior is configured in an optional `agent.json` next to it (missing = all defaults):

```json
{
  "$schema": "../../agent.schema.json",
  "model": "sonnet",
  "cron": "0 */6 * * *",
  "worktree": true,
  "input": "task",
  "autoPR": true,
  "prCreateLabels": ["vteam"]
}
```

`vteam init` writes `vteam/agent.schema.json` so editors can autocomplete and validate `agent.json` through the `$schema` reference. Cross-field rules (e.g. `readOnly` requires `worktree`) are not in the JSON Schema; vteam checks them when it loads the agent.

Validation is strict: unknown keys are rejected everywhere, including inside `memory` and `onFinish`. Invalid JSON fails with `Invalid JSON in <name>/agent.json`; schema violations fail with `Invalid config in <name>/agent.json:` followed by one line per offending path. YAML frontmatter is rejected: an `AGENT.md`, `MEMORY.md` or `ON_FINISH.md` that starts with `---` is a load error. Agents in the old format must be converted by hand — move the frontmatter fields into `agent.json` and leave only the prompt in the markdown file.

| Field             | Default    | Description                                                                                                      |
| ----------------- | ---------- | ---------------------------------------------------------------------------------------------------------------- |
| `model`           | —          | Claude model passed to `claude --model` (e.g. `"sonnet"`, `"opus"`, `"haiku"`, or a full model ID). Omit to use Claude Code's default. |
| `cron`            | —          | Default cron expression (5 fields: minute hour day month weekday), used when you enable the agent's cron in the TUI or with `vteam cron enable`. Setting it does not schedule the agent. Validated on load. |
| `worktree`        | `false`    | Run in an isolated git worktree; push branch on commit                                                           |
| `readOnly`        | `false`    | Run in a worktree but skip commit/push/PR (requires `worktree: true`, incompatible with `autoPR`). Agent runs freely — `readOnly` only prevents the orchestrator from committing/pushing afterward. |
| `output`          | —          | `"task"` to produce findings that the orchestrator creates as aven tasks (incompatible with `autoPR` and `readOnly`). When omitted, the agent uses generic content output. All agents share a unified output schema — `output` controls only the `content` field type. |
| `input`           | —          | `"task"` to pick from the aven `todo` queue; `"pr"` to respond to PR review comments (requires `worktree: true`)         |
| `taskFilter`      | —          | Requires `input: "task"`. Map of aven metadata key → value, list of values, or `null` (key missing). Picks only matching tasks. See [Routing tasks to agents](#routing-tasks-to-agents). |
| `prTriggerLabel`  | —          | Label signalling "this PR needs work" (e.g. `vteam:changes-requested`); the orchestrator discovers PRs by this label and removes it after the agent pushes |
| `autoPR`          | `false`    | Create a pull request after pushing                                                                              |
| `prCreateLabels`  | —          | Labels applied to created PRs (auto-created if they don't exist)                                                 |
| `resolveThreads`  | `false`    | Requires `input: "task"` and `worktree: true`. On a [PR follow-up](#following-up-on-review-comments), resolve each review thread the agent replied to |
| `allowedTools`    | —          | Claude Code tools the agent may use (same syntax as `--allowedTools` CLI flag, e.g. `["Read", "Bash(git *)"]`). When set, the agent gets no other built-in tools; omit it for Claude Code's default set. MCP tools from your Claude Code config stay available. |
| `disallowedTools` | —          | Claude Code tools the agent may NOT use (same syntax as `--disallowedTools` CLI flag)                            |
| `memory`          | —          | Curation agent settings: `model`, `allowedTools`, `disallowedTools`. Requires `MEMORY.md`. See [Per-agent memory](#per-agent-memory-memorymd). |
| `onFinish`        | —          | Hook settings: `model`, `allowedTools`, `disallowedTools`. Requires `ON_FINISH.md`. See [On-finish hooks](#on-finish-hooks-on_finishmd). |

### On-finish hooks (ON_FINISH.md)

Place an `ON_FINISH.md` file alongside `AGENT.md` to run a post-run hook. After an agent completes (success or failure), the orchestrator spawns a second `claude -p` call with the run outcome injected as context.

This lets you add notifications, logging, or any other post-run action without modifying vteam itself — Claude handles the integration using whatever MCPs or CLI tools are available in your environment.

`ON_FINISH.md` is the hook's system prompt:

```markdown
You are a notification bot. Post the run result to the #eng-prs Slack channel.
If the run failed, also ping @oncall.
```

The hook's model and tools go in the `onFinish` section of `agent.json`:

```json
{
  "onFinish": {
    "model": "haiku",
    "allowedTools": ["Bash(curl *)", "mcp__slack__send_message"]
  }
}
```

The user prompt is auto-generated from the run outcome and includes: agent name, status (completed/failed), timestamps, task details, branch name, PR URL, reviewed PR info, agent content output (review text, analysis, etc.), and error message (if any). When the agent returns `content` in its output, the hook receives it in a `## Content` section — this allows hooks to act on the agent's primary deliverable (e.g. post a PR review as a GitHub comment).

| `onFinish` field  | Default | Description                        |
| ----------------- | ------- | ---------------------------------- |
| `model`           | —       | Claude model override for the hook |
| `allowedTools`    | —       | Tools the hook may use             |
| `disallowedTools` | —       | Tools the hook may NOT use         |

The hook runs in the main project directory (not the worktree). Hook failures are logged but do not affect the agent run's exit status. The hook is opt-in — agents without an `ON_FINISH.md` skip it entirely. The `onFinish` section is optional; having one without `ON_FINISH.md` is a load error.

## Task lifecycle

Tasks live in [aven](#prerequisites), a local-first task manager backed by SQLite. vteam scopes everything to one aven project (`tasks.project`) and one label (`tasks.label`, default `vteam`) — it only sees tasks that carry the label. The real output of vteam is the PRs created by the coder.

```
improver finds issue
        │
        ▼
    ┌───────┐
    │ inbox │
    └───────┘
        │  human triage (aven edit <ref> --status todo)
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

Each `input: "task"` agent declares which tasks it works on with a `taskFilter` over aven metadata:

```jsonc
// vteam/agents/coder/agent.json: findings from the improver or the security analyst
{
  "input": "task",
  "taskFilter": { "found-by": ["improver", "security-analyst"] }
}

// vteam/agents/mr-reviewer/agent.json: tasks a human handed to it
{
  "input": "task",
  "taskFilter": { "actor": "mr-reviewer" }
}
```

- Different keys must all match (AND). A list of values matches any of them (OR). `null` matches a key that is missing or empty, so `"found-by": ["improver", null]` also covers tasks you create yourself in aven.
- An agent without `taskFilter` matches every task.
- Filters may overlap. Picking happens under a claim lock shared by all agents, so only one agent gets a task. Once it is `active`, it is off every other agent's list.
- vteam writes `found-by` (the creating agent) on every task. Any other key, like `actor`, is yours to set: `aven edit <ref> --metadata actor=mr-reviewer`.
- A task that no agent's filter matches is never picked. `vteam status` warns about `todo` tasks no agent will pick and shows which task each agent would pick next.

Agents only pick tasks returned by `aven list --status todo --ready`, so aven's dependency blocking and deferral (`available_at`) are respected. If the vteam process is killed hard mid-run, the task may stay `active`. Run `vteam clean` to return it to `todo`, or reset it manually with `aven edit <ref> --status todo`.

Because tasks live in aven, you can triage them with `aven tui` or the `aven` CLI: move findings to `backlog` or `canceled`, change priority, add dependencies, or add the `vteam` label to your own tasks to queue them for the coder.

### How a task is represented in aven

| aven field             | Set by vteam                                                                    |
| ---------------------- | ------------------------------------------------------------------------------- |
| title                  | The finding's title                                                             |
| project / label        | `tasks.project` / `tasks.label`                                                 |
| agent labels           | `<tasks.label>-<agent>` (e.g. `vteam-improver`): added on creation for the agent that created the task, and when a task is set to `done` for the agent that finished it (e.g. `vteam-coder`). Never removed, so they stay when a task is reopened |
| status                 | `inbox` on creation (`done` for a task recording a PR opened without a task); `active` while being worked on; `done` on success; `todo` again when the merge ensurer reopens it; `canceled` when the merge ensurer finds its PR closed; `backlog` when a reopened task's PR is merged or closed |
| priority               | From severity: `critical`→`urgent`, `high`→`high`, `medium`→`medium`, `low`→`low` (aven `none` reads back as `low`) |
| description            | Markdown with `## Description`, `## Suggested Fix` (optional), `## Affected Files` |
| metadata `found-by`    | Name of the agent that created the task (usable in `taskFilter`)                |
| metadata `retry-count` | Incremented each time the coder finishes with no changes                        |
| metadata `completed`, `branch`, `pr-url` | Added when the task is set to `done` (`pr-url` only if a PR was created). A `todo` task with `pr-url` is a [follow-up](#following-up-on-review-comments) on that PR |
| metadata `merged`      | `true` once the merge ensurer sees the task's PR merged                          |
| notes                  | One per run that returned output: `<agent>: <status>`, the agent's summary, blocker reason and PR URL if any. All notes, yours included, are shown to the agent that next picks the task |
| metadata `claimed-by`, `claimed-pid`, `claimed-host` | Run ID, process ID and hostname of the run working the task. Set with `active`, removed when the task leaves `active` |

Tasks are referenced by their aven ref (e.g. `APP-7KQ9`); run state files in `vteam/.runs/` record it as `taskRef`. Refs are local to aven, so vteam keeps them out of branch names and PRs.

### Severity levels

`critical` > `high` > `medium` > `low`

The coder picks the highest-severity task first.

## Memory and deduplication

There is no separate overview file. At prompt-build time, the orchestrator lists every non-deleted task in the aven project carrying the vteam label — all statuses (inbox, backlog, todo, active, done, canceled) — and injects them into the agent's prompt as `- [status] severity | title | files`. Claude uses this list to avoid reporting duplicate findings. Canceling a task in aven therefore marks it as rejected (e.g. a false positive): the reviewer sees it and won't report it again.

Claude returns findings as structured JSON; the orchestrator creates the actual aven tasks. vteam passes the output schema to `claude --json-schema`, so Claude Code itself rejects a final answer that is not valid output.

### Per-agent memory (MEMORY.md)

Agents can maintain persistent memory across runs. Place a `MEMORY.md` file alongside `AGENT.md` to define a memory curation strategy — natural language rules for how the agent's memory should be maintained.

```markdown
Keep a running list of areas scanned and key patterns observed.
Record false positives to avoid repeating them.
Maximum 30 lines — drop oldest entries first.
```

The curation agent's model and tools go in the `memory` section of `agent.json`, e.g. `"memory": { "model": "haiku" }`.

**How it works:**

1. Before each run, the orchestrator reads the agent's memory file (`vteam/.memory/<agent-name>/store.md`) and injects it into the prompt
2. The agent includes a `memoryUpdate` field in its structured JSON output with observations from the current run
3. After the run, the orchestrator spawns a small curation agent that merges the new update into existing memory according to the MEMORY.md rules
4. The orchestrator writes the curated result back to the memory file (full replacement)

Memory files are stored in `vteam/.memory/` and are gitignored — they are local workflow state. The curation agent failure is non-fatal and does not affect the agent run's exit status.

| `memory` field    | Default | Description                                    |
| ----------------- | ------- | ---------------------------------------------- |
| `model`           | —       | Claude model override for the curation agent   |
| `allowedTools`    | —       | Tools the curation agent may use               |
| `disallowedTools` | —       | Tools the curation agent may NOT use           |

Memory is opt-in — agents without a `MEMORY.md` skip it entirely, and a `memory` section without `MEMORY.md` is a load error. `vteam init` scaffolds a default MEMORY.md for each built-in agent.

## Agents

vteam ships with four default agents (improver, security-analyst, coder, test-writer). Their `agent.json` files come with default cron patterns, which run once you enable them (see [Running on a schedule](#running-on-a-schedule)): coder every 5 minutes (`*/5 * * * *`), improver on the hour (`0 * * * *`), security-analyst every 4 hours at quarter past (`15 */4 * * *`), and test-writer every 2 hours at half past (`30 */2 * * *`). The merge ensurer's default is every 15 minutes (`mergeEnsurer.cron`). Add custom agents by creating `vteam/agents/<name>/AGENT.md` (plus an optional `agent.json`) — no config changes needed.

### Improver

- Read-only — scans the codebase, never modifies any files
- Returns findings as structured JSON — the orchestrator creates them as aven tasks in `inbox` for human triage
- Does not use a worktree or commit — findings stay local until the coder acts on them
- Limited to 1 finding per run (configurable in the AGENT.md prompt)
- Prioritizes severity: security bugs > performance > code quality

### Security analyst

- Read-only — scans for one security finding per run and returns it as an aven task in `inbox`, like the improver
- Covers vulnerable and outdated dependencies, injection (SQL/NoSQL, command, path traversal, SSRF, XSS, prototype pollution), data leaks (secrets, logs, over-broad responses), missing mechanisms (validation, authorization, rate limiting, security headers), and shared security utilities
- May run `npm`/`pnpm`/`yarn` `audit`, `outdated`, `ls`, and `npm view` to check dependencies; `audit fix` is disallowed
- Its memory tracks reviewed attack surfaces, the security mechanisms the project already has, and accepted risks, so it rotates through the codebase instead of re-reporting
- Its findings are routed to the coder (`found-by: security-analyst`)

### Coder

- Picks one ready task per run from the aven `todo` queue, limited to improver and security-analyst findings, reopened test-writer PRs, and tasks you created yourself (`"taskFilter": { "found-by": ["improver", "security-analyst", "test-writer", null] }`)
- Works in an isolated git worktree (never touches the main working tree)
- Makes minimal, focused changes following existing code style
- Does not commit or push — the orchestrator handles all git operations and PR creation

### Test writer

- Scans the codebase for untested or under-tested code
- Picks one function or feature per run — keeps PRs small and reviewable
- Studies existing test files to match project conventions (runner, helpers, mocking patterns)
- Works in an isolated git worktree
- Runs tests, type-check, and lint before reporting completion
- Does not modify source code — only adds or updates test files
- Does not commit or push — the orchestrator handles all git operations
- Each PR it opens is recorded as a `done` task (`found-by: test-writer`), so the merge ensurer tracks it and the coder applies review feedback when it is reopened

All agents return a unified JSON output (`AgentOutput`). The orchestrator validates it, creates aven tasks from `content` when type is `"task"`, commits from `commitMessage` when present, and passes `content` through to ON_FINISH hooks. All agents receive existing task titles in their prompt, giving them full context of past and present work.

## Caveats and known limitations

### Locking

vteam uses advisory file locking via atomic `mkdir` (POSIX guarantees this is atomic). Each lock directory contains an `info.json` with the holding PID and timestamp.

- **Stale detection**: A lock is considered stale only if the holding process is dead (checked via `kill -0`), so a long run keeps its lock however long it takes. A lock directory without its info file (a crash right after `mkdir`) is stale after 30 minutes. Stale locks are renamed aside and checked again before being deleted, and a process only releases a lock it still owns.
- **Not distributed**: Locks are local to the filesystem. If you run vteam from multiple machines against the same repo (e.g., via NFS), locks won't protect against concurrent runs.
- **Crash recovery**: If vteam crashes while holding a lock, the lock may persist. The next run or `vteam clean` breaks it once the holding process is dead.
- **Same agent concurrency**: Two runs of the same agent are serialized by the lock. Different agents (improver + coder) can run concurrently.
- **Task claiming**: Picking a task (list → select → set `active`) runs under `vteam/.locks/task-claim.lock`, shared by all agents, so two agents with overlapping `taskFilter`s never get the same task. The lock is held only while picking, not for the whole run. Like every lock it is local: agents on different machines syncing one aven database can still race.

### Worktrees

- **Crash cleanup**: If the coder crashes after creating a worktree but before cleanup, the worktree and its branch persist. Run `vteam clean` to remove orphans.
- **Stale branches**: Task branch names include the run ID (`vteam/<title-slug>-<runId>`), so a retried task never reuses an earlier attempt's branch. If a local branch with the same name exists, `createWorktree` deletes it before creating a new worktree.
- **Pushing**: vteam pushes with `--force-with-lease` against the commit it started from. A new branch must not exist on the remote yet. A PR branch must still be where vteam checked it out, so if someone pushes to the PR while the coder runs, the push is rejected instead of overwriting their commits, and the task goes back to `todo`. Network errors (connection reset, broken pipe, remote hung up, timeouts) are retried twice, after 5s and 15s. Before each retry vteam checks whether the dropped push actually reached the remote.
- **Disk space**: Each worktree is a full checkout. On large repos, this can be significant. Worktrees are cleaned up after each run.

### PR creation

- Requires `gh` (GitHub) or `glab` (GitLab) CLI installed and authenticated.
- If the CLI is missing, the branch is still pushed — you just need to create the PR manually.
- Labels are auto-created in the repository if they don't already exist.
- Set `"autoPR": false` in the agent's `agent.json` to skip PR creation entirely.

### General

- **No budget caps**: There's no `--max-budget-usd` on agent runs. A single improver or coder invocation uses one Claude session with no spending limit. Monitor usage via your Anthropic dashboard.
- **No rollback**: If the coder's changes break something, you close the PR. There's no automatic revert mechanism.
- **Title-based dedup only**: Duplicate detection relies on Claude reading existing task titles in its prompt. Similar but differently-worded findings may slip through.
- **One `aven show` per task**: `aven list --json` doesn't include metadata or descriptions, so building the task index runs `aven show --full --json` for every labelled task (8 at a time). Large projects make prompt building slower.
- **Stuck `active` tasks**: If the vteam process is killed hard, the picked task stays `active` and won't be retried until `vteam clean` (on the same machine) or `aven edit <ref> --status todo` resets it.

## Claude Code skills

Skills live in `skills/` and are symlinked into `~/.claude/skills/` so they work from any project. Install with:

```bash
vteam skill install
```

See `skills/SETUP.md` for details.

### `/vteam` — context injection

A non-interactive skill that injects full vteam knowledge into the current Claude Code session. Invoke it with `/vteam` when working in a project that uses vteam — Claude will understand the framework's architecture, CLI commands, agent configuration, and design principles without needing to read the docs.

### `/create-vteam-agent` — agent scaffolding

An interactive skill that scaffolds new vteam agents. It asks clarifying questions about the agent's purpose, input/output modes, worktree needs, tool permissions, memory, and hooks — then generates `agent.json` and `AGENT.md` (and optionally `MEMORY.md` / `ON_FINISH.md`).

Invoke from any project with `/create-vteam-agent`.

## Development

```bash
just build          # go build -o dist/vteam ./cmd/vteam
just install        # go install ./cmd/vteam (into $(go env GOBIN), usually ~/go/bin)
just dev <args>     # go run ./cmd/vteam (e.g., just dev run improver)
just lint           # go vet + golangci-lint
just test           # go test -race ./...
just cover          # statement coverage
just clean          # rm -rf dist/
```

### Dogfooding

This project has its own `vteam/` directory pointing the agents at its own code. Run `just dev run improver` to have vteam review itself.

### Dependencies

vteam keeps external dependencies to a minimum: `cobra` for the CLI, `bubbletea` and `lipgloss` for the TUI, and `golang.org/x/term`, `x/sync`, `x/text`. Cron parsing, config validation, and slug generation are internal packages. Tests use the standard library plus `go-cmp` and `testscript`.

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
