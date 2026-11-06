# Interactive mode

An interactive run hands the constructed prompt to an interactive Claude Code session in a workmux worktree
instead of running `claude -p`. The user works with Claude, and the run is settled when Claude (or the user)
submits the `AgentOutput` back to vteam.

A run is interactive only when started with `vteam run <agent> --interactive` or with the interactive toggle in
the TUI run dialog. There is no global or per-agent setting, and cron runs are always headless.

## Flow

```
vteam run coder --interactive              (or TUI run dialog → interactive toggle)
  1. Hand-off (vteam run, exits when done)
     lock → claim task → create branch → workmux worktree (headless) → git prep
     → build prompt → open Claude in a background workmux window → save session → exit
  2. User works with Claude in the window
  3. Submit (`vteam submit`, invoked by Claude via /vteam-submit)
     validate → commit/push/PR/tasks → task done → print result
     → detached: on-finish hook, memory curation, `workmux remove`
  Window closed without submit → ensure-merge marks the session abandoned
     task → todo + note; worktree left for the user to clean up
```

Cron runs stay headless and can run alongside open sessions: a session does not hold the agent lock.

## Status

Implemented. Deviations from the plan below:
- `vteam status` reads only local state, so it lists open and abandoned sessions but not whether a window is
  still open; the merge ensurer is what checks windows.
- The submit command in the prompt carries `--project <dir>` so it works from any worktree; `submit` also finds
  the project via `git rev-parse --git-common-dir` and the session from the current directory when run by hand.
- A hand-off ends with `◆ <agent> handed off to an interactive session`, not `completed`.
- After the first manual test: inside tmux the window opens in the user's session and gets focus (`--parent-session`
  only outside tmux). Killing the window of the dedicated session ended it and detached the user, so finalize closes
  the window (`workmux remove`) only when it is in the user's own session; otherwise it removes the worktree and
  branch with git and leaves the window.

## Phase 0: workmux spike

1. `workmux add <existing-local-branch> --headless --json` checks out a branch vteam created.
2. `workmux open <handle> -P prompt.md` opens without switching the user's window (`open` has no `--background`).
3. `workmux open --config <tmp.yaml>` with `agent: "claude --append-system-prompt-file … --model …"` is honored
   (`open` has no `-a`).
4. Runs started outside tmux (TUI/CLI not inside tmux; `SpawnRun` inherits env, so `$TMUX` passes through when
   the TUI runs in tmux): does `--parent-session <name>` create the session?

Fallback if 2 or 3 fail: `workmux add <branch> --background -a "<claude cmd>"` with no prompt, then git prep,
then `workmux send <handle> -f prompt.md`.

### Results (workmux 0.1.271)

1. Works. The JSON receipt has `handle`, `branch`, `worktree_path`.
2. `open` *switches* to the window when it is in the caller's session (per docs). With `--parent-session <other>`
   it creates the window there and the user's client stays put. So sessions always open in a dedicated tmux
   session, `vteam-<project>`.
3. Works. `agent:` with flags (quoted paths with spaces OK); workmux appends `-- <prompt>`.
4. Works with `$TMUX` unset; `--parent-session` creates the session when missing.
5. `list --json` reports `is_open: false` once the window is killed; `remove --force <handle>` removes the
   worktree, branch, and window.

No fallback needed.

## Phase 1: output contract, multiple tasks

- `model.ContentTasks = "tasks"` with `Content.Tasks []TaskContentBody` (marshal/unmarshal). `output.Parse` /
  `validateContent` accept it (≥1 item).
- `applyOutput` creates one aven task per item; the on-finish hook gets the list as JSON.
- Headless prompts never advertise `"tasks"`, so goldens are unchanged.

## Phase 2: prompt

- `prompt.Input` gains `Interactive bool` and `SubmitCommand string`.
- Separate `interactiveOutputInstruction` (`outputInstruction` untouched): collaborate with the user; when asked
  to submit, pipe the JSON to `vteam submit --session <runId>` on stdin; fix and rerun on validation errors; do
  not `git commit`; task agents may return `"tasks"`.
- New goldens/tests for the interactive variants (task, generic, threads).

## Phase 3: new packages

**`internal/workmux`** — interface on `execx.Runner` + fake: `AddHeadless`, `Open` (or `AddBackground`/`Send`),
`Remove(handle)` (`--force`), `List()` (`list --json` → `{handle, path, branch, is_open}`).

**`internal/session`** — `vteam/.sessions/<runId>/` (own `.gitignore` `*`):
- `session.json`: runId, agent, handle, path, branch, `RemoteSHA`, `StartSHA`, task ref, `review`/`followUp`
  (incl. `BaseMerge` and threads), focus, startedAt, log path, `status: open | submitting | submitted | abandoned`
- `system-prompt.md`, `prompt.md`, `workmux.yaml`
- `Save`, `Load`, `List`, `FindByDir` (main checkout found via `git rev-parse --git-common-dir`)

**Worktree creation** — vteam creates the local branch, then `workmux add <branch> --headless`:
- New task: `git branch --no-track vteam/<slug>-<runId> origin/<base>`
- Follow-up / `input: "pr"`: `git branch vteam-session/<runId> origin/<prBranch>` (unique name, so it never
  clashes with an abandoned worktree holding the PR branch; `Push` already pushes `HEAD:refs/heads/<branch>`)
- Conflicting follow-up: `MergeBaseBranch` before the prompt is built
- Non-worktree agents get a scratch worktree; nothing is committed

## Phase 4: split `internal/run`

- `run.Options.Interactive`. `execute` → `runAgent` (headless) or `handOff`: write prompt + session files, build
  the Claude command (`claude.Args` minus `-p`, stream-json, `--json-schema`, `--no-session-persistence`), open the
  window, save session `open`, log `◆ session open`, exit without `finish` (task stays `active`, worktree stays).
- `run.Submit(ctx, d, sess, out)` rebuilds the `runner` from the session and reuses `applyOutput` + `finish`.
- Commit rule: commit uncommitted changes with `commitMessage`; publish whenever HEAD moved past `StartSHA` (covers
  manual commits). PR title from `commitMessage`, else the head commit subject.
- Submit failure (lease, JSON, aven): session stays `open`, task stays `active`, Claude retries.
  Blocked/failed/no changes → `recordNoChanges`, session closes.

## Phase 5: claims

- Interactive claims add `claimed-session=<runId>`.
- `ReleaseOrphaned` (`vteam clean`) and the status orphan list skip them.
- `vteam clean` treats session worktree paths as in use.

## Phase 6: CLI

- `vteam run <agent> --interactive` — fails early when `workmux` is not on PATH.
- `vteam submit [--session <runId>] [--file <f>]` — stdin default; refuses non-`open`; per-session lock
  `vteam/.locks/session-<runId>`; appends to the hand-off log.
- Hidden `vteam session finalize <runId>` (detached): on-finish hook, memory curation, `workmux remove --force`,
  status `submitted`.

## Phase 7: abandoned sweep in `ensure-merge`

- `Sessions` section: one `workmux list --json`; each `open` session whose window is gone → task `todo` (claim
  removed, retries unchanged), note `<agent>: abandoned` + worktree path, status `abandoned`. Worktree untouched.
- Skipped when there are no open sessions. `submit` refuses abandoned sessions.

## Phase 8: status and TUI

- `vteam status`: `Interactive sessions` section — open (window open/closed) and abandoned (with a
  `workmux remove <handle>` hint).
- TUI run dialog: `interactive` toggle, off by default, passes `--interactive` via `Deps.RunOnce` → `SpawnRun`.
  Not shown for the merge ensurer.
- Sidebar `◆ N` badge for open sessions; details list handles.

## Phase 9: skill, tests, docs

- `skills/vteam-submit/SKILL.md`; update the `vteam` knowledge skill.
- Tests: `run_test.go` (hand-off, submit with multiple tasks / manual commits / failed push keeps session open,
  finalize); unit tests for `session`, `workmux`, `output` `"tasks"`, the abandoned sweep; e2e txtar with a fake
  `workmux`.
- CLAUDE.md, README.md, project structure tree.

## Assumptions

- Abandoned → task `todo` without a `retry-count` bump; worktree stays until the user removes it.
- Session records stay in `vteam/.sessions/`; no pruning yet.
- The TUI toggle always starts off.
