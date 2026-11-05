# vteam — Virtual Development Team Framework

## What this project is

A Go CLI (`vteam`) that orchestrates AI agents powered by `claude -p` (Claude Code's headless mode) to autonomously review codebases and implement fixes. The framework manages a task lifecycle (inbox → todo → active → done) in the `aven` task manager, runs agents in isolated git worktrees, and maintains a shared memory file so stateless agent invocations don't duplicate work.

The end user's workflow:
1. Define agent prompts in `AGENT.md` files
2. Run improver — it scans the codebase and writes findings to inbox
3. Triage — move the findings worth fixing from inbox to todo in aven
4. Run coder (on a cron) — it picks up a task, marks it active, implements it in a worktree, creates a branch + PR, marks the task done

## Architecture

**Core principle: the orchestrator owns all state, Claude owns all intelligence.**

The Go orchestrator handles state transitions — creating worktrees, updating aven tasks, creating PRs. Claude (`-p` mode) handles reasoning — reading code, finding issues, implementing fixes. If Claude crashes mid-run, no state is corrupted because the orchestrator applies changes atomically after Claude finishes.

### How agents run

Each agent is invoked as a `claude -p` subprocess with:
- `--append-system-prompt-file` — the agent's `AGENT.md` content (via temp file)
- `--output-format stream-json` + `--verbose` — real-time streaming of tool calls; the runner accumulates `assistant` text events to extract the final result
- `--allowedTools` / `--disallowedTools` — per-agent tool permissions from `agent.json` (uses the same syntax as native Claude Code CLI flags)
- `--tools` — when `allowedTools` is set, the built-in tools it names (`Bash(git *)` → `Bash`; `mcp__*` entries skipped), so the agent has no other built-in tools. `--allowedTools` alone only pre-approves tools; it does not remove the rest (`claude.BuiltinTools`). MCP servers from the user's Claude Code config are not affected by `--tools`.
- `--json-schema` — agent runs only (not hooks or curation): the JSON Schema of the agent's `AgentOutput` (`output.Schema`), so Claude Code validates the final answer and returns it in the result event's `structured_output`
- `--no-session-persistence` — no session clutter

The orchestrator assembles a layered prompt: AGENT.md (role) → optional `--focus` context (priority section) → existing task titles (from aven) → task content (plus its aven notes, oldest first, under `## Task Notes`) or PR review comments → output format instructions. The prompt is passed via stdin. Claude returns structured JSON as its final output — the orchestrator parses this to create aven tasks, git commits, and PRs.

### Structured output contract

Claude's text output (extracted from the `result` field of the JSON envelope) must be valid JSON matching a single unified `AgentOutput` schema:

```
{ status, summary, content?, filesChanged?, commitMessage?, blockerReason?, memoryUpdate?, threadReplies? }
```

All fields except `status` and `summary` are optional. The `content` field is a discriminated union controlled by the `output` field in `agent.json`:

- **`output: "task"`** → `content: { type: "task", body: { title, severity, description, suggestedFix?, files } }` — the orchestrator creates an aven task from the finding.
- **Default (no `output` field)** → `content: { type: "generic", body: "string" }` — a freeform deliverable (review, analysis, report). The orchestrator passes it through to the ON_FINISH hook via `RunOutcome.content`.

The `filesChanged` and `commitMessage` fields are present only when the agent modified files. The orchestrator commits when `commitMessage` is present and `readOnly` is false.

The optional `memoryUpdate` field is included in the output format instructions only when the agent has a `MEMORY.md` strategy file. Agents without memory configuration never see this field.

The optional `threadReplies` field (`[{ thread, body }]`) is included only when the run is a follow-up on an existing PR with unresolved review threads. The prompt labels the threads `T1`, `T2`, …; Claude replies by label, and the orchestrator maps labels back to platform thread IDs, so Claude never handles real IDs.

The output format instructions are injected into every agent's user prompt by the prompt builder (`outputInstruction` in `internal/prompt`). `prompt.Build` also returns the matching JSON Schema (`Parts.OutputSchema`, from `output.Schema`), which offers `memoryUpdate` and `threadReplies` under the same conditions; the runner passes it as `--json-schema`. The orchestrator parses `structured_output` when present and falls back to the result text otherwise. It validates the output with `output.Parse` (`internal/output`). Unknown keys are ignored, `null` values are rejected. Markdown fences are stripped before parsing as a fallback.

### Memory management

Each `claude -p` call is stateless. Memory is external:

- **Tasks** — stored in aven (local-first SQLite task manager, `aven` CLI must be on PATH). Scoped to the configured `tasks.project` and `tasks.label`; tasks without the label are invisible to vteam. The orchestrator reads tasks via the `TaskManager` interface at prompt-build time and injects a summary of existing task titles, severities, and statuses into every agent's prompt.
- **Deduplication** — The prompt builder calls `taskManager.getIndex()` and includes every non-deleted labelled task, of every aven status (including `canceled`), in the "Existing Tasks" section. Claude avoids reporting duplicates, so canceling a task in aven acts as "rejected — don't report again". No hashing. aven is the single source of truth.
- **Per-agent memory** — agents can optionally have a `MEMORY.md` strategy file at `vteam/agents/<name>/MEMORY.md`. When present, the orchestrator injects the agent's accumulated memory (from `vteam/.memory/<agent-name>/store.md`) into the prompt. After the main run, if the agent returned a `memoryUpdate` in its structured output, the orchestrator spawns a small curation agent. The curation agent receives the MEMORY.md instructions (system prompt), the current memory content, and the new update — it returns the complete replacement memory content. The orchestrator writes the result back to `vteam/.memory/<agent-name>/store.md`. Memory files are local-only and gitignored. Curation failure is non-fatal.

### Worktrees

Agents with `worktree: true` get an isolated git worktree (`git worktree add`). `CreateWorktree` fetches `baseBranch` and branches from `origin/<baseBranch>` (not the local branch, which may be stale or hold unpushed commits). Task runs use the branch `vteam/<slugified-title>-<runId>` — aven refs are local and never appear in branch names, commits, or PRs. Claude edits files in the worktree but does not commit — it returns a structured JSON output with a commit message. The orchestrator then runs `git add -A` + `git commit`, pushes the branch, optionally creates a PR (if `autoPR: true`), and cleans up the worktree. A PR opened by a run without a task (e.g. test-writer) gets a receipt task (`recordPRTask` in `internal/run`): created with the commit subject as title, the commit body as description, severity `low`, `found-by=<agent>`, then set straight to `done` with `completed`/`branch`/`pr-url`, the agent's done label, and a run note. This keeps the invariant that every vteam PR is backed by a task, which the merge ensurer relies on. A failure to record it is logged and does not fail the run. The coder template's `taskFilter` includes `test-writer` so reopened test PRs get picked, and `security-analyst` so its findings get implemented.

`Push` pushes with `--force-with-lease=refs/heads/<branch>:<remoteSha>`. `git.Session`'s remote SHA is `""` for `CreateWorktree` (the branch must not exist on the remote) and the fetched `origin/<branch>` sha for `CheckoutWorktree`, so commits someone else pushed to a PR branch during the run are never overwritten — the push fails and the task returns to `todo`. Transient network errors (`IsTransientPushError`) are retried after 5s and 15s. Before each retry, `git ls-remote` checks whether the dropped push already landed (the lease would reject a re-push of it).

Follow-ups on a conflicting PR merge the PR's base branch into the worktree first (`MergeBaseBranch`, see Task manager → Lifecycle step 5).

Agents with `input: "pr"` use `CheckoutWorktree` to check out an existing PR branch (via `git fetch` + `git worktree add` from the remote tracking branch). Claude edits files and returns a commit message. The orchestrator commits, pushes to the same branch, posts a summary comment on the PR, and removes the `prTriggerLabel`. Discovery is label-based: the orchestrator searches for open PRs matching the `prTriggerLabel`. The user adds the trigger label when they want changes; the orchestrator removes it after the agent pushes. This avoids GitHub's limitation where PR authors cannot submit "Request changes" reviews on their own PRs.

### Agent configuration

Each agent is a directory `vteam/agents/<name>/`. `AGENT.md` (required — its presence defines the agent) is the system prompt, plain markdown. All orchestrator behavior lives in an optional `agent.json` next to it:

```json
{
  "$schema": "../../agent.schema.json",
  "model": "sonnet",
  "cron": "0 */6 * * *",
  "worktree": true,
  "input": "task",
  "autoPR": true,
  "prCreateLabels": ["vteam"],
  "memory": { "model": "haiku" },
  "onFinish": { "model": "haiku", "allowedTools": ["mcp__slack__send_message"] }
}
```

- `worktree` (default: `false`) — run in an isolated git worktree, push branch on commit
- `readOnly` (default: `false`) — run in a worktree but skip commit/push/PR (requires `worktree: true`, incompatible with `autoPR: true`). The agent runs freely in the worktree — `readOnly` only prevents the orchestrator from committing and pushing afterward.
- `output` (optional, `"task"`) — controls the `content` type in the unified `AgentOutput` schema. When `"task"`, the prompt instructs Claude to return `content: { type: "task", body: { title, severity, ... } }` and the orchestrator creates an aven task. When omitted, the prompt instructs `content: { type: "generic", body: "string" }`. Independent of `worktree`. Incompatible with `autoPR: true` and `readOnly: true`.
- `input` (optional, `"task"` or `"pr"`) — `"task"`: pick a ready task routed to this agent (see Task manager → routing), manage task lifecycle; `"pr"`: pick a PR with pending review feedback, check out its branch (requires `worktree: true`)
- `taskFilter` (optional, requires `input: "task"`) — map of aven metadata key → string, `null` (key missing/empty), or a non-empty list of those. Keys AND, list values OR. E.g. `{ "found-by": ["improver", "security-analyst"] }` or `{ "actor": "mr-reviewer" }`. Omitted = matches every task.
- `prTriggerLabel` — label that signals "this PR needs work" (e.g. `vteam:changes-requested`); the orchestrator discovers PRs by this label and removes it after the agent pushes
- `autoPR` (default: `false`) — create a pull request after pushing
- `cron` — default cron expression (5 fields: minute hour day month weekday). It does not schedule the agent by itself: crons are opt-in per agent (TUI or `vteam cron enable`), and this pattern is only what the TUI dialog and `vteam cron enable` start from
- `model` — Claude model override passed to `claude --model` (alias or full model ID); omit to use Claude Code's default
- `prCreateLabels` — labels applied to created PRs
- `resolveThreads` (default: `false`, requires `input: "task"` and `worktree: true`) — resolve each review thread the agent replied to on a PR follow-up
- `allowedTools` — Claude Code tools the agent may use (same syntax as the `--allowedTools` CLI flag, e.g. `["Read", "Bash(git *)"]`). When set, it is also the agent's whole built-in toolset (passed as `--tools`); an allowlist of only `mcp__*` tools leaves no built-in tools. Omit it for Claude Code's default toolset.
- `disallowedTools` — Claude Code tools the agent may NOT use (same syntax as `--disallowedTools`)
- `memory` / `onFinish` — `{ model?, allowedTools?, disallowedTools? }` for the memory curation agent and the on-finish hook. The `MEMORY.md` / `ON_FINISH.md` file enables the feature; the section only configures it, and a section without its markdown file is a load error.
- `$schema` — ignored by vteam; points editors at the JSON Schema

`config.ResolveAgent` (`internal/config/agent.go`) validates `agent.json` on load with `ParseAgentConfig`, a hand-written validator in `internal/config/check.go` whose messages match the old zod ones. It is strict: unknown keys are rejected at every level, and cross-field rules (e.g. `readOnly` requires `worktree`) are extra checks in the validator. A missing `agent.json` means all defaults. Any `AGENT.md`, `MEMORY.md`, or `ON_FINISH.md` that starts with `---` is a load error — YAML frontmatter is no longer supported and must be moved to `agent.json` by hand.

`vteam init` writes `vteam/agent.schema.json` (embedded from `internal/config/agent.schema.json`) for editor autocomplete and validation. The JSON Schema cannot express the cross-field rules; only vteam enforces those. It is maintained by hand: when `agent.json` fields change, update it and the validator together. `internal/config/config_test.go` checks that its keys match the validator's and that the dogfood copy at `vteam/agent.schema.json` is identical. `vteam.config.json` contains global settings (baseBranch, platform, worktreeDir, tasks, mergeEnsurer). Add custom agents by creating `vteam/agents/<name>/AGENT.md` — no config changes needed.

### Task manager

All task operations go through the `tasks.Manager` interface (`internal/tasks/tasks.go`: `ListReady`, `ListDone`, `GetIndex`, `Create`, `Update`, `AddNote`). The only implementation is `tasks.Aven` (`internal/tasks/aven.go`), which shells out to the `aven` CLI — it never touches aven's SQLite file directly. Configuration lives under `tasks` in `vteam.config.json`:

```json
{
  "tasks": { "maxRetries": 3, "project": "my-app", "label": "vteam", "workspace": "optional" }
}
```

- `tasks.project` (required) — aven project; aven creates it on the first `add`. `vteam init` defaults it to the directory name.
- `tasks.label` (default `"vteam"`) — opt-in label. vteam only lists, picks, and dedups against tasks with this label, and creates the label on demand. Humans can queue their own aven tasks for the coder by adding the label.

Agent labels are hardcoded conventions, all defined in `internal/tasks/labels.go`: a created task gets `<tasks.label>-<creating agent>` (e.g. `vteam-improver`), and a task set to `done` gets `<tasks.label>-<finishing agent>` (e.g. `vteam-coder`). They are never removed, so they stay when a task is reopened. `ToAvenLabel` mirrors aven's label normalization (lowercase, runs of non-`[a-z0-9]` → `-`, trimmed) so the label vteam writes is the one aven stores. Each label is created (`aven label create`) before use, because `aven edit --label` rejects unknown labels.
- `tasks.workspace` (optional) — passed as `--workspace` to every aven call.
- `mergeEnsurer.cron` (optional) — default cron pattern for `vteam ensure-merge` (job name `merge-ensurer`), used when its cron is enabled.
- `mergeEnsurer.label` (default `"vteam:changes-requested"`) — PR label a human adds to approve the review feedback; see Merge ensurer.

Routing (`MatchesFilter` in `tasks.go`): `ListReady` returns every ready labelled task with its metadata; `run` keeps the tasks matching the running agent's `taskFilter` (no filter = all). There is no fallback agent — a task no filter matches is never picked. Overlaps are allowed — picking runs under a global claim lock (see Lifecycle), so exactly one run gets each task. vteam only writes `found-by`; humans set other keys such as `actor` via `aven edit <ref> --metadata actor=<agent>`. `PickNext` (also in `tasks.go`) applies the filter, the retry limit, and the severity/age ordering; `run` and `vteam status` both use it.

Mapping: severity `critical/high/medium/low` ↔ aven priority `urgent/high/medium/low` (priority `none` reads as `low`). `found-by`, `retry-count`, `completed`, `branch`, `pr-url`, `merged`, `claimed-by`, `claimed-pid`, `claimed-host` are aven metadata (string values). The finding's description, suggested fix, and affected files become the aven description; affected files are not stored as metadata.

Tasks created from `output: "task"` findings start in `inbox` and are only picked once a human moves them to `todo`. aven promotes inbox tasks with priority medium or higher to `todo` on `add`, so `Create` follows up with `aven edit <ref> --status inbox`.

Lifecycle for `input: "task"` agents:
1. Under the global claim lock `vteam/.locks/task-claim` (shared by all agents, held only while picking): `aven list --status todo --ready` (respects aven dependencies and `available_at`), keep tasks routed to this agent, drop tasks with `retry-count >= maxRetries`, sort by severity then oldest (`PickNext`) — pick the first and set it `active` with claim metadata `claimed-by` (runId), `claimed-pid`, `claimed-host`. Once `active`, the task drops out of every other agent's `ListReady`. aven has no compare-and-swap, so the lock is what prevents two agents claiming the same task; it only works on one machine.
2. Commit pushed → `done` with `completed`/`branch`/`pr-url` metadata and the agent's done label.
3. Worktree agent produced no changes → back to `todo` with `retry-count` incremented.
4. Any other ending (error, crash, readOnly or non-worktree agent) → back to `todo` in `finally`, retries unchanged.
5. Follow-up on an existing PR: when a worktree agent picks a task that already has `pr-url` metadata (typically a `done` task a human moved back to `todo`, with a note saying which review comments to apply), the orchestrator looks the PR up (`PullRequest`). If it is merged or closed, the task goes to `backlog` with a note and the agent does not run (remove `pr-url` to start over on a new branch). If it is open, the orchestrator checks out the PR branch with `CheckoutWorktree`, adds an `## Existing Pull Request` section with the PR's unresolved review threads (`OpenReviewThreads`; resolved threads and top-level comments are left out) to the prompt, pushes to that branch, skips `autoPR`, and settles the task as in steps 2–4 with `pr-url` kept. After the push, the orchestrator posts each of the agent's `threadReplies` to its thread (`ReplyToThread`) as `🤖 **vteam <agent>:** <body>` followed by `Fixed in <sha>`, and resolves the thread (`ResolveThread`) when the agent has `resolveThreads: true`. Replies to unknown labels are skipped, replies are dropped when nothing was pushed, and failed replies are logged without failing the run. The run note records `Replied to N of M review threads.` Threads Claude skipped get no reply. If the PR's `mergeable` is `conflicting`, the orchestrator runs `MergeBaseBranch` (fetch + `git merge --no-edit origin/<pr base>`) in the worktree before building the prompt and adds a `## Merge Conflicts` section listing the conflicted files. On commit, MERGE_HEAD makes `git commit` produce the merge commit with Claude's message. If `FindUnresolvedConflicts` still finds `<<<<<<<`/`>>>>>>>` markers in those files, nothing is committed, the run counts as "no changes" (step 3), and the note records the files. A clean merge is pushed even if Claude made no other changes.
6. Every transition out of `active` removes the claim metadata. A hard-killed process can leave a task `active`; `vteam clean` returns `active` tasks to `todo` when their `claimed-host` is this machine and `claimed-pid` is dead. Tasks claimed on another host, or `active` without a claim (e.g. set by a human), are left alone.

### Merge ensurer

`vteam ensure-merge` (`internal/ensuremerge`) is a deterministic sweep, not a `claude -p` agent. A `done` task only means its PR is open; the ensurer moves it through review. It only sees PRs that have a task, so every PR vteam opens must have one (see Worktrees). Under `vteam/.locks/merge-ensurer`, it calls `ListDone` and, for each task with `pr-url` and without `merged=true` metadata, calls `PullRequest`:

- merged → metadata `merged=true` (stays `done`, never checked again)
- closed → `canceled` + note (canceled stays in dedup, so the finding is not re-reported)
- open with the `mergeEnsurer.label` PR label (default `vteam:changes-requested`) → `RemoveLabel` **first**, then `todo` with `retry-count` removed, then a note. If label removal throws, the task stays `done` (a left-over label would otherwise reopen the task after every push).
- open and `mergeable: "conflicting"` → `todo` + note; the coder resolves the conflict (see step 5)
- otherwise (incl. `mergeable: "unknown"`) → nothing. Review comments alone never reopen a task; the label is the human approval.

Per-task failures are logged and the sweep continues. Its output follows the `vteam run` style: a `Setup` section, a `Pull requests` section with one headline per task plus `PR`/`state`/`action` fields, and a `Result` section with the counts. It runs on a schedule once its cron is enabled like an agent's (job name `merge-ensurer`, default pattern `mergeEnsurer.cron`); logs go to `vteam/.logs/merge-ensurer/`.

Whenever the agent returned output (every ending except an error or crash), the orchestrator appends an aven note to the task: `<agent>: <status>`, the agent's `summary`, `Blocker: <blockerReason>` if present, and `PR: <url>` if one was created. A failed note write is logged and does not fail the run. Notes (from runs and from humans) are injected into the prompt the next time an agent picks the task, so retries see why earlier attempts failed.

`GetIndex` and `ListReady` cost one `aven show --full --json` per task (8 concurrent), because `aven list --json` omits description, metadata, and notes. `unknown-project`/`unknown-label` errors from `aven list` are treated as an empty list.

### On-finish hooks

An agent can optionally have an `ON_FINISH.md` file at `vteam/agents/<name>/ON_FINISH.md`. When present, the orchestrator spawns a second `claude -p` call after the agent run completes (both success and failure). The hook receives a structured summary of the run outcome (status, branch, PR URL, task info, content, error) as its user prompt. When the agent returned `content` in its output, the hook receives it in a `## Content` section — generic content as a string, task content as JSON. This allows hooks to act on the agent's primary deliverable (e.g. post a PR review as a comment).

The ON_FINISH.md content is the hook's system prompt. Its `model`, `allowedTools`, and `disallowedTools` go in the `onFinish` section of `agent.json`. The hook runs in the main project directory (not the worktree) and its failure does not affect the agent run's exit status.

### Per-agent memory

An agent can optionally have a `MEMORY.md` file at `vteam/agents/<name>/MEMORY.md`. This defines the memory curation strategy — how accumulated memory should be maintained across runs.

The MEMORY.md content is the curation agent's system prompt, e.g.:

```markdown
Keep a running list of areas scanned. Record false positives to avoid repeating them.
Maximum 30 lines — drop oldest entries first.
```

Its `model`, `allowedTools`, and `disallowedTools` go in the `memory` section of `agent.json`. Memory data is stored in `vteam/.memory/<agent-name>/store.md` (gitignored). The curation agent runs after each agent run and its failure does not affect the agent run's exit status.

## Claude Code skills

Skills live in `skills/` and are symlinked into `~/.claude/skills/` via `vteam skill install`. See `skills/SETUP.md` for setup.

### `/vteam` — context injection

A non-interactive skill (`skills/vteam/SKILL.md`) that injects full vteam knowledge into the current Claude Code session. No questions, no output — it loads background context so Claude can assist with vteam configuration, agent authoring, and troubleshooting from any project.

### `/create-vteam-agent` — agent scaffolding

An interactive skill (`skills/create-vteam-agent/SKILL.md`) that scaffolds new vteam agents. It walks through clarifying questions (input mode, output mode, worktree, tools, memory, hooks, model, cron) and generates `agent.json` + `AGENT.md` + optional `MEMORY.md` / `ON_FINISH.md` files.

**The skill must stay in sync with the codebase.** When any of the following change, update the skill accordingly:
- `agent.json` fields (added, removed, renamed, new validation rules) in `internal/config`
- The `AgentOutput` type or `content` union in `internal/model` and `internal/output`
- How prompts are assembled in `internal/prompt`
- `memory` / `onFinish` section fields
- New agent capabilities or orchestrator behaviors that affect what questions the skill should ask

## Project structure

```
skills/
├── vteam/
│   └── SKILL.md                  Context injection skill — loads vteam knowledge into any session
└── create-vteam-agent/
    └── SKILL.md                  Interactive skill for scaffolding agents
cmd/vteam/
├── main.go                       Entry point; calls cli.Main
├── main_test.go                  testscript e2e harness (fake claude/gh, real aven on a temp db)
└── testdata/script/*.txtar       e2e scripts: init, errors, failed run, full lifecycle
internal/
├── cli/                          Cobra commands; wires real dependencies into the packages below
├── model/                        Shared types (tasks, AgentOutput, Content union, PRs, run outcome)
├── ansi/                         ANSI painters per palette role (TTY / NO_COLOR / FORCE_COLOR aware) and Strip
├── palette/                      Color roles (brand, muted, ok, warn, …) → ANSI number or hex; the one place to restyle the CLI and TUI
├── log/                          Run output formatting: Logger (sections, elapsed-time gutter, glyphs, tool lines) and Format* helpers
├── runlog/                       Per-run log file under vteam/.logs/<agent>/ (ANSI-stripped copy of the Logger output)
├── runstate/                     Per-run state JSON under vteam/.runs/; live-run discovery from agent locks
├── lock/                         Advisory file locking (atomic mkdir, stale detection)
├── execx/                        Runner interface for every subprocess (aven, git, gh, glab) + Fake for tests
├── config/                       vteam.config.json + agent.json validation, agent resolution, embedded agent.schema.json
├── cron/                         5-field cron parser and next-fire computation
├── output/                       Parses Claude's AgentOutput (fence stripping, JSON extraction)
├── prompt/                       Assembles layered prompts + output format instructions
├── claude/                       Spawns claude -p, renders stream-json, tool-line descriptions
├── tasks/                        Manager interface, labels, claims, routing/pick order, Aven implementation
│   └── taskstest/                In-memory Manager fake
├── git/                          Git worktree create/checkout/merge/push/remove/cleanup
│   └── gittest/                  Temp-repo helpers for tests
├── platform/                     Platform interface; GitHub (gh) and GitLab (glab) PRs, threads, labels
├── run/                          vteam run <agent> — main orchestration flow on injected Deps
├── ensuremerge/                  vteam ensure-merge — record merges, cancel closed PRs, reopen labelled/conflicting PR tasks
├── schedule/                     vteam cron — enabled-cron state, long-lived scheduler, detached scheduler/run spawning, Scheduler section
├── status/                       vteam status — running agents, scheduler, next picks, tasks needing attention
├── tui/                          vteam (no args) — bubbletea/lipgloss TUI: Overview, Agents (log viewer, run/cron actions), Info, About tabs
├── clean/                        vteam clean — remove unused worktrees, stale locks, orphaned active tasks
├── scaffold/                     vteam init — scaffold vteam/ in any project
│   └── templates/                Embedded templates: <agent>.agent.json/.agent.md/.memory.md for the four default agents, vteam.config.json
├── skill/                        vteam skill install/uninstall — manage Claude Code skill symlinks
├── slugify/
└── golden/                       Loads testdata/golden/*.json in tests
testdata/
├── golden/                       Frozen goldens captured from the old TypeScript implementation
└── claude/                       stream-json fixtures replayed by the claude runner tests
```

## Commands

```
just build          # go build -o dist/vteam ./cmd/vteam
just install        # go install ./cmd/vteam (into $(go env GOBIN), usually ~/go/bin)
just dev <args>     # go run ./cmd/vteam (e.g. just dev run improver)
just lint           # go vet + golangci-lint
just test           # go test -race ./...
just cover          # statement coverage
just clean          # rm -rf dist/
```

### CLI commands

```
vteam                                   # interactive TUI (Overview / Agents / Info / About); prints help outside a terminal or a vteam project
vteam init                              # scaffold vteam/ directory
vteam run                               # list available agents (hidden from --help)
vteam run <agent>                       # run a specific agent (hidden from --help)
vteam run <agent> --focus "..."         # run with priority context injected into the prompt
vteam status                            # show running agents, scheduler state, next picks, tasks needing attention
vteam clean                             # remove worktrees no running agent uses, break stale locks, release orphaned active tasks
vteam ensure-merge                      # record merged PRs, cancel closed ones, reopen labelled or conflicting PR tasks (hidden from --help)
vteam cron enable <agent> [expr]        # enable an agent's (or merge-ensurer's) cron; expr defaults to agent.json's cron
vteam cron disable <agent>              # disable it
vteam cron start                        # start the long-lived scheduler for the enabled crons (foreground)
vteam skill install                     # symlink skills from skills/ into ~/.claude/skills/
vteam skill uninstall                   # remove skill symlinks from ~/.claude/skills/
```

`run` and `ensure-merge` are `Hidden` cobra commands: the TUI is the front door, but the scheduler and the TUI spawn them as subprocesses (`schedule.JobCommand`, `schedule.SpawnRun`), and scripts, system cron, and the e2e tests call them directly, so they must keep working. `vteam status` also covers what `cron status` used to show (the `Scheduler` section).

Crons are opt-in per agent. The enabled ones are local state in `vteam/.cron/schedules.json` (`{"jobs": {"<name>": {"cron": "<expr>"}}}`, written atomically by `schedule.Enable`/`Disable`). The directory carries its own `.gitignore` (`*`), so projects scaffolded before it existed don't commit it. The `cron` in `agent.json` and `mergeEnsurer.cron` are only default patterns (`schedule.DefaultCron`). The job name `merge-ensurer` runs `vteam ensure-merge`; any other name runs `vteam run <name>` (`schedule.JobCommand`). `FindJobs` returns the enabled jobs whose agent still loads.

`vteam cron start` runs a foreground process that schedules the enabled crons (parsed by `internal/cron`: 5 fields, ranges, steps, lists, month and weekday names, `7` = Sunday, day-of-month and day-of-week OR'ed when both are set; `L` and `#` are not supported). It refuses to start when none are enabled. It rereads `schedules.json` on every wake, at least every `PollInterval` (2s), and logs `cron enabled`/`changed`/`disabled` in its Activity section; a new or changed cron counts its next fire from the moment it was picked up. When no cron is enabled and no child is running it logs `no crons enabled, stopping` and exits, so the scheduler the TUI started does not outlive the last cron (this applies to a foreground one too). Each agent run spawns a subprocess (`vteam run <agent>`). If an agent is still running when its next cron tick fires, the tick is skipped. The scheduler discards the child's stdout/stderr because the child writes its own log; it picks that log's path itself and passes it in the `VTEAM_RUN_LOG` env var (honored by `runlog.Start`), so it can print the exact file. Its output uses `internal/log`: a banner, the same `Scheduler` section as `vteam status` (`RenderScheduler`), then an `Activity` section with a wall-clock gutter (`UseWallClock`) logging each job start (with its log path), finish, failure, and skipped tick. It writes its pid to `vteam/.locks/cron.pid` and refuses to start while that pid is alive; the file is removed when it exits. Stop with Ctrl+C, which SIGTERMs running children (stopping because no crons are left waits for them instead). `RenderScheduler` shows `running`, `not running, crons paused` (enabled crons, no scheduler), or `not running` plus `no crons enabled`.

`schedule.StartDetached` starts `vteam cron start` in its own session (`Setsid`) with output appended to `vteam/.cron/scheduler.log`, and `schedule.SpawnRun` starts one `vteam run <agent>` / `vteam ensure-merge` the same way with `VTEAM_RUN_LOG` set. The TUI uses both, so its runs and scheduler keep going after it quits. Default patterns are written by `config.SetAgentCron` / `SetMergeEnsurerCron` (`internal/config/write.go`), which keep key order and rewrite the file with two-space indentation (creating `agent.json` when missing).

`vteam status` (`internal/status`) reads only local state plus one `GetIndex` and one `ListReady`: `runstate.FindLive` turns live agent locks in `vteam/.locks/` (except `task-claim`) into running jobs, each with its newest `vteam/.runs/` state started before the lock (`runstate.FindLatest`) for the worktree path; status matches them to `active` tasks by `claimed-pid`/`claimed-host`; `cron.pid` gives the scheduler state; `PickNext` per `input: "task"` agent gives the next pick. It also lists `done` tasks with an unmerged `pr-url`, plus inbox, unroutable, retry-exhausted, orphaned `active` tasks, and worktrees under `worktreeDir` no running job uses (`git.FindOrphaned`, which compares real paths). `vteam clean` removes exactly those worktrees (`git.CleanupOrphaned`), so it is safe to run while agents work.

`vteam` with no arguments opens the TUI (`internal/tui`, bubbletea v2 + lipgloss v2) on the alternate screen when stdin and stdout are terminals and `vteam/` exists; otherwise it prints help (plus a `vteam init` hint when `vteam/` is missing). It is keyboard only: `1`-`4` switch tabs, `j`/`k`, `ctrl+d`/`ctrl+u`, `gg`/`G` scroll, `ctrl+r` refreshes, `?` toggles a help overlay, `q`/`ctrl+c` quit. The header shows the tabs and, top right, the aven project (`tasks.project`, or the directory name when the config fails to load), the task label, and the git branch.

- **Overview** — `status.Render` output without its banner, refreshed every 2s (a refresh starts only after the previous one finished; a failed refresh keeps the last frame and shows the error in the footer).
- **Agents** — a sidebar of every agent, the merge ensurer, and any other name with a `vteam/.logs/<name>/` directory (`tui.LoadAgents`), showing live runs from agent locks and each agent's details. The log pane shows the selected agent's newest run log, re-read every 500ms when its size or mtime changes, recolored by line glyph and wrapped with a hanging indent (`w` toggles wrap). It follows the end of the log until you scroll up; `G` or scrolling to the bottom resumes. `p`/`n` step to the previous/next run, and the title's `run i/n` turns yellow on an older run. `l`/`enter` focus the log, `h`/`esc` the sidebar, and `tab`/`shift+tab` toggle between them. `space`/`a` opens an action menu for the selected agent (`internal/tui/actions.go`), and each item also has its own key. `r` opens the run dialog: a multiline focus field (bubbles `textarea`; `⏎` runs, `shift+⏎`/`alt+⏎`/`ctrl+j` adds a line, pastes are accepted) whose trimmed text is passed as `--focus` (`Deps.RunOnce` → `schedule.SpawnRun`); empty means a normal run. The merge ensurer takes no focus, so for it the dialog is a plain confirmation. `c` opens the cron dialog, prefilled with the enabled pattern, else the default, else `0 * * * *`. The dialog uses a readline-style input (`input.go`) and shows the pattern in words (`describeCron`), the next 3 fires, or the parse error; `tab` toggles saving it as the default. `⏎` enables it (`Deps.EnableCron`, plus `Deps.SaveCron` when saving), then starts a detached scheduler (`Deps.StartScheduler`) if none runs. `s` stops the cron. `S` starts the scheduler when crons are paused. An agent with an enabled cron shows `◷` in the sidebar, `cron`/`next` in its details, and a line above the log. All three turn yellow "paused" when no scheduler runs. Results and errors show as a 5s toast in the footer, which also counts `◷ N on cron` / `◷ N paused`. While a dialog is open it gets every key except `ctrl+c`.
- **Info** — the project, `vteam.config.json`, scheduler state and enabled-cron count, and each agent's `agent.json` settings (model, input/output, enabled cron and next run or `off` with its default, flags, `taskFilter`, allowed/denied tools).
- **About** — vteam's description, its three principles (the codebase improves unattended, agents keep learning, humans intervene at triage and code review), the author, and the license (`internal/tui/about.go`).

`status` builds its output as lines with the `Format*` helpers in `internal/log`, and the TUI's Overview shows the same lines, so they look the same as `vteam run` output.

Every `vteam run <agent>` writes a log file for that run at `vteam/.logs/<agent>/<ISO-timestamp>.log` (`internal/runlog`), whether it was started by hand or by the scheduler. Every line the run's `log.Logger` prints is also written to the file, ANSI colors stripped, including the final error. `vteam run` output goes through `internal/log`: sections (`Setup`, `Agent: <name>`, `Result`, `On-finish hook`, `Memory curation`, `Cleanup`), an elapsed-time gutter, and glyphs per line kind (`•` step, `❯` Claude text, `⚙` tool call, `✓`/`!`/`✗`). `internal/claude` renders tool calls (`DescribeTool`), failed tool results, and an exit line with duration, turns, and `total_cost_usd`. In a TTY, `Logger.ToolCall` cuts tool inputs to one line on the terminal and writes the full input to the log file. Logging starts after the agent is resolved, so an unknown agent name creates no file.

## Before submitting changes

All three must pass before any commit or PR:

1. **`just test`** — all Go tests must pass (with `-race`)
2. **`just lint`** — no `go vet` or golangci-lint issues
3. **`just build`** — clean compilation to `dist/vteam`

## v1 scope and constraints

- Ships with four default agents (`improver`, `security-analyst`, `coder`, `test-writer`). Custom agents supported by creating `vteam/agents/<name>/AGENT.md`.
- Supports both GitHub (`gh`) and GitLab (`glab`) — configured via `platform` in `vteam.config.json`.
- No Slack integration yet.
- No `--max-budget-usd` caps on agent runs.
- Requires the `aven` CLI on PATH for task management.
- Go 1.27+ (see `go.mod`), single binary. Installed with `go install github.com/leodnlv/vteam/cmd/vteam@latest`.
- Templates live in `internal/scaffold/templates/` and are embedded with `go:embed`.

## Conventions

- Self-documenting code, no comments unless the why is non-obvious.
- Terminal output of every CLI command uses the helpers in `internal/log` (banner, sections, gutter, glyphs, fields). The conventions are in the project skill `.claude/skills/cli-output-style/SKILL.md`; load it before adding a command or changing what one prints. Colors are picked by role from `internal/palette`, never by hue.
- Task refs (e.g. `APP-7KQ9`) are local to the user's aven database. Never put them in branch names, commit messages, or PR bodies.
- Locking uses atomic `mkdir`. A lock is stale only when its holder pid is dead (or it has no `info.json` and is older than 30 min); age alone never breaks a live lock. Release only removes a lock the current process owns.
- Tasks live in the user's aven database, not the repo. The real shared artifacts are PRs.
- All agents return a unified `AgentOutput` JSON. The orchestrator handles all state mutations based on the output fields: creates aven tasks from `content.type: "task"`, commits from `commitMessage` + `filesChanged`, and passes `content` through to ON_FINISH hooks.
- No module mocking: code that talks to the outside world takes an interface (`execx.Runner`, `tasks.Manager`, `run.Worktrees`, `platform.Platform`, the claude runner) and tests pass fakes. `run.Deps` bundles them for the orchestrator.
- `internal/run/run_test.go` is the integration test for the agent → ON_FINISH → MEMORY data flow and the task lifecycle (it uses the in-memory `taskstest.Fake`). When adding new hooks, output fields, or changing how agent output is routed to downstream stages, update this test.
- `internal/tasks/aven_test.go` includes a round-trip test against the real `aven` binary using a temp `--db`, and the e2e scripts in `cmd/vteam/testdata/script/` run the built CLI against real `aven` and `git`. Both are skipped when `aven` is not installed.
- `testdata/golden/*.json` pin byte-exact prompts, log formatting, validation messages, and cron results from the old TypeScript implementation. Treat them as frozen; when behavior changes on purpose, edit the golden and the code together.

## Keeping CLAUDE.md and README.md current

This file is the primary source of truth for how Claude understands vteam. `README.md` is the public-facing documentation for users. When you make changes that alter vteam's behavior, update the relevant sections of both files in the same commit. Specifically:

- **New or removed CLI commands** — update Commands and project structure
- **New or changed `agent.json` fields** — update Agent configuration
- **Changes to prompt assembly, agent invocation flags, or orchestrator flow** — update Architecture subsections
- **New or changed config options in `vteam.config.json`** — update Agent configuration or v1 scope
- **New conventions (file naming, locking, task lifecycle)** — update Conventions
- **New integrations or platform support** — update v1 scope and constraints
- **Added, moved, or deleted source files** — update Project structure tree

- **Changes to `agent.json` fields, output schema, or orchestrator behavior** — update the `create-vteam-agent` skill (`skills/create-vteam-agent/SKILL.md`) so it generates correct agents, and the `vteam` knowledge skill (`skills/vteam/SKILL.md`) so its context stays accurate

Do not update CLAUDE.md or README.md for internal refactors that don't change external behavior, test additions, or bug fixes that don't alter documented behavior.

## Dogfooding

This project has a `vteam/` directory that points the improver and coder at its own code. Run `just dev run improver` to have vteam review itself.
