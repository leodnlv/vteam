import { describe, it, expect } from "vitest";
import { hostname } from "node:os";
import { CLAIM_KEYS, claimMetadata, releaseOrphanedTasks } from "./claim.js";
import type { TaskIndex, TaskManager, TaskUpdate } from "./task-manager.js";
import type { Task } from "../types.js";

function task(ref: string, metadata: Record<string, string> = {}): Task {
  return {
    ref,
    title: `Task ${ref}`,
    status: "active",
    severity: "high",
    files: [],
    retryCount: 0,
    metadata,
    description: "",
    notes: [],
    createdAt: "2026-10-01T00:00:00Z",
  };
}

function fakeManager(tasks: Task[]) {
  const updates: { ref: string; changes: TaskUpdate }[] = [];
  const manager: TaskManager = {
    listReady: async () => [],
    getIndex: async (): Promise<TaskIndex> => ({
      all: tasks,
      byStatus: new Map([["active", tasks.filter((t) => t.status === "active")]]),
      titles: tasks.map((t) => t.title),
    }),
    create: async () => "",
    update: async (ref, changes) => {
      updates.push({ ref, changes });
    },
    addNote: async () => {},
  };
  return { manager, updates };
}

describe("claimMetadata", () => {
  it("records the run, process and host", () => {
    expect(claimMetadata("run-1")).toEqual({
      "claimed-by": "run-1",
      "claimed-pid": String(process.pid),
      "claimed-host": hostname(),
    });
  });
});

describe("releaseOrphanedTasks", () => {
  const claim = (pid: number, host = "box") => ({
    "claimed-by": "run",
    "claimed-pid": String(pid),
    "claimed-host": host,
  });
  const alive = (pid: number) => pid === 100;

  it("returns tasks claimed by a dead process on this host to todo", async () => {
    const { manager, updates } = fakeManager([task("A", claim(200))]);

    const sweep = await releaseOrphanedTasks(manager, alive, "box");

    expect(sweep.released.map((t) => t.ref)).toEqual(["A"]);
    expect(updates).toEqual([
      { ref: "A", changes: { status: "todo", removeMetadata: [...CLAIM_KEYS] } },
    ]);
  });

  it("leaves tasks claimed by a live process", async () => {
    const { manager, updates } = fakeManager([task("A", claim(100))]);

    const sweep = await releaseOrphanedTasks(manager, alive, "box");

    expect(sweep).toEqual({ released: [], foreignHost: [], unclaimed: [] });
    expect(updates).toEqual([]);
  });

  it("leaves tasks claimed on another host", async () => {
    const { manager, updates } = fakeManager([task("A", claim(200, "other"))]);

    const sweep = await releaseOrphanedTasks(manager, alive, "box");

    expect(sweep.foreignHost.map((t) => t.ref)).toEqual(["A"]);
    expect(updates).toEqual([]);
  });

  it("leaves active tasks without a vteam claim", async () => {
    const { manager, updates } = fakeManager([task("A")]);

    const sweep = await releaseOrphanedTasks(manager, alive, "box");

    expect(sweep.unclaimed.map((t) => t.ref)).toEqual(["A"]);
    expect(updates).toEqual([]);
  });
});
