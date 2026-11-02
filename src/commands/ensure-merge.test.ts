import { describe, it, expect, vi, beforeEach } from "vitest";
import type { TaskManager, TaskUpdate } from "../tasks/task-manager.js";
import type { PullRequestInfo, Task, VteamConfig } from "../types.js";

vi.mock("../integrations/pull-request.js", () => ({
  getPullRequest: vi.fn(),
  removePRLabel: vi.fn(),
}));

import { getPullRequest, removePRLabel } from "../integrations/pull-request.js";
import { ensureMerges } from "./ensure-merge.js";

const config: VteamConfig = {
  baseBranch: "main",
  platform: "github",
  worktreeDir: ".worktrees",
  tasks: { maxRetries: 3, project: "app", label: "vteam" },
  mergeEnsurer: { label: "vteam:changes-requested" },
};

function doneTask(ref: string, metadata: Record<string, string>): Task {
  return {
    ref,
    title: `Task ${ref}`,
    status: "done",
    severity: "medium",
    retryCount: 0,
    metadata,
    description: "",
    notes: [],
    createdAt: "2026-10-01T00:00:00Z",
  };
}

function pr(overrides: Partial<PullRequestInfo> = {}): PullRequestInfo {
  return {
    number: 7,
    title: "vteam: coder: Fix the thing",
    branch: "vteam/fix-the-thing",
    baseBranch: "main",
    url: "https://github.com/org/repo/pull/7",
    state: "open",
    labels: [],
    mergeable: "mergeable",
    review: "none",
    ...overrides,
  };
}

const prUrl = "https://github.com/org/repo/pull/7";
let updates: { ref: string; changes: TaskUpdate }[];
let notes: { ref: string; text: string }[];
let calls: string[];

function fakeManager(tasks: Task[]): TaskManager {
  return {
    listReady: async () => [],
    listDone: async () => tasks,
    getIndex: async () => ({ all: tasks, byStatus: new Map(), titles: [] }),
    create: async () => "",
    update: async (ref, changes) => {
      calls.push("update");
      updates.push({ ref, changes });
    },
    addNote: async (ref, text) => {
      notes.push({ ref, text });
    },
  };
}

beforeEach(() => {
  updates = [];
  notes = [];
  calls = [];
  vi.mocked(getPullRequest).mockReset();
  vi.mocked(removePRLabel).mockReset().mockImplementation(() => {
    calls.push("removeLabel");
  });
  vi.spyOn(console, "log").mockImplementation(() => {});
  vi.spyOn(console, "error").mockImplementation(() => {});
});

describe("ensureMerges", () => {
  it("only looks at done tasks with a PR that is not yet recorded as merged", async () => {
    vi.mocked(getPullRequest).mockReturnValue(pr());
    const tasks = [
      doneTask("A", { "pr-url": prUrl }),
      doneTask("B", {}),
      doneTask("C", { "pr-url": prUrl, merged: "true" }),
    ];

    const sweep = await ensureMerges(fakeManager(tasks), config, "/repo");

    expect(getPullRequest).toHaveBeenCalledOnce();
    expect(getPullRequest).toHaveBeenCalledWith("github", prUrl, "/repo");
    expect(sweep.waiting.map((t) => t.ref)).toEqual(["A"]);
  });

  it("leaves the task done when the PR is open, unlabelled, and mergeable", async () => {
    vi.mocked(getPullRequest).mockReturnValue(pr({ labels: ["vteam"] }));

    await ensureMerges(fakeManager([doneTask("A", { "pr-url": prUrl })]), config, "/repo");

    expect(updates).toEqual([]);
    expect(notes).toEqual([]);
    expect(removePRLabel).not.toHaveBeenCalled();
  });

  it("logs why a task is still waiting", async () => {
    vi.mocked(getPullRequest).mockReturnValue(pr({ mergeable: "unknown", review: "approved" }));

    await ensureMerges(fakeManager([doneTask("A", { "pr-url": prUrl })]), config, "/repo");

    const output = vi.mocked(console.log).mock.calls.map((call) => call.join(" ")).join("\n");
    expect(output).toContain(`A "Task A"\n  PR #7 ${prUrl}`);
    expect(output).toContain('open, approved, no "vteam:changes-requested" label, mergeability not computed yet. Waiting.');
  });

  it("treats unknown mergeability as nothing to do yet", async () => {
    vi.mocked(getPullRequest).mockReturnValue(pr({ mergeable: "unknown" }));

    const sweep = await ensureMerges(fakeManager([doneTask("A", { "pr-url": prUrl })]), config, "/repo");

    expect(sweep.waiting).toHaveLength(1);
    expect(updates).toEqual([]);
  });

  it("records a merged PR on the task and keeps it done", async () => {
    vi.mocked(getPullRequest).mockReturnValue(pr({ state: "merged" }));

    const sweep = await ensureMerges(fakeManager([doneTask("A", { "pr-url": prUrl })]), config, "/repo");

    expect(updates).toEqual([{ ref: "A", changes: { metadata: { merged: "true" } } }]);
    expect(sweep.merged).toHaveLength(1);
  });

  it("cancels the task with a note when the PR was closed without merging", async () => {
    vi.mocked(getPullRequest).mockReturnValue(pr({ state: "closed" }));

    const sweep = await ensureMerges(fakeManager([doneTask("A", { "pr-url": prUrl })]), config, "/repo");

    expect(updates).toEqual([{ ref: "A", changes: { status: "canceled" } }]);
    expect(notes[0].text).toContain(`PR ${prUrl} was closed without merging.`);
    expect(sweep.canceled).toHaveLength(1);
  });

  it("removes the approval label before reopening the task", async () => {
    vi.mocked(getPullRequest).mockReturnValue(pr({ labels: ["vteam", "vteam:changes-requested"] }));

    const sweep = await ensureMerges(
      fakeManager([doneTask("A", { "pr-url": prUrl, "retry-count": "2" })]),
      config,
      "/repo",
    );

    expect(removePRLabel).toHaveBeenCalledWith("github", 7, "vteam:changes-requested", "/repo");
    expect(calls).toEqual(["removeLabel", "update"]);
    expect(updates).toEqual([{ ref: "A", changes: { status: "todo", removeMetadata: ["retry-count"] } }]);
    expect(notes[0].text).toContain('has the "vteam:changes-requested" label');
    expect(notes[0].text).not.toContain("conflicts with");
    expect(sweep.reopened).toHaveLength(1);
  });

  it("leaves the task done when the label cannot be removed", async () => {
    vi.mocked(getPullRequest).mockReturnValue(pr({ labels: ["vteam:changes-requested"] }));
    vi.mocked(removePRLabel).mockImplementation(() => {
      throw new Error("gh: forbidden");
    });

    const sweep = await ensureMerges(fakeManager([doneTask("A", { "pr-url": prUrl })]), config, "/repo");

    expect(updates).toEqual([]);
    expect(sweep.failed).toHaveLength(1);
  });

  it("reopens a conflicting PR without needing the label", async () => {
    vi.mocked(getPullRequest).mockReturnValue(pr({ mergeable: "conflicting" }));

    await ensureMerges(fakeManager([doneTask("A", { "pr-url": prUrl })]), config, "/repo");

    expect(removePRLabel).not.toHaveBeenCalled();
    expect(updates[0].changes.status).toBe("todo");
    expect(notes[0].text).toContain(`PR ${prUrl} conflicts with main`);
  });

  it("keeps sweeping after one PR lookup fails", async () => {
    vi.mocked(getPullRequest)
      .mockImplementationOnce(() => {
        throw new Error("gh: not found");
      })
      .mockReturnValueOnce(pr({ state: "merged" }));

    const sweep = await ensureMerges(
      fakeManager([doneTask("A", { "pr-url": prUrl }), doneTask("B", { "pr-url": prUrl })]),
      config,
      "/repo",
    );

    expect(sweep.failed.map((t) => t.ref)).toEqual(["A"]);
    expect(sweep.merged.map((t) => t.ref)).toEqual(["B"]);
  });
});
