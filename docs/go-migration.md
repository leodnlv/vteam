# Go migration plan

Port vteam from TypeScript/Node to Go. Breaking changes are allowed. Test coverage must stay at least as good as it is now.

**Status: done.** The TypeScript code was deleted at cutover. This file is kept as a record of the decisions; the git history before the cutover commit has the TS sources.

## Decisions

- Module: `github.com/leodnlv/vteam`, Go 1.27 (`go.mod`).
- Go code lives at the repo root next to `src/` until cutover. Then `src/` and the npm files are deleted.
- CLI: `spf13/cobra`.
- Dashboard: `charmbracelet/bubbletea`, because more interactive TUI features are planned.
- `vteam skill install` keeps symlinking from the `skills/` directory of a checkout.
- Tests: stdlib `testing` + `google/go-cmp` + `rogpeppe/go-internal/testscript`. No testify.
- Distribution: `go install github.com/leodnlv/vteam/cmd/vteam@latest`. npm is dropped.

## Phase 0 findings

- **Cron:** croner accepts `L` (last day of month) and `n#k` (k-th weekday). Decision: drop both. The Go parser rejects them, which is a breaking change. Everything else in `testdata/golden/cron.json` must match, including OR semantics when both day-of-month and day-of-week are set.
- **AgentOutput is not strict.** `agentOutputSchema` is a plain `z.object`, so unknown keys are stripped, not rejected. Decode it without `DisallowUnknownFields`. `agent.json` and the nested agent config objects *are* strict. `vteam.config.json` is not.
- **TS coverage baseline** (`testdata/ts-coverage.txt`): 63.9% statements, 87.2% branches. Near-zero today:
  - `integrations/pull-request` (8%)
  - `integrations/merge-request` (0%)
  - `commands/clean` (0%)
  - `commands/status` (12%)
  - `orchestrator/agent-runner` (23%)
  - `commands/cron` (30%)
  The Go port must reach at least each package's baseline, and the gaps above get real tests.

## Goldens

`scripts/capture-goldens.ts` (deleted at cutover) ran the TS code and wrote `testdata/golden/*.json`. Go tests load these files directly. They are now frozen: when behavior changes on purpose, edit the golden by hand together with the code.

| File | What it pins |
|---|---|
| `prompt.json`, `on-finish-prompt.json`, `memory-prompt.json` | exact system and user prompts |
| `agent-output.json` | fence/prose JSON extraction, valid vs invalid output |
| `agent-config.json` | 666 `agent.json` cases (the full cross-field matrix plus edge cases): valid or not, with messages |
| `vteam-config.json` | config validation and defaults |
| `cron.json` | expression validity and the next 5 fire times |
| `log.json` | clock, duration, age, truncate, banner, section, and list formatting, with and without color |
| `slugify.json`, `aven-label.json`, `task-description.json`, `task-filter.json` | pure helpers, routing, pick order |
| `describe-tool.json` | tool-line text, tool error extraction |
| `claude-run.json` | claude argv, stdin, and rendered output for `testdata/claude/*.jsonl` |

## Hard parts

1. **No module mocking.** `vi.mock` has no Go equivalent. Orchestration takes a `Deps` struct of interfaces (`TaskManager`, `AgentRunner`, `Worktrees`, `Platform`, `Clock`, `Sleep`). Tests pass in-memory fakes.
2. **zod.** It is replaced by:
   - structs decoded with `DisallowUnknownFields`
   - a custom `UnmarshalJSON` for the `content` union and the `taskFilter` values
   - hand-written `Validate()` for the refinements
   - `invopop/jsonschema` to generate `agent.schema.json`
3. **Byte-exact text.** The prompt builder, log formatting, and status rendering must keep their exact output. Phase 0 captures goldens from the TS code. The Go code must reproduce them.

## Layout

```
cmd/vteam/main.go           cobra root; commands return errors, only main exits
internal/
  ansi/ log/ slugify/
  lock/                     mkdir lock, kill(pid,0) stale check
  config/                   vteam.config.json + agent.json, Validate(), JSON schema
  agent/                    agent resolution
  output/                   AgentOutput + Content union
  prompt/                   prompt builder
  claude/                   claude -p runner, stream-json parser
  tasks/                    TaskManager, labels, claim, pickNextTask, aven impl
  git/                      worktree manager
  platform/                 Platform interface; github.go, gitlab.go
  execx/                    exec Runner interface shared by aven/git/platform
  runlog/ runstate/
  commands/                 run, status, clean, ensuremerge, cron, dashboard, init, skill
  templates/                go:embed
  testfakes/                in-memory fakes for Deps
testdata/                   goldens captured from TS (Phase 0)
e2e/testdata/*.txtar        testscript suites
```

## Testing

| TS | Go |
|---|---|
| `vi.mock` modules | `Deps` + `internal/testfakes` |
| real-git temp repos | same, `t.TempDir()` |
| real aven round trip, `skipIf` | `exec.LookPath("aven")` + `t.Skip` |
| `process.exit` spies | commands return errors |
| gh/glab/aven argv tests | fake `execx.Runner` records argv, returns canned output |
| claude subprocess | fake claude via `TestMain` re-exec, replays stream-json fixtures |
| hard-coded delays (push retry 5s/15s, cron, dashboard refresh) | injected `Clock`/`Sleep` |
| — | `go test -race ./...` |
| — | testscript e2e: real binary, fake `claude`/`aven`/`gh` on PATH |

Parity: `testdata/ts-test-inventory.txt` lists every TS test case (356). Each case gets a Go subtest. A case may only be dropped with a written reason.

These areas are under-tested today and get new tests in the port:
- `status`
- `clean`
- `run-state`
- `pull-request.ts`: 538 lines, 5 cases. Every operation gets argv + canned-JSON tests on both platforms.

## Phases

0. **Capture TS behavior.** ✅ Done.
   - A `tsx` script writes goldens to `testdata/`: prompts, log formatting, status frames, `agent.schema.json`, stream-json parse results.
   - Generate the test inventory.
   - Record the TS coverage baseline.
1. **Skeleton.** ✅ Done. `go.mod`, `cmd/vteam`, justfile targets (`build`, `test -race`, `lint` = `go vet` + `golangci-lint`, `dev`), CI.
2. **Leaf packages.** ✅ Done.
   - `ansi`, `log`, `slugify`, `lock`, `labels`, `config`, `output`, `runstate`, `runlog`.
   - `runlog` takes an explicit `io.Writer` (terminal + ANSI-stripping file writer) instead of patching stdout.
3. **Exec boundary.** ✅ Done. `execx`, `git` (ports the 25 real-git tests), `tasks/aven`, `platform`.
4. **Claude runner + prompt builder.** ✅ Done.
   - Runner: stream-json parsing, tool lines, timeout via `exec.CommandContext` + `cmd.Cancel` (SIGTERM).
   - Prompt builder: golden tests with an `-update` flag.
5. **Orchestration.** ✅ Done.
   - `run` and `ensure-merge` on `Deps`.
   - Port all 46 `pipeline.test.ts` cases, including claim-lock contention between goroutines.
6. **Remaining commands.** ✅ Done. `init`, `status`, `clean`, `cron`, `dashboard` (bubbletea), `skill`. Then the testscript e2e suite.
7. **Cutover.** ✅ Done.
   - Run the Go binary on the dogfood `vteam/` for a few improver/coder cycles. On-disk state formats are unchanged, so TS and Go can run side by side on the same repo.
   - Delete `src/` and the npm files.
   - Update `CLAUDE.md`, `README.md`, `skills/*`, and `.claude/skills/cli-output-style`.

## Status after phases 1–6

- Every Go package passes `go test -race`. All goldens match. `vteam status` prints byte-identical output from the TS and Go versions on the dogfood project.
- `testdata/ts-test-inventory.txt`: 344 of 356 TS cases ported, 12 dropped with a reason (shell escaping, node-only child args, assertions on mocks).
- Coverage: 85.8% of statements across `internal/` (TS: 63.9%). Every package meets or beats its TS file except `scaffold` (86% vs 100%). What is left there is error returns after file writes, which TS did not have as separate statements.
- e2e (`cmd/vteam/testdata/script/*.txtar`) runs the real binary through init, errors, a failed run, and the full lifecycle: improver finding → triage → coder worktree → push to a bare origin → PR → merge ensurer → clean. It uses the real `aven` on a temp database and fake `claude`/`gh`.
- Lint: `go vet` + golangci-lint (pinned with `go get -tool`). It found one real bug, fixed and covered by a regression test: a failed memory write was reported as success.
- Breaking changes so far:
  - error messages start lowercase (`claude exited with code 1`)
  - cron `L` and `n#k` are gone
  - aven metadata flags are written in key order
  - the dashboard also quits on `q`

## Cutover

- Dogfooded the Go binary by hand on this repo before deleting anything.
- Deleted `src/`, `package.json`, `package-lock.json`, `tsconfig.json`, `eslint.config.js`, `scripts/capture-goldens.ts`, and the `goldens`/`ts-*` just targets.
- `CLAUDE.md`, `README.md`, `skills/*`, and `.claude/skills/cli-output-style` now describe the Go code.
- `testdata/ts-coverage.txt` and `testdata/ts-test-inventory.txt` stay as the record of the parity check.
