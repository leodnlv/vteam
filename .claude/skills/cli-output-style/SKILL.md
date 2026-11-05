---
name: cli-output-style
description: The terminal output style shared by every vteam CLI command (run, cron, status, ensure-merge, and the TUI's Overview tab). Load before adding a new vteam command or changing what an existing command prints, so its output uses the internal/log helpers and looks like the rest.
---

All vteam terminal output goes through `internal/log` (layout: the `Logger` methods and the `Format*` functions) and `internal/ansi` (`Bold`, `Gray`, …, `Strip`). Never `fmt.Println` hand-built strings or raw ANSI codes in a command. If a helper you need is missing, add it to `internal/log` rather than formatting inline.

## Two kinds of commands

**Streaming commands** do work and report it as it happens (`vteam run`, `vteam ensure-merge`, `vteam cron start`). They print lines immediately with the `*log.Logger` methods.

**Snapshot commands** show current state (`vteam status`, `vteam cron status`). They build a `[]string` with the `log.Format*` helpers and print it once with `strings.Join(lines, "\n")`. The TUI's Overview tab (`internal/tui`) redraws the same lines, so put the rendering in a `Render*(...) []string` function that other commands can reuse (`schedule.RenderScheduler` is shared by `status`, `cron status`, and `cron start`).

## Building blocks

| Helper | Output | Use for |
| --- | --- | --- |
| `Banner(title, detail)` / `FormatBanner` | `vteam ▸ <title>   <gray detail>` | First line of every command. `title` is the command or agent name. `detail` is the log path (`log: vteam/.logs/...`), pid, or scope (`aven: <project> · label: <label>`). |
| `Section(title)` / `FormatSection` | blank line + `━━ Title ━━━━` (cyan, bold, up to 72 cols) | Phases of a streaming command (`Setup`, `Agent: <name>`, `Result`, `Cleanup`) or groups in a snapshot (`Running`, `Up next`, `Needs attention`). Title Case. A count goes in parentheses: `Awaiting merge (3)`. |
| `Step(text)` | ` 00:12  • text` | A neutral progress step or a decision ("lock acquired", "picked task …", "nothing to do"). |
| `OK(text)` | ` 00:12  ✓ text` | Something succeeded or changed state as intended. |
| `Warn(text)` | ` 00:12  ! text` (yellow, stderr) | Not fatal but worth noticing: a fallback, a skipped item, a non-fatal failure. |
| `Fail(text)` | ` 00:12  ✗ text` (red, stderr) | Something failed. |
| `Field(label, value)` | gray label padded to 10 cols, aligned under the text of the line above | Details of the preceding step/ok/warn/fail line: `path`, `branch`, `PR`, `log`, `reason`, `error`. Short, lowercase labels. |
| `Finished(name, failure)` | blank line + `✓ <name> completed in 12s` or `✗ <name> failed after 12s: <msg>` | Last line of a streaming command that has a clear end. |
| `FormatListItem(glyph, text)` | `  <glyph> text` | One entry in a snapshot section. |
| `FormatListDetail(label, value)` | `    <gray label> value` | A detail line under a list item. |
| `ClaudeText`, `ToolCall`, `ToolError`, `AgentExit` | `❯`, `⚙`, … | Only for streaming `claude -p` output in `internal/claude`. |

Formatting helpers: `FormatDuration` (`12s`, `3m05s`, `1h02m`) for how long something took, `FormatAge` (`5m`, `2h 10m`, `3d 1h`) for how long ago or until, `FormatTimeOfDay` for a wall-clock time, `Truncate` for single-line cuts, `PadEnd` for padding by rune count (pad before coloring).

## Gutter (streaming commands)

Every `Step`/`OK`/`Warn`/`Fail` line starts with a gray time gutter.

- Default: elapsed time since `StartClock()` (`00:12`). Call `logger.StartClock()` right before the banner.
- Long-lived processes (`cron start`) call `logger.UseWallClock()` instead, so the gutter shows `14:03:27`.

## Glyphs and colors

- `•` blue: neutral item or step. `✓` green: good. `!` yellow: attention. `✗` red: failure. `●` green: something live (a running job). `·` gray with `ansi.Dim` text: empty placeholder ("nothing running"). Snapshot sections use the same glyph meanings as streaming lines.
- `ansi.Bold` for the identifier the line is about: task ref, agent or job name, the item name in a list. Do not bold whole sentences.
- `ansi.Gray` for secondary metadata: pids, durations, URLs in lists, hints such as `(run 'vteam clean')`.
- Severity: use the `severityColor` map in `internal/status` (critical bold red, high red, medium yellow, low gray) when showing a task severity.
- Never hard-code escape codes. `internal/ansi` already respects TTY, `NO_COLOR`, and `FORCE_COLOR`.

## Text style

- Lowercase sentence fragments without a trailing period: `worktree removed`, `pushed vteam/fix-x-abc123`, `failed to add note to task APP-1: <msg>`.
- Lead with what happened. Put the reason in a `Field` line or a gray suffix.
- One finding per line. When an item has several details (a PR, its state, the action taken), use a headline line plus `Field` lines instead of one long sentence:

  ```
   00:03  ✓ APP-7 Fix race in claim lock
            PR        #42 https://github.com/o/r/pull/42
            state     open · approved · has "vteam:changes-requested" label
            action    task moved to todo
  ```

- Join list-like facts with ` · ` (`merged 2 · canceled 0 · reopened 1 · waiting 4`, `pid 123 · 5m`).
- Align columns in lists by padding names to the longest one (`log.PadEnd(text, width)`), as `RenderScheduler` and `renderUpNext` do.
- Hints name the command to run in single quotes: `(start it with 'vteam cron start')`.

## Command skeleton (streaming)

Commands are wired in `internal/cli`. The work lives in its own package and takes a `*log.Logger` (plus its other dependencies) so tests can pass `log.New(&buf, &buf)`.

```go
func foo(ctx context.Context, dir string) error {
	logger := log.Std()
	runLog, err := runlog.Start(dir, "foo", time.Now(), logger)
	if err != nil {
		return err
	}
	defer func() { _ = runLog.Close() }()
	logger.StartClock()
	logger.Banner("foo", "log: "+relative(dir, runLog.Path))

	err = func() error {
		logger.Section("Setup")
		// ... logger.Step(...)
		logger.Section("Result")
		// ... logger.OK(...) / logger.Fail(...)
		return nil
	}()
	if err != nil {
		logger.WriteFile(err.Error() + "\n")
		logger.Finished("foo", err.Error())
		return log.ErrReported
	}
	logger.Finished("foo", "")
	return nil
}
```

Returning `log.ErrReported` makes `main` exit 1 without printing the error a second time.

A command that runs unattended (from the scheduler) writes a run log with `runlog.Start`, which copies every line the logger prints into `vteam/.logs/<name>/` with colors stripped. Pass a different name only when the log directory must differ from the command name (`ensure-merge` logs to `merge-ensurer`).

## Snapshot skeleton

```go
func foo(dir string) error {
	lines := append([]string{log.FormatBanner("foo", "<scope>")}, RenderFoo(dir)...)
	fmt.Println(strings.Join(append(lines, ""), "\n"))
	return nil
}

func RenderFoo(dir string) []string {
	lines := []string{log.FormatSection("Things")}
	if len(things) == 0 {
		return append(lines, log.FormatListItem(ansi.Gray("·"), ansi.Dim("no things")))
	}
	width := 0
	for _, t := range things {
		width = max(width, len(t.Name))
	}
	for _, t := range things {
		lines = append(lines, log.FormatListItem(ansi.Blue("•"), ansi.Bold(log.PadEnd(t.Name, width))+"  "+ansi.Gray(t.Detail)))
	}
	return lines
}
```

Leave out a section that would only say "nothing" when its absence is informative (`Awaiting merge`, `Needs attention`). Keep it with a `·` placeholder when the reader expects to see it (`Running`, `Scheduler`).

## Tests

Tests run without a TTY, so output has no color and no line truncation. Write to a `bytes.Buffer` through `log.New(&buf, &buf)` and assert with `strings.Contains` on the parts that matter (field values, the headline), not on the exact gutter time. Pass `log.WithClock` when the gutter matters.
