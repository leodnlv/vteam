import { mkdirSync, mkdtempSync, writeFileSync, rmSync, chmodSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { Cron } from "croner";
import { buildPrompt, buildOnFinishPrompt, buildMemoryCurationPrompt } from "../src/orchestrator/prompt-builder.js";
import { parseAgentOutput } from "../src/orchestrator/output-schema.js";
import { agentConfigSchema, vteamConfigSchema, isValidCronExpression } from "../src/config/schema.js";
import { describeTool, toolErrorText, runClaudeAgent, type AgentRunResult, type ContentBlock } from "../src/orchestrator/agent-runner.js";
import { formatTaskDescription, matchesTaskFilter, pickNextTask } from "../src/tasks/task-manager.js";
import { toAvenLabel } from "../src/tasks/labels.js";
import { slugify } from "../src/slugify.js";
import { stripAnsi } from "../src/ansi.js";
import * as log from "../src/log.js";
import type { TaskIndex, TaskManager } from "../src/tasks/task-manager.js";
import type { AgentConfig, PRFollowUpContext, PRReviewContext, RunOutcome, Task, TaskFilter } from "../src/types.js";

const OUT = resolve(import.meta.dirname, "..", "testdata", "golden");
const tmp = mkdtempSync(join(tmpdir(), "vteam-goldens-"));

function write(name: string, cases: unknown): void {
  writeFileSync(join(OUT, `${name}.json`), JSON.stringify(cases, null, 2) + "\n");
  console.log(`wrote testdata/golden/${name}.json`);
}

function withColor<T>(enabled: boolean, fn: () => T): T {
  const saved = { NO_COLOR: process.env.NO_COLOR, FORCE_COLOR: process.env.FORCE_COLOR };
  if (enabled) {
    delete process.env.NO_COLOR;
    process.env.FORCE_COLOR = "1";
  } else {
    process.env.NO_COLOR = "1";
    delete process.env.FORCE_COLOR;
  }
  try {
    return fn();
  } finally {
    for (const [k, v] of Object.entries(saved)) {
      if (v === undefined) delete process.env[k];
      else process.env[k] = v;
    }
  }
}

function mdFile(name: string, content: string): string {
  const path = join(tmp, name);
  writeFileSync(path, content);
  return path;
}

function task(overrides: Partial<Task>): Task {
  return {
    ref: "APP-1",
    title: "Fix the thing",
    status: "todo",
    severity: "medium",
    retryCount: 0,
    metadata: {},
    description: "## Description\n\nIt is broken.",
    notes: [],
    createdAt: "2026-01-01T00:00:00Z",
    ...overrides,
  };
}

function fakeTasks(all: Task[]): TaskManager {
  const index: TaskIndex = { all, byStatus: new Map(), titles: all.map((t) => t.title) };
  return {
    getIndex: async () => index,
    listReady: async () => [],
    listDone: async () => [],
    create: async () => "",
    update: async () => {},
    addNote: async () => {},
  };
}

async function capturePrompts(): Promise<void> {
  const agentMd = "  You are the improver.\n\nFind one issue.\n\n";
  const agentMdPath = mdFile("AGENT.md", agentMd);
  const memoryMdPath = mdFile("MEMORY.md", "Keep it short.\n");

  const existing = [
    task({ ref: "APP-1", title: "SQL injection in login", status: "done", severity: "critical" }),
    task({ ref: "APP-2", title: "Flaky test", status: "canceled", severity: "low" }),
  ];
  const followUpPR = {
    number: 42,
    title: "vteam: fix thing",
    branch: "vteam/fix-thing-abc",
    url: "https://github.com/o/r/pull/42",
    state: "open" as const,
    baseBranch: "main",
    labels: [],
    mergeable: "conflicting" as const,
    review: "none" as const,
  };
  const threads = [
    {
      id: "PRRT_1",
      path: "src/a.ts",
      line: 10,
      comments: [
        { author: "alice", body: "Rename this.", createdAt: "2026-01-01T00:00:00Z" },
        { author: "bob", body: "Agreed.", createdAt: "2026-01-01T00:01:00Z" },
      ],
    },
    { id: "PRRT_2", path: "src/b.ts", comments: [{ author: "alice", body: "Why?", createdAt: "2026-01-01T00:00:00Z" }] },
    { id: "PRRT_3", comments: [{ author: "carol", body: "General remark.", createdAt: "2026-01-01T00:00:00Z" }] },
  ];
  const review: PRReviewContext = {
    pr: { number: 7, title: "Add feature", branch: "feature", url: "https://github.com/o/r/pull/7" },
    repoSlug: "o/r",
    comments: [
      { author: "alice", body: "Nit.", path: "src/x.ts", line: 3, createdAt: "2026-01-01T00:00:00Z" },
      { author: "bob", body: "No line.", path: "src/y.ts", createdAt: "2026-01-01T00:00:00Z" },
      { author: "carol", body: "Top level.", createdAt: "2026-01-01T00:00:00Z" },
    ],
  };

  type PromptCase = {
    name: string;
    agent: Omit<AgentConfig, "agentMdPath" | "memory"> & { memory?: boolean };
    existingTasks: Task[];
    task?: Task;
    review?: PRReviewContext | PRFollowUpContext;
    focus?: string;
    memoryContent?: string;
  };

  const cases: PromptCase[] = [
    { name: "bare generic agent", agent: { name: "reviewer" }, existingTasks: [] },
    { name: "task output with existing tasks", agent: { name: "improver", output: "task" }, existingTasks: existing },
    {
      name: "focus and memory",
      agent: { name: "improver", output: "task", memory: true },
      existingTasks: existing,
      focus: "Look at auth only.",
      memoryContent: "- scanned src/auth\n- false positive: x",
    },
    {
      name: "task with notes",
      agent: { name: "coder", input: "task", worktree: true },
      existingTasks: existing,
      task: task({
        notes: [
          { body: "coder: failed\nTests broke.", createdAt: "2026-01-02T00:00:00Z" },
          { body: "Try the other approach.", createdAt: "2026-01-03T00:00:00Z" },
        ],
      }),
    },
    { name: "task without notes", agent: { name: "coder", input: "task", worktree: true }, existingTasks: [], task: task({}) },
    { name: "pr review", agent: { name: "pr-fixer", input: "pr", worktree: true }, existingTasks: [], review },
    {
      name: "pr review without comments",
      agent: { name: "pr-fixer", input: "pr", worktree: true },
      existingTasks: [],
      review: { ...review, comments: [] },
    },
    {
      name: "follow-up with threads",
      agent: { name: "coder", input: "task", worktree: true },
      existingTasks: [],
      task: task({ metadata: { "pr-url": followUpPR.url } }),
      review: { pr: followUpPR, threads },
    },
    {
      name: "follow-up without threads",
      agent: { name: "coder", input: "task", worktree: true, memory: true },
      existingTasks: [],
      task: task({}),
      review: { pr: followUpPR, threads: [] },
    },
    {
      name: "follow-up with conflicted merge",
      agent: { name: "coder", input: "task", worktree: true },
      existingTasks: [],
      task: task({}),
      review: { pr: followUpPR, threads, baseMerge: { baseBranch: "main", conflictedFiles: ["src/a.ts", "README.md"] } },
    },
    {
      name: "follow-up with clean merge",
      agent: { name: "coder", input: "task", worktree: true },
      existingTasks: [],
      task: task({}),
      review: { pr: followUpPR, threads: [], baseMerge: { baseBranch: "main", conflictedFiles: [] } },
    },
  ];

  const out: unknown[] = [];
  for (const c of cases) {
    const agent: AgentConfig = {
      ...c.agent,
      agentMdPath,
      memory: c.agent.memory ? { memoryMdPath } : undefined,
    };
    const result = await buildPrompt(agent, fakeTasks(c.existingTasks), c.task, c.review, c.focus, c.memoryContent);
    out.push({ ...c, agentMd, expected: result });
  }
  write("prompt", out);

  const onFinishMd = "Post a summary.\n";
  const onFinishMdPath = mdFile("ON_FINISH.md", onFinishMd);
  const base: RunOutcome = {
    agent: "coder",
    status: "completed",
    startedAt: "2026-01-01T00:00:00.000Z",
    completedAt: "2026-01-01T00:05:00.000Z",
  };
  const onFinishCases: { name: string; outcome: RunOutcome }[] = [
    { name: "minimal", outcome: base },
    {
      name: "everything",
      outcome: {
        ...base,
        focus: "auth",
        task: { title: "Fix it", severity: "high" },
        branch: "vteam/fix-it-1",
        prUrl: "https://github.com/o/r/pull/1",
        reviewedPR: { number: 3, title: "Feature", url: "https://github.com/o/r/pull/3" },
        tasksCreated: ["APP-1", "APP-2"],
        commitMessage: { subject: "vteam: fix it", body: "Long body." },
        content: { type: "generic", body: "A review." },
      },
    },
    {
      name: "task content and error",
      outcome: {
        ...base,
        status: "failed",
        error: "boom",
        branch: "only-branch",
        content: {
          type: "task",
          body: { title: "T", severity: "low", description: "D", files: ["a.ts:1"] },
        },
      },
    },
    { name: "pr url only", outcome: { ...base, prUrl: "https://x/1", tasksCreated: [] } },
  ];
  write(
    "on-finish-prompt",
    onFinishCases.map((c) => ({ ...c, onFinishMd, expected: buildOnFinishPrompt({ onFinishMdPath }, c.outcome) })),
  );

  const memoryMd = readFileSync(memoryMdPath, "utf-8");
  const memoryCases = [
    { name: "first entry", currentMemory: "", memoryUpdate: "Scanned src/." },
    { name: "with focus", currentMemory: "- old", memoryUpdate: "- new", focus: "auth" },
  ];
  write(
    "memory-prompt",
    memoryCases.map((c) => ({
      ...c,
      memoryMd,
      expected: buildMemoryCurationPrompt({ memoryMdPath }, c.currentMemory, c.memoryUpdate, c.focus),
    })),
  );
}

function captureOutputParsing(): void {
  const valid = { status: "completed", summary: "did it" };
  const inputs: [string, string][] = [
    ["minimal", JSON.stringify(valid)],
    ["json fence", "```json\n" + JSON.stringify(valid) + "\n```"],
    ["bare fence", "```\n" + JSON.stringify(valid) + "\n```"],
    ["prose before and after", `Here you go:\n${JSON.stringify(valid)}\nThanks!`],
    ["brace in prose before json", `I used {braces} here.\n${JSON.stringify(valid)}`],
    ["unknown keys are stripped", JSON.stringify({ ...valid, extra: 1 })],
    [
      "full task output",
      JSON.stringify({
        status: "partial",
        summary: "s",
        content: { type: "task", body: { title: "t", severity: "high", description: "d", suggestedFix: "f", files: ["a:1"] } },
        filesChanged: ["a"],
        commitMessage: { subject: "vteam: x", body: "" },
        blockerReason: "b",
        memoryUpdate: "m",
        threadReplies: [{ thread: "T1", body: "fixed" }],
      }),
    ],
    ["generic content", JSON.stringify({ ...valid, content: { type: "generic", body: "" } })],
    ["bad status", JSON.stringify({ status: "done", summary: "s" })],
    ["missing summary", JSON.stringify({ status: "completed" })],
    ["unknown content type", JSON.stringify({ ...valid, content: { type: "other", body: "x" } })],
    ["task content with empty files", JSON.stringify({ ...valid, content: { type: "task", body: { title: "t", severity: "low", description: "d", files: [] } } })],
    ["task content with empty title", JSON.stringify({ ...valid, content: { type: "task", body: { title: "", severity: "low", description: "d", files: ["a"] } } })],
    ["task content with bad severity", JSON.stringify({ ...valid, content: { type: "task", body: { title: "t", severity: "urgent", description: "d", files: ["a"] } } })],
    ["generic content with object body", JSON.stringify({ ...valid, content: { type: "generic", body: { a: 1 } } })],
    ["commit message empty subject", JSON.stringify({ ...valid, commitMessage: { subject: "", body: "b" } })],
    ["thread reply empty body", JSON.stringify({ ...valid, threadReplies: [{ thread: "T1", body: "" }] })],
    ["not json", "I could not finish."],
    ["empty", ""],
    ["array", "[1,2]"],
  ];
  write(
    "agent-output",
    inputs.map(([name, input]) => {
      try {
        return { name, input, ok: true, expected: parseAgentOutput(input) };
      } catch {
        return { name, input, ok: false };
      }
    }),
  );
}

function captureAgentConfig(): void {
  const options = {
    worktree: [undefined, true, false],
    readOnly: [undefined, true, false],
    output: [undefined, "task"],
    input: [undefined, "task", "pr"],
    autoPR: [undefined, true, false],
    resolveThreads: [undefined, true],
    taskFilter: [undefined, { "found-by": "improver" }],
  } as const;
  const keys = Object.keys(options) as (keyof typeof options)[];
  const combos: Record<string, unknown>[] = [{}];
  for (const key of keys) {
    const next: Record<string, unknown>[] = [];
    for (const combo of combos) {
      for (const value of options[key]) {
        next.push(value === undefined ? combo : { ...combo, [key]: value });
      }
    }
    combos.splice(0, combos.length, ...next);
  }

  const extra: Record<string, unknown>[] = [
    { unknownKey: true },
    { memory: { model: "haiku", bogus: 1 } },
    { onFinish: { allowedTools: ["Read"] } },
    { $schema: "../../agent.schema.json", model: "sonnet" },
    { cron: "0 */6 * * *" },
    { cron: "0 */6 * * * *" },
    { cron: "not a cron" },
    { input: "task", taskFilter: { "found-by": ["improver", "security-analyst"] } },
    { input: "task", taskFilter: { actor: null } },
    { input: "task", taskFilter: { actor: [null, "coder"] } },
    { input: "task", taskFilter: { actor: [] } },
    { input: "task", taskFilter: { actor: "" } },
    { input: "task", taskFilter: { "": "x" } },
    { input: "task", taskFilter: { actor: 1 } },
    { worktree: "yes" },
    { output: "generic" },
    { input: "issue" },
    { prCreateLabels: ["vteam", 2] },
  ];

  write(
    "agent-config",
    [...combos, ...extra].map((input) => {
      const result = agentConfigSchema.safeParse(input);
      return {
        input,
        ok: result.success,
        errors: result.success ? [] : result.error.issues.map((i) => i.message),
      };
    }),
  );

  const vteamInputs: Record<string, unknown>[] = [
    { baseBranch: "main", platform: "github", worktreeDir: ".w", tasks: { maxRetries: 3, project: "p" } },
    { baseBranch: "main", platform: "gitlab", worktreeDir: ".w", tasks: { maxRetries: 0, project: "p", label: "x", workspace: "w" }, mergeEnsurer: { cron: "*/5 * * * *" } },
    { baseBranch: "main", platform: "github", worktreeDir: ".w", tasks: { maxRetries: 3, project: "p" }, mergeEnsurer: { label: "custom" }, extra: true },
    { baseBranch: "main", platform: "bitbucket", worktreeDir: ".w", tasks: { maxRetries: 3, project: "p" } },
    { baseBranch: "main", platform: "github", worktreeDir: ".w", tasks: { maxRetries: -1, project: "p" } },
    { baseBranch: "main", platform: "github", worktreeDir: ".w", tasks: { maxRetries: 1.5, project: "p" } },
    { baseBranch: "main", platform: "github", worktreeDir: ".w", tasks: { maxRetries: 3, project: "" } },
    { baseBranch: "main", platform: "github", worktreeDir: ".w", tasks: { maxRetries: 3, project: "p", label: "" } },
    { baseBranch: "main", platform: "github", worktreeDir: ".w", tasks: { maxRetries: 3, project: "p" }, mergeEnsurer: { cron: "bad" } },
    { platform: "github", worktreeDir: ".w", tasks: { maxRetries: 3, project: "p" } },
  ];
  write(
    "vteam-config",
    vteamInputs.map((input) => {
      const result = vteamConfigSchema.safeParse(input);
      return result.success ? { input, ok: true, expected: result.data } : { input, ok: false };
    }),
  );
}

function captureCron(): void {
  const from = new Date("2026-03-07T10:17:30Z");
  const exprs = [
    "* * * * *",
    "0 */6 * * *",
    "*/15 9-17 * * 1-5",
    "30 2 1 * *",
    "0 0 1 * 1",
    "0 0 * * 0",
    "0 0 * * 7",
    "0 12 29 2 *",
    "5,10,55 * * * *",
    "0 0 * jan,jul *",
    "0 0 * * mon-fri",
    "0 0 L * *",
    "0 0 * * 5#2",
    "@daily",
    "0 0 * * * *",
    "60 * * * *",
    "* 24 * * *",
    "",
    "a b c d e",
  ];
  const savedTz = process.env.TZ;
  process.env.TZ = "UTC";
  try {
    write(
      "cron",
      exprs.map((expr) => {
        const valid = isValidCronExpression(expr);
        if (!valid) return { expr, valid };
        const job = new Cron(expr, { paused: true, timezone: "UTC" });
        const next = job.nextRuns(5, from).map((d) => d.toISOString());
        job.stop();
        return { expr, valid, from: from.toISOString(), next };
      }),
    );
  } finally {
    if (savedTz === undefined) delete process.env.TZ;
    else process.env.TZ = savedTz;
  }
}

function captureLog(): void {
  const clocks = [0, 999, 1000, 59_999, 60_000, 3_599_000, 3_600_000, 36_061_000, -5];
  const durations = [0, 499, 500, 1500, 59_499, 59_500, 60_000, 61_000, 3_599_499, 3_600_000, 7_260_000];
  const ages = [-1, 0, 59_999, 60_000, 3_599_999, 3_600_000, 86_399_999, 86_400_000, 90_061_000];
  const truncates: [string, number][] = [["hello", 10], ["hello", 5], ["hello", 4], ["hello", 1], ["hello", 0], ["", 3]];

  const formatted = (enabled: boolean) =>
    withColor(enabled, () => ({
      banner: log.formatBanner("status"),
      bannerDetail: log.formatBanner("status", "aven: p · label: vteam"),
      section: log.formatSection("Running"),
      sectionLong: log.formatSection("x".repeat(80)),
      listItem: log.formatListItem("•", "line one\nline two\n\nline four"),
      listDetail: log.formatListDetail("worktree", ".w/x"),
    }));

  write("log", {
    formatClock: clocks.map((ms) => ({ ms, expected: log.formatClock(ms) })),
    formatDuration: durations.map((ms) => ({ ms, expected: log.formatDuration(ms) })),
    formatAge: ages.map((ms) => ({ ms, expected: log.formatAge(ms) })),
    truncate: truncates.map(([text, width]) => ({ text, width, expected: log.truncate(text, width) })),
    plain: formatted(false),
    color: formatted(true),
  });
}

function capturePureHelpers(): void {
  const slugs = ["Fix SQL injection in login()", "  Ünïcödé — Tëst  ", "---a---b---", "", "日本語 title", "UPPER_case 123"];
  write("slugify", slugs.map((input) => ({ input, expected: slugify(input) })));

  const labels = ["vteam-improver", "Vteam_Coder", "  spaced  out  ", "--x--", "a.b/c", "Ünï"];
  write("aven-label", labels.map((input) => ({ input, expected: toAvenLabel(input) })));

  write("task-description", [
    { body: { title: "t", severity: "low", description: "D", files: ["a.ts:1", "b.ts"] } },
    { body: { title: "t", severity: "low", description: "D", suggestedFix: "F", files: ["a.ts"] } },
  ].map((c) => ({ ...c, expected: formatTaskDescription(c.body as never) })));

  const tasks = [
    task({ ref: "A", severity: "low", createdAt: "2026-01-01T00:00:00Z", metadata: { "found-by": "improver" } }),
    task({ ref: "B", severity: "high", createdAt: "2026-01-03T00:00:00Z", metadata: { "found-by": "security-analyst" } }),
    task({ ref: "C", severity: "high", createdAt: "2026-01-02T00:00:00Z", metadata: { "found-by": "improver", actor: "" } }),
    task({ ref: "D", severity: "critical", createdAt: "2026-01-04T00:00:00Z", retryCount: 3, metadata: {} }),
    task({ ref: "E", severity: "medium", createdAt: "2026-01-05T00:00:00Z", metadata: { actor: "mr-reviewer" } }),
  ];
  const filters: TaskFilter[] = [
    {},
    { "found-by": "improver" },
    { "found-by": ["improver", "security-analyst"] },
    { actor: null },
    { actor: [null, "mr-reviewer"] },
    { "found-by": "improver", actor: null },
    { "found-by": "nobody" },
  ];
  write("task-filter", {
    tasks,
    maxRetries: 3,
    cases: filters.map((filter) => ({
      filter,
      matches: tasks.filter((t) => matchesTaskFilter(t, filter)).map((t) => t.ref),
      next: pickNextTask(tasks, filter, 3)?.ref ?? null,
    })),
  });

  const cwd = "/repo/wt";
  const tools: [string, Record<string, unknown>][] = [
    ["Bash", { command: "ls -la" }],
    ["Bash", {}],
    ["Read", { file_path: "/repo/wt/src/a.ts" }],
    ["Read", { file_path: "/elsewhere/b.ts" }],
    ["Edit", { file_path: "/repo/wt" }],
    ["NotebookEdit", { notebook_path: "/repo/wt/n.ipynb" }],
    ["Grep", { pattern: "TODO", path: "/repo/wt/src" }],
    ["Glob", { pattern: "**/*.ts" }],
    ["WebFetch", { url: "https://x.y" }],
    ["WebSearch", { query: "go cron" }],
    ["Task", { description: "explore", command: "c" }],
    ["mcp__x__y", { path: "/repo/wt/z" }],
    ["mcp__x__y", { query: "" }],
    ["Read", { file_path: "" }],
  ];
  const blocks: ContentBlock[] = [
    { type: "tool_result", is_error: true, content: "Permission denied" },
    { type: "tool_result", is_error: true, content: "" },
    { type: "tool_result", is_error: true, content: [{ type: "text", text: "a" }, { type: "text", text: "b" }] },
    { type: "tool_result", is_error: true, content: [{ type: "image" }] },
    { type: "tool_result", is_error: true },
    { type: "tool_result", is_error: false, content: "fine" },
    { type: "text", is_error: true, content: "x" },
  ];
  write("describe-tool", {
    cwd,
    tools: tools.map(([name, input]) => ({ name, input, expected: describeTool(name, input, cwd) })),
    toolErrors: blocks.map((block) => ({ block, expected: toolErrorText(block) ?? null })),
  });
}

async function captureClaudeRun(): Promise<void> {
  const fixtureDir = resolve(import.meta.dirname, "..", "testdata", "claude");
  const bin = join(tmp, "bin");
  mkdirSync(bin);
  const argsFile = join(tmp, "claude-args.txt");
  const stdinFile = join(tmp, "claude-stdin.txt");

  const cwd = join(tmp, "wt");
  mkdirSync(cwd);
  const served = join(tmp, "served.jsonl");

  const results: unknown[] = [];
  for (const fixture of ["success.jsonl", "failure.jsonl"]) {
    const exitCode = fixture.startsWith("failure") ? 3 : 0;
    writeFileSync(served, readFileSync(join(fixtureDir, fixture), "utf-8").replaceAll("/repo/wt", cwd));
    writeFileSync(
      join(bin, "claude"),
      `#!/bin/sh\nprintf '%s\\n' "$@" > '${argsFile}'\ncat > '${stdinFile}'\necho 'warning on stderr' >&2\ncat '${served}'\nexit ${exitCode}\n`,
    );
    chmodSync(join(bin, "claude"), 0o755);

    const lines: string[] = [];
    const saved = { log: console.log, error: console.error, path: process.env.PATH };
    console.log = (...a: unknown[]) => lines.push(a.join(" "));
    console.error = (...a: unknown[]) => lines.push(a.join(" "));
    process.env.PATH = `${bin}:${process.env.PATH}`;
    let result: AgentRunResult;
    try {
      result = await withColor(false, () =>
        runClaudeAgent({
          systemPrompt: "SYSTEM",
          userPrompt: "USER",
          cwd,
          model: "sonnet",
          allowedTools: ["Read", "Bash(git *)"],
          disallowedTools: ["WebFetch"],
        }),
      );
    } finally {
      console.log = saved.log;
      console.error = saved.error;
      process.env.PATH = saved.path;
    }

    const tmpPrompt = /\/[^\s]*vteam-[^/\s]+\//g;
    const args = readFileSync(argsFile, "utf-8").trimEnd().split("\n").map((a) => a.replace(tmpPrompt, "<tmp>/"));
    results.push({
      fixture,
      exitCode,
      args,
      stdin: readFileSync(stdinFile, "utf-8"),
      output: lines.filter((l) => !l.startsWith("(node:")).map((l) => stripAnsi(l).replace(/^ \d\d:\d\d(:\d\d)? /, " <clock> ").replaceAll(cwd, "/repo/wt").replace(tmpPrompt, "<tmp>/")),
      result: {
        exitCode: result.exitCode,
        resultText: result.resultText,
        costUsd: result.costUsd ?? null,
        durationMs: result.durationMs ?? null,
        stderr: result.stderr,
      },
    });
  }
  write("claude-run", results);
}

mkdirSync(OUT, { recursive: true });
try {
  await capturePrompts();
  captureOutputParsing();
  captureAgentConfig();
  captureCron();
  captureLog();
  capturePureHelpers();
  await captureClaudeRun();
} finally {
  rmSync(tmp, { recursive: true, force: true });
}
