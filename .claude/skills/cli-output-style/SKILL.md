---
name: cli-output-style
description: The terminal output style shared by every vteam CLI command (run, cron, status, dashboard, ensure-merge). Load before adding a new vteam command or changing what an existing command prints, so its output uses the src/log.ts helpers and looks like the rest.
---

All vteam terminal output goes through `src/log.ts` (layout) and `src/ansi.ts` (`color`, `stripAnsi`). Never call `console.log` with hand-built strings or raw ANSI codes in a command. If a helper you need is missing, add it to `src/log.ts` rather than formatting inline.

## Two kinds of commands

**Streaming commands** do work and report it as it happens (`vteam run`, `vteam ensure-merge`, `vteam cron start`). They print lines immediately with the side-effecting helpers.

**Snapshot commands** show current state (`vteam status`, `vteam cron status`, `vteam dashboard`). They build a `string[]` with the `format*` helpers and print it once with `console.log(lines.join("\n"))`. The dashboard redraws the same lines, so put the rendering in a `render*(...) : string[]` function that other commands can reuse (`renderScheduler` is shared by `status`, `cron status`, and `cron start`).

## Building blocks

| Helper | Output | Use for |
| --- | --- | --- |
| `banner(title, detail?)` / `formatBanner` | `vteam ▸ <title>   <gray detail>` | First line of every command. `title` is the command or agent name. `detail` is the log path (`log: vteam/.logs/...`), pid, or scope (`aven: <project> · label: <label>`). |
| `section(title)` / `formatSection` | blank line + `━━ Title ━━━━` (cyan, bold, up to 72 cols) | Phases of a streaming command (`Setup`, `Agent: <name>`, `Result`, `Cleanup`) or groups in a snapshot (`Running`, `Up next`, `Needs attention`). Title Case. A count goes in parentheses: `Awaiting merge (3)`. |
| `step(text)` | ` 00:12  • text` | A neutral progress step or a decision ("lock acquired", "picked task …", "nothing to do"). |
| `ok(text)` | ` 00:12  ✓ text` | Something succeeded or changed state as intended. |
| `warn(text)` | ` 00:12  ! text` (yellow, stderr) | Not fatal but worth noticing: a fallback, a skipped item, a non-fatal failure. |
| `fail(text)` | ` 00:12  ✗ text` (red, stderr) | Something failed. |
| `field(label, value)` | gray label padded to 10 cols, aligned under the text of the line above | Details of the preceding step/ok/warn/fail line: `path`, `branch`, `PR`, `log`, `reason`, `error`. Short, lowercase labels. |
| `finished(name, failure?)` | blank line + `✓ <name> completed in 12s` or `✗ <name> failed after 12s: <msg>` | Last line of a streaming command that has a clear end. |
| `formatListItem(glyph, text)` | `  <glyph> text` | One entry in a snapshot section. |
| `formatListDetail(label, value)` | `    <gray label> value` | A detail line under a list item. |
| `claudeText`, `toolCall`, `toolError`, `agentExit` | `❯`, `⚙`, … | Only for streaming `claude -p` output in `agent-runner.ts`. |

Formatting helpers: `formatDuration` (`12s`, `3m05s`, `1h02m`) for how long something took, `formatAge` (`5m`, `2h 10m`, `3d 1h`) for how long ago or until, `formatTimeOfDay` for a wall-clock time, `truncate` for single-line cuts.

## Gutter (streaming commands)

Every `step`/`ok`/`warn`/`fail` line starts with a gray time gutter.

- Default: elapsed time since `startClock()` (`00:12`). Call `log.startClock()` right before the banner.
- Long-lived processes (`cron start`) call `log.useWallClock()` instead, so the gutter shows `14:03:27`.

## Glyphs and colors

- `•` blue: neutral item or step. `✓` green: good. `!` yellow: attention. `✗` red: failure. `●` green: something live (a running job). `·` gray with `color.dim` text: empty placeholder ("nothing running"). Snapshot sections use the same glyph meanings as streaming lines.
- `color.bold` for the identifier the line is about: task ref, agent or job name, the item name in a list. Do not bold whole sentences.
- `color.gray` for secondary metadata: pids, durations, URLs in lists, hints such as `(run 'vteam clean')`.
- Severity: use the `SEVERITY_COLOR` map in `status.ts` (critical bold red, high red, medium yellow, low gray) when showing a task severity.
- Never hard-code escape codes. `color` already respects TTY, `NO_COLOR`, and `FORCE_COLOR`.

## Text style

- Lowercase sentence fragments without a trailing period: `worktree removed`, `pushed vteam/fix-x-abc123`, `failed to add note to task APP-1: <msg>`.
- Lead with what happened. Put the reason in a `field` line or a gray suffix.
- One finding per line. When an item has several details (a PR, its state, the action taken), use a headline line plus `field` lines instead of one long sentence:

  ```
   00:03  ✓ APP-7 Fix race in claim lock
            PR        #42 https://github.com/o/r/pull/42
            state     open · approved · has "vteam:changes-requested" label
            action    task moved to todo
  ```

- Join list-like facts with ` · ` (`merged 2 · canceled 0 · reopened 1 · waiting 4`, `pid 123 · 5m`).
- Align columns in lists by padding names to the longest one (`padEnd(width)`), as `renderScheduler` and `renderUpNext` do.
- Hints name the command to run in single quotes: `(start it with 'vteam cron start')`.

## Command skeleton (streaming)

```ts
export async function fooCommand(): Promise<void> {
  const cwd = process.cwd();
  const runLog = startRunLog(cwd, "foo");
  log.startClock();
  log.banner("foo", `log: ${relative(cwd, runLog.path)}`);
  try {
    log.section("Setup");
    // ... log.step(...)
    log.section("Result");
    // ... log.ok(...) / log.fail(...)
    log.finished("foo");
  } catch (err) {
    runLog.write(`${err instanceof Error ? (err.stack ?? err.message) : String(err)}\n`);
    log.finished("foo", err instanceof Error ? err.message : String(err));
    process.exitCode = 1;
  } finally {
    runLog.close();
  }
}
```

A command that runs unattended (from the scheduler) writes a run log with `startRunLog`, which tees stdout/stderr into `vteam/.logs/<name>/` and strips colors. Pass a different name only when the log directory must differ from the command name (`ensure-merge` logs to `merge-ensurer`).

## Snapshot skeleton

```ts
export async function fooCommand(): Promise<void> {
  const cwd = process.cwd();
  console.log([log.formatBanner("foo", "<scope>"), ...renderFoo(cwd), ""].join("\n"));
}

function renderFoo(cwd: string): string[] {
  const lines = [log.formatSection("Things")];
  if (things.length === 0) return [...lines, log.formatListItem(color.gray("·"), color.dim("no things"))];
  const width = Math.max(...things.map((t) => t.name.length));
  for (const t of things) {
    lines.push(log.formatListItem(color.blue("•"), `${color.bold(t.name.padEnd(width))}  ${color.gray(t.detail)}`));
  }
  return lines;
}
```

Leave out a section that would only say "nothing" when its absence is informative (`Awaiting merge`, `Needs attention`). Keep it with a `·` placeholder when the reader expects to see it (`Running`, `Scheduler`).

## Tests

Tests run without a TTY, so output has no color and no line truncation. Assert on `console.log`/`console.error` spy text with `toContain` for the parts that matter (field values, the headline), not on the exact gutter time.
