import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, writeFileSync } from "node:fs";
import { rmSync } from "node:fs";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import { buildPrompt, buildOnFinishPrompt, buildMemoryCurationPrompt } from "./prompt-builder.js";
import type { TaskManager } from "../tasks/task-manager.js";
import type { AgentConfig, Task, PRReviewContext, PRFollowUpContext, RunOutcome } from "../types.js";

let tmp: string;

beforeEach(() => {
  tmp = mkdtempSync(join(tmpdir(), "vteam-prompt-test-"));
});

afterEach(() => {
  rmSync(tmp, { recursive: true, force: true });
});

function makeTask(overrides: Partial<Task>): Task {
  return {
    ref: "VTM-AAAA",
    title: "Task",
    status: "todo",
    severity: "medium",
    files: [],
    retryCount: 0,
    metadata: {},
    description: "",
    notes: [],
    createdAt: "2026-04-19T00:00:00Z",
    ...overrides,
  };
}

function stubTaskManager(tasks: Task[]): TaskManager {
  return {
    listReady: async () => tasks.filter((t) => t.status === "todo"),
    getIndex: async () => ({
      all: tasks,
      byStatus: new Map(),
      titles: tasks.map((t) => t.title),
    }),
    create: async () => "VTM-NEW1",
    update: async () => {},
    addNote: async () => {},
  };
}

function setup(tasks: Task[] = []): { agentConfig: AgentConfig; taskManager: TaskManager } {
  const agentMdPath = resolve(tmp, "AGENT.md");
  writeFileSync(agentMdPath, "# Test Agent\n\nYou are a test agent.\n", "utf-8");

  const taskManager = stubTaskManager(tasks);

  return {
    agentConfig: {
      name: "test-agent",
      agentMdPath,
      model: "sonnet",
    },
    taskManager,
  };
}

describe("buildPrompt", () => {
  it("includes agent md content as system prompt", async () => {
    const { agentConfig, taskManager } = setup();
    const { systemPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(systemPrompt).toContain("You are a test agent");
  });

  it("includes existing task titles in user prompt", async () => {
    const { agentConfig, taskManager } = setup([
      makeTask({ title: "Null check missing", severity: "high", files: ["src/auth.ts:45"] }),
    ]);

    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).toContain("Null check missing");
    expect(userPrompt).toContain("Existing Tasks");
  });

  it("omits existing tasks section when no tasks exist", async () => {
    const { agentConfig, taskManager } = setup();
    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).not.toContain("Existing Tasks");
  });

  it("includes task details when task provided", async () => {
    const { agentConfig, taskManager } = setup();
    const task = makeTask({
      title: "Fix null check",
      severity: "high",
      files: ["src/auth.ts:45"],
      description: "## Description\n\nThe auth module is broken.",
    });

    const { systemPrompt, userPrompt } = await buildPrompt(agentConfig, taskManager, task);
    expect(systemPrompt).toContain("You are a test agent");
    expect(userPrompt).toContain("Fix null check");
    expect(userPrompt).toContain("high");
    expect(userPrompt).toContain("src/auth.ts:45");
    expect(userPrompt).toContain("auth module is broken");
  });

  it("includes task notes oldest first when the task has notes", async () => {
    const { agentConfig, taskManager } = setup();
    const task = makeTask({
      notes: [
        { body: "coder: blocked\n\nTests would not run.", createdAt: "2026-10-01T00:00:00Z" },
        { body: "Try mocking the DB.", createdAt: "2026-10-02T00:00:00Z" },
      ],
    });

    const { userPrompt } = await buildPrompt(agentConfig, taskManager, task);
    expect(userPrompt).toContain("## Task Notes");
    expect(userPrompt.indexOf("Tests would not run.")).toBeLessThan(userPrompt.indexOf("Try mocking the DB."));
  });

  it("omits the task notes section when the task has no notes", async () => {
    const { agentConfig, taskManager } = setup();
    const { userPrompt } = await buildPrompt(agentConfig, taskManager, makeTask({}));
    expect(userPrompt).not.toContain("## Task Notes");
  });

  it("omits task section when no task provided", async () => {
    const { agentConfig, taskManager } = setup();
    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).not.toContain("## Your Task");
  });

  it("shows tasks of every status with status, severity, and files", async () => {
    const { agentConfig, taskManager } = setup([
      makeTask({ title: "Todo task", status: "todo", severity: "high", files: ["a.ts"] }),
      makeTask({ title: "Rejected task", status: "canceled", severity: "low", files: ["b.ts", "c.ts"] }),
    ]);

    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).toContain("- [todo] high | Todo task | a.ts");
    expect(userPrompt).toContain("- [canceled] low | Rejected task | b.ts, c.ts");
  });

  it("includes review context when provided", async () => {
    const { agentConfig, taskManager } = setup();
    const review: PRReviewContext = {
      pr: {
        number: 42,
        title: "vteam: Fix null check in auth",
        branch: "vteam/fix-null-check",
        url: "https://github.com/org/repo/pull/42",
      },
      repoSlug: "org/repo",
      comments: [
        {
          author: "reviewer1",
          body: "This approach won't work for concurrent requests.",
          path: "src/auth.ts",
          line: 45,
          createdAt: "2026-04-19T10:00:00Z",
        },
        {
          author: "reviewer2",
          body: "Test coverage is insufficient.",
          createdAt: "2026-04-19T11:00:00Z",
        },
      ],
    };

    const { userPrompt } = await buildPrompt(agentConfig, taskManager, undefined, review);
    expect(userPrompt).toContain("Pull Request");
    expect(userPrompt).toContain("#42");
    expect(userPrompt).toContain("org/repo");
    expect(userPrompt).toContain("vteam: Fix null check in auth");
    expect(userPrompt).toContain("vteam/fix-null-check");
    expect(userPrompt).toContain("https://github.com/org/repo/pull/42");
    expect(userPrompt).toContain("reviewer1 on `src/auth.ts:45`");
    expect(userPrompt).toContain("concurrent requests");
    expect(userPrompt).toContain("reviewer2");
    expect(userPrompt).toContain("Test coverage");
  });

  it("includes PR info but omits comments section when review has no comments", async () => {
    const { agentConfig, taskManager } = setup();
    const review: PRReviewContext = {
      pr: {
        number: 42,
        title: "vteam: Fix null check in auth",
        branch: "vteam/fix-null-check",
        url: "https://github.com/org/repo/pull/42",
      },
      repoSlug: "org/repo",
      comments: [],
    };

    const { userPrompt } = await buildPrompt(agentConfig, taskManager, undefined, review);
    expect(userPrompt).toContain("Pull Request");
    expect(userPrompt).toContain("#42");
    expect(userPrompt).toContain("org/repo");
    expect(userPrompt).not.toContain("Review Comments");
  });

  it("includes open review threads for a follow-up on an existing PR", async () => {
    const { agentConfig, taskManager } = setup();
    const followUp: PRFollowUpContext = {
      pr: {
        number: 42,
        title: "vteam: coder: Fix null check in auth",
        branch: "vteam/fix-null-check",
        url: "https://github.com/org/repo/pull/42",
      },
      threads: [
        {
          id: "PRRT_1",
          path: "src/auth.ts",
          line: 45,
          comments: [
            { author: "reviewer1", body: "Guard against an empty email too.", createdAt: "2026-04-19T10:00:00Z" },
            { author: "reviewer2", body: "Agreed.", createdAt: "2026-04-19T11:00:00Z" },
          ],
        },
      ],
    };

    const { userPrompt } = await buildPrompt(agentConfig, taskManager, makeTask({}), followUp);
    expect(userPrompt).toContain("## Existing Pull Request");
    expect(userPrompt).toContain("Branch: vteam/fix-null-check");
    expect(userPrompt).toContain("If the task notes say which of them to apply, apply only those.");
    expect(userPrompt).toContain("### T1 · `src/auth.ts:45`\n\n**reviewer1:** Guard against an empty email too.\n\n**reviewer2:** Agreed.");
    expect(userPrompt.indexOf("## Your Task")).toBeLessThan(userPrompt.indexOf("## Existing Pull Request"));
    expect(userPrompt).toContain('"threadReplies": [{ "thread": "T1"');
    expect(userPrompt).toContain("Do not reply to threads you skipped.");
  });

  it("tells a follow-up to work from the task notes when no review threads are open", async () => {
    const { agentConfig, taskManager } = setup();
    const followUp: PRFollowUpContext = {
      pr: { number: 42, title: "t", branch: "b", url: "https://github.com/org/repo/pull/42" },
      threads: [],
    };

    const { userPrompt } = await buildPrompt(agentConfig, taskManager, makeTask({}), followUp);
    expect(userPrompt).toContain("no unresolved review threads. Work from the task notes.");
    expect(userPrompt).not.toContain("threadReplies");
  });

  it("omits review section when no review provided", async () => {
    const { agentConfig, taskManager } = setup();
    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).not.toContain("Pull Request");
    expect(userPrompt).not.toContain("Review Comments");
  });

  it("includes focus as first section in user prompt", async () => {
    const { agentConfig, taskManager } = setup();
    const { userPrompt } = await buildPrompt(agentConfig, taskManager, undefined, undefined, "the invite user functionality");
    expect(userPrompt).toContain("## Priority Focus");
    expect(userPrompt).toContain("the invite user functionality");
    const focusIndex = userPrompt.indexOf("## Priority Focus");
    const outputIndex = userPrompt.indexOf("## Output Format");
    expect(focusIndex).toBeLessThan(outputIndex);
  });

  it("omits focus section when not provided", async () => {
    const { agentConfig, taskManager } = setup();
    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).not.toContain("Priority Focus");
  });

  it("uses unified output schema with status for all agents", async () => {
    const { agentConfig, taskManager } = setup();
    agentConfig.worktree = true;
    agentConfig.readOnly = true;
    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).toContain('"status"');
    expect(userPrompt).toContain('"summary"');
  });

  it("uses task content schema when output is task", async () => {
    const { agentConfig, taskManager } = setup();
    agentConfig.output = "task";
    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).toContain('"type": "task"');
    expect(userPrompt).toContain('"severity"');
    expect(userPrompt).toContain('"title"');
  });

  it("uses generic content schema by default (no output field)", async () => {
    const { agentConfig, taskManager } = setup();
    agentConfig.output = undefined;
    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).toContain('"type": "generic"');
    expect(userPrompt).toContain('"status"');
  });

  it("uses task content schema for worktree agent with output task", async () => {
    const { agentConfig, taskManager } = setup();
    agentConfig.worktree = true;
    agentConfig.output = "task";
    const { userPrompt } = await buildPrompt(agentConfig, taskManager);
    expect(userPrompt).toContain('"type": "task"');
  });
});

describe("buildOnFinishPrompt", () => {
  function setupOnFinish(): string {
    const onFinishPath = resolve(tmp, "ON_FINISH.md");
    writeFileSync(
      onFinishPath,
      "Post a Slack notification with the run result.",
      "utf-8",
    );
    return onFinishPath;
  }

  it("uses ON_FINISH.md body as system prompt", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "coder",
      status: "completed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:05:00Z",
    };
    const { systemPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(systemPrompt).toBe("Post a Slack notification with the run result.");
  });

  it("includes run outcome in user prompt", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "coder",
      status: "completed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:05:00Z",
    };
    const { userPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(userPrompt).toContain("coder");
    expect(userPrompt).toContain("completed");
    expect(userPrompt).toContain("2026-04-21T10:00:00Z");
  });

  it("includes task details when present", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "coder",
      status: "completed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:05:00Z",
      task: { title: "Fix null check", severity: "high", files: ["src/auth.ts:45"] },
      branch: "vteam/fix-null-check",
      prUrl: "https://github.com/org/repo/pull/42",
    };
    const { userPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(userPrompt).toContain("Fix null check");
    expect(userPrompt).toContain("high");
    expect(userPrompt).toContain("src/auth.ts:45");
    expect(userPrompt).toContain("vteam/fix-null-check");
    expect(userPrompt).toContain("https://github.com/org/repo/pull/42");
  });

  it("includes error on failure", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "coder",
      status: "failed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:01:00Z",
      error: "Claude exited with code 1",
    };
    const { userPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(userPrompt).toContain("failed");
    expect(userPrompt).toContain("Claude exited with code 1");
  });

  it("includes reviewed PR details", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "coder",
      status: "completed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:05:00Z",
      reviewedPR: { number: 42, title: "Fix auth", url: "https://github.com/org/repo/pull/42" },
    };
    const { userPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(userPrompt).toContain("Reviewed PR");
    expect(userPrompt).toContain("#42");
    expect(userPrompt).toContain("Fix auth");
  });

  it("omits optional sections when not present", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "improver",
      status: "completed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:05:00Z",
    };
    const { userPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(userPrompt).not.toContain("## Task");
    expect(userPrompt).not.toContain("## Branch");
    expect(userPrompt).not.toContain("## Error");
    expect(userPrompt).not.toContain("## Reviewed PR");
    expect(userPrompt).not.toContain("## Content");
  });

  it("includes generic content when present", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "pr-reviewer",
      status: "completed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:05:00Z",
      content: { type: "generic", body: "## Review\n\nLooks good overall." },
    };
    const { userPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(userPrompt).toContain("## Content");
    expect(userPrompt).toContain("Looks good overall");
  });

  it("includes task content as JSON when present", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "improver",
      status: "completed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:05:00Z",
      content: {
        type: "task",
        body: {
          title: "Missing null check",
          severity: "high",
          description: "Potential NPE",
          files: ["src/auth.ts:45"],
        },
      },
    };
    const { userPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(userPrompt).toContain("## Content");
    expect(userPrompt).toContain("Missing null check");
  });

  it("includes focus context when present", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "sentry-debugger",
      status: "completed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:05:00Z",
      focus: "https://sentry.io/issues/123",
    };
    const { userPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(userPrompt).toContain("## Focus Context");
    expect(userPrompt).toContain("https://sentry.io/issues/123");
  });

  it("omits focus context when not present", () => {
    const path = setupOnFinish();
    const outcome: RunOutcome = {
      agent: "improver",
      status: "completed",
      startedAt: "2026-04-21T10:00:00Z",
      completedAt: "2026-04-21T10:05:00Z",
    };
    const { userPrompt } = buildOnFinishPrompt({ onFinishMdPath: path }, outcome);
    expect(userPrompt).not.toContain("Focus Context");
  });
});

describe("buildMemoryCurationPrompt", () => {
  function setupMemory(): string {
    const memoryPath = resolve(tmp, "MEMORY.md");
    writeFileSync(
      memoryPath,
      "Keep a running list of scanned areas.",
      "utf-8",
    );
    return memoryPath;
  }

  it("includes focus as run context when provided", () => {
    const path = setupMemory();
    const { userPrompt } = buildMemoryCurationPrompt(
      { memoryMdPath: path },
      "Previously scanned auth/",
      "Scanned payments/",
      "https://sentry.io/issues/789",
    );
    expect(userPrompt).toContain("## Run Context");
    expect(userPrompt).toContain("https://sentry.io/issues/789");
  });

  it("omits run context when no focus provided", () => {
    const path = setupMemory();
    const { userPrompt } = buildMemoryCurationPrompt(
      { memoryMdPath: path },
      "Previously scanned auth/",
      "Scanned payments/",
    );
    expect(userPrompt).not.toContain("Run Context");
  });

  it("places run context before current memory", () => {
    const path = setupMemory();
    const { userPrompt } = buildMemoryCurationPrompt(
      { memoryMdPath: path },
      "Previously scanned auth/",
      "Scanned payments/",
      "debug sentry error",
    );
    const contextIndex = userPrompt.indexOf("## Run Context");
    const memoryIndex = userPrompt.indexOf("## Current Memory");
    expect(contextIndex).toBeLessThan(memoryIndex);
  });
});
