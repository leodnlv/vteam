import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import {
  mkdtempSync,
  mkdirSync,
  writeFileSync,
  readFileSync,
  existsSync,
  rmSync,
} from "node:fs";
import { join, resolve } from "node:path";
import { tmpdir } from "node:os";
import { execFileSync } from "node:child_process";
import type { AgentRunResult } from "../orchestrator/agent-runner.js";
import type { TaskManager, TaskUpdate } from "../tasks/task-manager.js";
import type { PullRequestInfo, ReviewThread, Task, TaskContentBody } from "../types.js";

vi.mock("../orchestrator/agent-runner.js", () => ({
  runClaudeAgent: vi.fn(),
}));

const taskStore = vi.hoisted(() => ({
  tasks: [] as Task[],
  created: [] as { body: TaskContentBody; foundBy: string }[],
  updates: [] as { ref: string; changes: TaskUpdate }[],
  notes: [] as { ref: string; text: string }[],
  failNotes: false,
}));

vi.mock("../tasks/aven-manager.js", () => ({
  AvenTaskManager: class implements TaskManager {
    async listReady() {
      const ready = taskStore.tasks.filter((t) => t.status === "todo");
      await new Promise((r) => setTimeout(r, 10));
      return ready;
    }
    async getIndex() {
      return {
        all: taskStore.tasks,
        byStatus: new Map(),
        titles: taskStore.tasks.map((t) => t.title),
      };
    }
    async create(body: TaskContentBody, foundBy: string) {
      taskStore.created.push({ body, foundBy });
      return `VTM-${taskStore.created.length}`;
    }
    async update(ref: string, changes: TaskUpdate) {
      taskStore.updates.push({ ref, changes });
      const task = taskStore.tasks.find((t) => t.ref === ref);
      if (task && changes.status) task.status = changes.status;
    }
    async addNote(ref: string, text: string) {
      if (taskStore.failNotes) throw new Error("aven note failed");
      taskStore.notes.push({ ref, text });
    }
  },
}));

vi.mock("../worktree/manager.js", () => ({
  createWorktree: vi.fn(),
  checkoutWorktree: vi.fn(),
  removeWorktree: vi.fn(),
  pushBranch: vi.fn(),
  getCommitSha: vi.fn(() => "abc1234def"),
}));

vi.mock("../integrations/pull-request.js", () => ({
  findReviewablePR: vi.fn(),
  getReviewComments: vi.fn(),
  getRepoSlug: vi.fn(),
  postPRComment: vi.fn(),
  removePRLabel: vi.fn(),
  getPullRequest: vi.fn(),
  getOpenReviewThreads: vi.fn(),
  replyToReviewThread: vi.fn(),
  resolveReviewThread: vi.fn(),
}));

vi.mock("../integrations/merge-request.js", () => ({
  createMergeRequest: vi.fn(() => "https://github.com/org/repo/pull/99"),
}));

import { runClaudeAgent } from "../orchestrator/agent-runner.js";
import { checkoutWorktree, createWorktree, pushBranch } from "../worktree/manager.js";
import {
  getOpenReviewThreads,
  getPullRequest,
  replyToReviewThread,
  resolveReviewThread,
} from "../integrations/pull-request.js";
import { createMergeRequest } from "../integrations/merge-request.js";
import { runCommand } from "./run.js";

const mockRunClaude = vi.mocked(runClaudeAgent);

let tmp: string;

function makeResult(
  resultText: string | null,
  exitCode = 0,
): AgentRunResult {
  return {
    stdout: "",
    stderr: "",
    exitCode,
    resultText,
    costUsd: 0.01,
    durationMs: 1000,
  };
}

function setupProject(opts: {
  hasOnFinish?: boolean;
  hasMemory?: boolean;
  existingMemory?: string;
  agentConfig?: Record<string, unknown>;
} = {}): void {
  const {
    hasOnFinish = false,
    hasMemory = false,
    existingMemory,
    agentConfig = { model: "sonnet" },
  } = opts;

  const vteamDir = resolve(tmp, "vteam");
  mkdirSync(vteamDir, { recursive: true });
  writeFileSync(
    resolve(vteamDir, "vteam.config.json"),
    JSON.stringify({
      baseBranch: "main",
      platform: "github",
      worktreeDir: ".worktrees",
      tasks: { maxRetries: 3, project: "test" },
    }),
  );

  const agentDir = resolve(vteamDir, "agents", "test-agent");
  mkdirSync(agentDir, { recursive: true });
  writeFileSync(
    resolve(agentDir, "agent.json"),
    JSON.stringify({
      ...agentConfig,
      ...(hasOnFinish ? { onFinish: { model: "haiku" } } : {}),
      ...(hasMemory ? { memory: { model: "haiku" } } : {}),
    }),
  );
  writeFileSync(resolve(agentDir, "AGENT.md"), "You are a test agent.");

  if (hasOnFinish) {
    writeFileSync(
      resolve(agentDir, "ON_FINISH.md"),
      "Post notification.",
    );
  }

  if (hasMemory) {
    writeFileSync(
      resolve(agentDir, "MEMORY.md"),
      "Keep a running list of scanned areas.",
    );
  }

  if (existingMemory) {
    const memDir = resolve(vteamDir, ".memory", "test-agent");
    mkdirSync(memDir, { recursive: true });
    writeFileSync(resolve(memDir, "store.md"), existingMemory);
  }
}

beforeEach(() => {
  tmp = mkdtempSync(join(tmpdir(), "vteam-pipeline-test-"));
  vi.spyOn(process, "cwd").mockReturnValue(tmp);
  vi.spyOn(process, "exit").mockImplementation(() => undefined as never);
  vi.spyOn(console, "log").mockImplementation(() => {});
  vi.spyOn(console, "error").mockImplementation(() => {});
  mockRunClaude.mockReset();
  vi.mocked(checkoutWorktree).mockReset();
  vi.mocked(createWorktree).mockReset();
  vi.mocked(pushBranch).mockReset();
  vi.mocked(getPullRequest).mockReset();
  vi.mocked(getOpenReviewThreads).mockReset();
  vi.mocked(replyToReviewThread).mockReset();
  vi.mocked(resolveReviewThread).mockReset();
  vi.mocked(createMergeRequest).mockClear();
  taskStore.tasks = [];
  taskStore.created = [];
  taskStore.updates = [];
  taskStore.notes = [];
  taskStore.failNotes = false;
});

afterEach(() => {
  vi.restoreAllMocks();
  rmSync(tmp, { recursive: true, force: true });
});

describe("pipeline: agent output → ON_FINISH", () => {
  it("passes generic content to ON_FINISH hook", async () => {
    setupProject({ hasOnFinish: true });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Review done.",
            content: { type: "generic", body: "All looks good." },
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("hook ok"));

    await runCommand("test-agent", {});

    expect(mockRunClaude).toHaveBeenCalledTimes(2);

    const onFinishCall = mockRunClaude.mock.calls[1][0];
    expect(onFinishCall.userPrompt).toContain("completed");
    expect(onFinishCall.userPrompt).toContain("All looks good.");
    expect(onFinishCall.systemPrompt).toContain("Post notification.");
  });

  it("passes task content as JSON to ON_FINISH hook", async () => {
    setupProject({
      hasOnFinish: true,
      agentConfig: { model: "sonnet", output: "task" },
    });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Found issue.",
            content: {
              type: "task",
              body: {
                title: "Null pointer in auth",
                severity: "high",
                description: "NPE when user.email is null",
                files: ["src/auth.ts:45"],
              },
            },
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("hook ok"));

    await runCommand("test-agent", {});

    expect(mockRunClaude).toHaveBeenCalledTimes(2);

    const onFinishCall = mockRunClaude.mock.calls[1][0];
    expect(onFinishCall.userPrompt).toContain("Null pointer in auth");
    expect(onFinishCall.userPrompt).toContain("high");
  });

  it("runs ON_FINISH with error details when agent fails", async () => {
    setupProject({ hasOnFinish: true });

    mockRunClaude
      .mockResolvedValueOnce(makeResult(null, 1))
      .mockResolvedValueOnce(makeResult("hook ok"));

    await runCommand("test-agent", {});

    expect(mockRunClaude).toHaveBeenCalledTimes(2);

    const onFinishCall = mockRunClaude.mock.calls[1][0];
    expect(onFinishCall.userPrompt).toContain("failed");
    expect(onFinishCall.userPrompt).toContain("Claude exited with code 1");
    expect(process.exit).toHaveBeenCalledWith(1);
  });

  it("includes branch and PR info in ON_FINISH when present in outcome", async () => {
    setupProject({ hasOnFinish: true });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Done.",
            content: { type: "generic", body: "Result." },
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("hook ok"));

    await runCommand("test-agent", {});

    const onFinishCall = mockRunClaude.mock.calls[1][0];
    expect(onFinishCall.userPrompt).toContain("test-agent");
    expect(onFinishCall.userPrompt).toContain("completed");
  });
});

describe("pipeline: agent output → MEMORY curation", () => {
  it("passes memoryUpdate to curation agent", async () => {
    setupProject({ hasMemory: true });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Scanned auth module.",
            memoryUpdate: "Scanned src/auth.ts — found singleton pattern.",
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("Updated memory content."));

    await runCommand("test-agent", {});

    expect(mockRunClaude).toHaveBeenCalledTimes(2);

    const memoryCall = mockRunClaude.mock.calls[1][0];
    expect(memoryCall.userPrompt).toContain(
      "Scanned src/auth.ts — found singleton pattern.",
    );
    expect(memoryCall.userPrompt).toContain("## New Update");
    expect(memoryCall.systemPrompt).toContain(
      "Keep a running list of scanned areas.",
    );
  });

  it("writes curation result to store.md", async () => {
    setupProject({ hasMemory: true });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Done.",
            memoryUpdate: "New info.",
          }),
        ),
      )
      .mockResolvedValueOnce(
        makeResult("- Scanned auth module\n- Found singleton pattern"),
      );

    await runCommand("test-agent", {});

    const storeFile = resolve(
      tmp,
      "vteam",
      ".memory",
      "test-agent",
      "store.md",
    );
    expect(existsSync(storeFile)).toBe(true);
    const content = readFileSync(storeFile, "utf-8");
    expect(content).toContain("Scanned auth module");
    expect(content).toContain("Found singleton pattern");
  });

  it("includes existing memory in curation prompt", async () => {
    setupProject({
      hasMemory: true,
      existingMemory: "Previously: scanned foo/",
    });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Done.",
            memoryUpdate: "Now: scanned bar/",
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("Merged memory."));

    await runCommand("test-agent", {});

    const memoryCall = mockRunClaude.mock.calls[1][0];
    expect(memoryCall.userPrompt).toContain("Previously: scanned foo/");
    expect(memoryCall.userPrompt).toContain("Now: scanned bar/");
  });

  it("skips curation when agent returns no memoryUpdate", async () => {
    setupProject({ hasMemory: true });

    mockRunClaude.mockResolvedValueOnce(
      makeResult(
        JSON.stringify({ status: "completed", summary: "Done." }),
      ),
    );

    await runCommand("test-agent", {});

    expect(mockRunClaude).toHaveBeenCalledTimes(1);
  });

  it("skips curation when agent has no MEMORY.md even if memoryUpdate present", async () => {
    setupProject({ hasMemory: false });

    mockRunClaude.mockResolvedValueOnce(
      makeResult(
        JSON.stringify({
          status: "completed",
          summary: "Done.",
          memoryUpdate: "Something to remember.",
        }),
      ),
    );

    await runCommand("test-agent", {});

    expect(mockRunClaude).toHaveBeenCalledTimes(1);
  });
});

describe("pipeline: agent output → task creation", () => {
  it("creates a task when agent returns task content", async () => {
    setupProject({
      agentConfig: { model: "sonnet", output: "task" },
    });

    mockRunClaude.mockResolvedValueOnce(
      makeResult(
        JSON.stringify({
          status: "completed",
          summary: "Found issue.",
          content: {
            type: "task",
            body: {
              title: "Missing error handling",
              severity: "medium",
              description: "No try-catch around DB call",
              files: ["src/db.ts:20"],
            },
          },
        }),
      ),
    );

    await runCommand("test-agent", {});

    expect(taskStore.created).toEqual([
      {
        body: {
          title: "Missing error handling",
          severity: "medium",
          description: "No try-catch around DB call",
          files: ["src/db.ts:20"],
        },
        foundBy: "test-agent",
      },
    ]);
  });

  it("does not create a task for generic content", async () => {
    setupProject();

    mockRunClaude.mockResolvedValueOnce(
      makeResult(
        JSON.stringify({
          status: "completed",
          summary: "Done.",
          content: { type: "generic", body: "All good." },
        }),
      ),
    );

    await runCommand("test-agent", {});

    expect(taskStore.created).toEqual([]);
  });
});

describe("pipeline: task input lifecycle", () => {
  function seedTask(overrides: Partial<Task> = {}): Task {
    const task: Task = {
      ref: "VTM-ABCD",
      title: "Fix the thing",
      status: "todo",
      severity: "high",
      files: ["src/thing.ts"],
      retryCount: 0,
      metadata: {},
      description: "It is broken.",
      notes: [],
      createdAt: "2026-10-01T00:00:00Z",
      ...overrides,
    };
    taskStore.tasks.push(task);
    return task;
  }

  it("marks the picked task active, then returns it to todo when not settled", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });
    seedTask();

    mockRunClaude.mockResolvedValueOnce(
      makeResult(JSON.stringify({ status: "completed", summary: "Looked." })),
    );

    await runCommand("test-agent", {});

    expect(taskStore.updates.map((u) => u.changes.status)).toEqual(["active", "todo"]);
    expect(mockRunClaude.mock.calls[0][0].userPrompt).toContain("It is broken.");
  });

  it("claims the task with run metadata and clears the claim on release", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });
    seedTask();

    mockRunClaude.mockResolvedValueOnce(
      makeResult(JSON.stringify({ status: "completed", summary: "Looked." })),
    );

    await runCommand("test-agent", {});

    const [claim, release] = taskStore.updates.map((u) => u.changes);
    expect(claim.metadata).toMatchObject({
      "claimed-by": expect.stringMatching(/-test-agent$/),
      "claimed-pid": String(process.pid),
    });
    expect(release.removeMetadata).toEqual(["claimed-by", "claimed-pid", "claimed-host"]);
  });

  it("releases the claim lock before the agent runs", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });
    seedTask();

    const claimLock = resolve(tmp, "vteam", ".locks", "task-claim.lock");
    let claimLockHeld: boolean | undefined;
    mockRunClaude.mockImplementationOnce(async () => {
      claimLockHeld = existsSync(claimLock);
      return makeResult(JSON.stringify({ status: "completed", summary: "Looked." }));
    });

    await runCommand("test-agent", {});

    expect(claimLockHeld).toBe(false);
  });

  it("never hands the same task to two agents running at once", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });
    const otherDir = resolve(tmp, "vteam", "agents", "other-agent");
    mkdirSync(otherDir, { recursive: true });
    writeFileSync(resolve(otherDir, "agent.json"), JSON.stringify({ model: "sonnet", input: "task" }));
    writeFileSync(resolve(otherDir, "AGENT.md"), "Other.");
    seedTask();

    let openGate!: () => void;
    const gate = new Promise<void>((r) => (openGate = r));
    mockRunClaude.mockImplementation(async () => {
      await gate;
      return makeResult(JSON.stringify({ status: "completed", summary: "Looked." }));
    });

    const runs = [runCommand("test-agent", {}), runCommand("other-agent", {})];
    await Promise.race([...runs, new Promise((r) => setTimeout(r, 3000))]);
    openGate();
    await Promise.all(runs);

    expect(taskStore.updates.filter((u) => u.changes.status === "active")).toHaveLength(1);
    expect(mockRunClaude).toHaveBeenCalledTimes(1);
  });

  it("notes the agent's summary and blocker reason on the task", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });
    seedTask();

    mockRunClaude.mockResolvedValueOnce(
      makeResult(JSON.stringify({ status: "blocked", summary: "Looked.", blockerReason: "No DB." })),
    );

    await runCommand("test-agent", {});

    expect(taskStore.notes).toEqual([
      { ref: "VTM-ABCD", text: "test-agent: blocked\n\nLooked.\n\nBlocker: No DB." },
    ]);
  });

  it("still releases the task when the note cannot be written", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });
    seedTask();
    taskStore.failNotes = true;

    mockRunClaude.mockResolvedValueOnce(
      makeResult(JSON.stringify({ status: "completed", summary: "Looked." })),
    );

    await runCommand("test-agent", {});

    expect(taskStore.updates.map((u) => u.changes.status)).toEqual(["active", "todo"]);
    expect(process.exit).not.toHaveBeenCalled();
  });

  it("returns the task to todo when the agent fails", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });
    seedTask();

    mockRunClaude.mockResolvedValueOnce(makeResult(null, 1));

    await runCommand("test-agent", {});

    expect(taskStore.updates.map((u) => u.changes.status)).toEqual(["active", "todo"]);
    expect(taskStore.notes).toEqual([]);
  });

  it("picks the highest severity task and skips tasks over the retry limit", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });
    seedTask({ ref: "VTM-LOW1", severity: "low" });
    seedTask({ ref: "VTM-CRIT", severity: "critical", retryCount: 3 });
    seedTask({ ref: "VTM-HIGH", severity: "high" });

    mockRunClaude.mockResolvedValueOnce(
      makeResult(JSON.stringify({ status: "completed", summary: "Done." })),
    );

    await runCommand("test-agent", {});

    expect(taskStore.updates[0]).toMatchObject({ ref: "VTM-HIGH", changes: { status: "active" } });
  });

  async function pickedRef(): Promise<string | undefined> {
    mockRunClaude.mockResolvedValueOnce(
      makeResult(JSON.stringify({ status: "completed", summary: "Done." })),
    );
    await runCommand("test-agent", {});
    return taskStore.updates.find((u) => u.changes.status === "active")?.ref;
  }

  it("picks only tasks matching its taskFilter, ORing list values", async () => {
    setupProject({
      agentConfig: { model: "sonnet", input: "task", taskFilter: { "found-by": ["improver", "security-analyst"] } },
    });
    seedTask({ ref: "VTM-OTHR", severity: "critical", metadata: { "found-by": "human" } });
    seedTask({ ref: "VTM-SECU", severity: "low", metadata: { "found-by": "security-analyst" } });

    expect(await pickedRef()).toBe("VTM-SECU");
  });

  it("matches a missing metadata key with null", async () => {
    setupProject({
      agentConfig: { model: "sonnet", input: "task", taskFilter: { actor: null } },
    });
    seedTask({ ref: "VTM-REVW", severity: "critical", metadata: { actor: "mr-reviewer" } });
    seedTask({ ref: "VTM-FREE", severity: "low" });

    expect(await pickedRef()).toBe("VTM-FREE");
  });

  it("picks every task when it has no taskFilter", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });
    seedTask({ ref: "VTM-REVW", severity: "critical", metadata: { actor: "mr-reviewer" } });

    expect(await pickedRef()).toBe("VTM-REVW");
  });

  it("stays idle when no ready task matches its taskFilter", async () => {
    setupProject({
      agentConfig: { model: "sonnet", input: "task", taskFilter: { actor: "mr-reviewer" } },
    });
    seedTask({ ref: "VTM-OTHR", metadata: { "found-by": "improver" } });

    await runCommand("test-agent", {});

    expect(mockRunClaude).not.toHaveBeenCalled();
    expect(taskStore.updates).toEqual([]);
  });

  it("does nothing when no task is ready", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task" } });

    await runCommand("test-agent", {});

    expect(mockRunClaude).not.toHaveBeenCalled();
    expect(taskStore.updates).toEqual([]);
  });
});

describe("pipeline: combined ON_FINISH + MEMORY", () => {
  it("runs both ON_FINISH and memory curation when both configured", async () => {
    setupProject({ hasOnFinish: true, hasMemory: true });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Done.",
            content: { type: "generic", body: "Review report." },
            memoryUpdate: "Scanned module X.",
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("hook ok"))
      .mockResolvedValueOnce(makeResult("Updated memory."));

    await runCommand("test-agent", {});

    expect(mockRunClaude).toHaveBeenCalledTimes(3);

    const onFinishCall = mockRunClaude.mock.calls[1][0];
    expect(onFinishCall.userPrompt).toContain("Review report.");

    const memoryCall = mockRunClaude.mock.calls[2][0];
    expect(memoryCall.userPrompt).toContain("Scanned module X.");
  });

  it("runs ON_FINISH even when memory curation is skipped", async () => {
    setupProject({ hasOnFinish: true, hasMemory: true });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Done.",
            content: { type: "generic", body: "Report." },
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("hook ok"));

    await runCommand("test-agent", {});

    expect(mockRunClaude).toHaveBeenCalledTimes(2);
  });

  it("makes no extra calls when neither ON_FINISH nor MEMORY configured", async () => {
    setupProject();

    mockRunClaude.mockResolvedValueOnce(
      makeResult(
        JSON.stringify({ status: "completed", summary: "Done." }),
      ),
    );

    await runCommand("test-agent", {});

    expect(mockRunClaude).toHaveBeenCalledTimes(1);
  });
});

describe("pipeline: focus flows to ON_FINISH and MEMORY", () => {
  it("passes focus to ON_FINISH hook", async () => {
    setupProject({ hasOnFinish: true });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Investigated error.",
            content: { type: "generic", body: "Root cause: missing null check." },
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("hook ok"));

    await runCommand("test-agent", { focus: "https://sentry.io/issues/123" });

    expect(mockRunClaude).toHaveBeenCalledTimes(2);

    const onFinishCall = mockRunClaude.mock.calls[1][0];
    expect(onFinishCall.userPrompt).toContain("Focus Context");
    expect(onFinishCall.userPrompt).toContain("https://sentry.io/issues/123");
  });

  it("passes focus to memory curation", async () => {
    setupProject({ hasMemory: true });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Done.",
            memoryUpdate: "Investigated auth error.",
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("Updated memory."));

    await runCommand("test-agent", { focus: "https://sentry.io/issues/456" });

    expect(mockRunClaude).toHaveBeenCalledTimes(2);

    const memoryCall = mockRunClaude.mock.calls[1][0];
    expect(memoryCall.userPrompt).toContain("Run Context");
    expect(memoryCall.userPrompt).toContain("https://sentry.io/issues/456");
  });

  it("omits focus sections when no focus provided", async () => {
    setupProject({ hasOnFinish: true, hasMemory: true });

    mockRunClaude
      .mockResolvedValueOnce(
        makeResult(
          JSON.stringify({
            status: "completed",
            summary: "Done.",
            content: { type: "generic", body: "Report." },
            memoryUpdate: "Scanned X.",
          }),
        ),
      )
      .mockResolvedValueOnce(makeResult("hook ok"))
      .mockResolvedValueOnce(makeResult("Updated memory."));

    await runCommand("test-agent", {});

    const onFinishCall = mockRunClaude.mock.calls[1][0];
    expect(onFinishCall.userPrompt).not.toContain("Focus Context");

    const memoryCall = mockRunClaude.mock.calls[2][0];
    expect(memoryCall.userPrompt).not.toContain("Run Context");
  });
});

describe("pipeline: existing memory flows to agent prompt", () => {
  it("injects existing memory into agent's user prompt", async () => {
    setupProject({
      hasMemory: true,
      existingMemory: "Previous run: scanned auth/",
    });

    mockRunClaude.mockResolvedValueOnce(
      makeResult(
        JSON.stringify({ status: "completed", summary: "Done." }),
      ),
    );

    await runCommand("test-agent", {});

    const agentCall = mockRunClaude.mock.calls[0][0];
    expect(agentCall.userPrompt).toContain("Previous run: scanned auth/");
    expect(agentCall.userPrompt).toContain("Agent Memory");
  });
});

describe("pipeline: follow-up on a task's existing PR", () => {
  const prUrl = "https://github.com/org/repo/pull/7";
  const prBranch = "vteam/fix-the-thing-20261001-000000-coder";

  function seedDoneTaskReopened(): void {
    taskStore.tasks.push({
      ref: "VTM-FLUP",
      title: "Fix the thing",
      status: "todo",
      severity: "high",
      files: ["src/thing.ts"],
      retryCount: 0,
      metadata: { branch: prBranch, "pr-url": prUrl, completed: "2026-10-01T00:00:00Z" },
      description: "It is broken.",
      notes: [{ body: "Apply the null-check thread, skip the rename.", createdAt: "2026-10-02T00:00:00Z" }],
      createdAt: "2026-10-01T00:00:00Z",
    });
  }

  function makeGitWorktree(branch: string): { path: string; branch: string } {
    const path = resolve(tmp, ".worktrees", branch);
    mkdirSync(path, { recursive: true });
    const git = (...args: string[]) => execFileSync("git", args, { cwd: path, stdio: "pipe" });
    git("init", "-q");
    git("config", "user.email", "test@example.com");
    git("config", "user.name", "test");
    git("commit", "-q", "--allow-empty", "-m", "init");
    return { path, branch };
  }

  function mockPR(state: PullRequestInfo["state"]): void {
    vi.mocked(getPullRequest).mockReturnValue({
      number: 7,
      title: "vteam: coder: Fix the thing",
      branch: prBranch,
      url: prUrl,
      state,
    });
  }

  function mockAgentEditing(extra: Record<string, unknown> = {}, edits = true): void {
    mockRunClaude.mockImplementationOnce(async ({ cwd }) => {
      if (edits) writeFileSync(resolve(cwd, "thing.ts"), "fixed");
      return makeResult(JSON.stringify({
        status: "completed",
        summary: "Added the null check.",
        filesChanged: ["thing.ts"],
        commitMessage: { subject: "vteam: guard null email", body: "Review follow-up." },
        ...extra,
      }));
    });
  }

  const threads: ReviewThread[] = [
    {
      id: "PRRT_null",
      path: "src/thing.ts",
      line: 12,
      comments: [{ author: "alice", body: "Missing null check here.", createdAt: "2026-10-01T12:00:00Z" }],
    },
  ];

  it("checks out the PR branch, prompts with open threads, and pushes without opening a new PR", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task", worktree: true, autoPR: true } });
    seedDoneTaskReopened();
    mockPR("open");
    vi.mocked(getOpenReviewThreads).mockReturnValue(threads);
    vi.mocked(checkoutWorktree).mockImplementation((_root, branch) => makeGitWorktree(branch));
    mockAgentEditing();

    await runCommand("test-agent", {});

    expect(getPullRequest).toHaveBeenCalledWith("github", prUrl, tmp);
    expect(checkoutWorktree).toHaveBeenCalledWith(tmp, prBranch, ".worktrees");
    expect(createWorktree).not.toHaveBeenCalled();

    const prompt = mockRunClaude.mock.calls[0][0].userPrompt;
    expect(prompt).toContain("## Existing Pull Request");
    expect(prompt).toContain("**alice:** Missing null check here.");
    expect(prompt).toContain("Apply the null-check thread, skip the rename.");

    expect(pushBranch).toHaveBeenCalledWith(expect.any(String), prBranch);
    expect(createMergeRequest).not.toHaveBeenCalled();

    const done = taskStore.updates.find((u) => u.changes.status === "done");
    expect(done?.changes.metadata).toMatchObject({ branch: prBranch, "pr-url": prUrl });
    expect(taskStore.notes[0].text).toContain(`PR: ${prUrl}`);
  });

  it.each(["merged", "closed"] as const)("moves the task to backlog without running when the PR is %s", async (state) => {
    setupProject({ agentConfig: { model: "sonnet", input: "task", worktree: true, autoPR: true } });
    seedDoneTaskReopened();
    mockPR(state);

    await runCommand("test-agent", {});

    expect(mockRunClaude).not.toHaveBeenCalled();
    expect(checkoutWorktree).not.toHaveBeenCalled();
    expect(taskStore.updates.map((u) => u.changes.status)).toEqual(["active", "backlog"]);
    expect(taskStore.updates[1].changes.removeMetadata).toEqual(["claimed-by", "claimed-pid", "claimed-host"]);
    expect(taskStore.notes[0].text).toContain(`PR ${prUrl} is ${state}`);
    expect(process.exit).not.toHaveBeenCalled();
  });

  it("starts a new branch and PR when the task has no pr-url", async () => {
    setupProject({ agentConfig: { model: "sonnet", input: "task", worktree: true, autoPR: true } });
    taskStore.tasks.push({
      ref: "VTM-NEW1",
      title: "Fix the thing",
      status: "todo",
      severity: "high",
      files: [],
      retryCount: 0,
      metadata: {},
      description: "It is broken.",
      notes: [],
      createdAt: "2026-10-01T00:00:00Z",
    });
    vi.mocked(createWorktree).mockImplementation(() => makeGitWorktree("vteam/fix-the-thing-new"));
    mockAgentEditing();

    await runCommand("test-agent", {});

    expect(getPullRequest).not.toHaveBeenCalled();
    expect(checkoutWorktree).not.toHaveBeenCalled();
    expect(createMergeRequest).toHaveBeenCalledOnce();
    expect(mockRunClaude.mock.calls[0][0].userPrompt).not.toContain("Existing Pull Request");
  });

  describe("thread replies", () => {
    const replies = {
      threadReplies: [
        { thread: "T1", body: "Added the null check." },
        { thread: "T9", body: "No such thread." },
      ],
    };

    function setupFollowUp(agentConfig: Record<string, unknown> = {}): void {
      setupProject({ agentConfig: { model: "sonnet", input: "task", worktree: true, ...agentConfig } });
      seedDoneTaskReopened();
      mockPR("open");
      vi.mocked(getOpenReviewThreads).mockReturnValue(threads);
      vi.mocked(checkoutWorktree).mockImplementation((_root, branch) => makeGitWorktree(branch));
    }

    it("asks for replies by thread label", async () => {
      setupFollowUp();
      mockAgentEditing();

      await runCommand("test-agent", {});

      const prompt = mockRunClaude.mock.calls[0][0].userPrompt;
      expect(prompt).toContain("### T1 · `src/thing.ts:12`");
      expect(prompt).toContain('"threadReplies"');
    });

    it("posts annotated replies to known threads after pushing, and notes the count", async () => {
      setupFollowUp();
      mockAgentEditing(replies);

      await runCommand("test-agent", {});

      expect(replyToReviewThread).toHaveBeenCalledOnce();
      expect(replyToReviewThread).toHaveBeenCalledWith(
        "github",
        7,
        "PRRT_null",
        "🤖 **vteam test-agent:** Added the null check.\n\nFixed in abc1234.",
        tmp,
      );
      expect(vi.mocked(pushBranch).mock.invocationCallOrder[0])
        .toBeLessThan(vi.mocked(replyToReviewThread).mock.invocationCallOrder[0]);
      expect(resolveReviewThread).not.toHaveBeenCalled();
      expect(taskStore.notes[0].text).toContain("Replied to 1 of 2 review threads.");
    });

    it("resolves replied threads when resolveThreads is on", async () => {
      setupFollowUp({ resolveThreads: true });
      mockAgentEditing(replies);

      await runCommand("test-agent", {});

      expect(resolveReviewThread).toHaveBeenCalledOnce();
      expect(resolveReviewThread).toHaveBeenCalledWith("github", 7, "PRRT_null", tmp);
    });

    it("posts no replies when nothing was committed", async () => {
      setupFollowUp({ resolveThreads: true });
      mockAgentEditing(replies, false);

      await runCommand("test-agent", {});

      expect(pushBranch).not.toHaveBeenCalled();
      expect(replyToReviewThread).not.toHaveBeenCalled();
      expect(resolveReviewThread).not.toHaveBeenCalled();
    });

    it("finishes the task when a reply cannot be posted", async () => {
      setupFollowUp({ resolveThreads: true });
      vi.mocked(replyToReviewThread).mockImplementation(() => {
        throw new Error("gh: forbidden");
      });
      mockAgentEditing(replies);

      await runCommand("test-agent", {});

      expect(resolveReviewThread).not.toHaveBeenCalled();
      expect(taskStore.updates.at(-1)?.changes.status).toBe("done");
      expect(taskStore.notes[0].text).toContain("Replied to 0 of 2 review threads.");
      expect(process.exit).not.toHaveBeenCalled();
    });
  });
});
