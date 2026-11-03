import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { mkdtempSync, mkdirSync, writeFileSync, existsSync, rmSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import type { Task } from "../types.js";

const mocks = vi.hoisted(() => ({
  cleanupOrphanedWorktrees: vi.fn(),
  findLiveRuns: vi.fn(),
  liveWorktreePaths: vi.fn(),
  releaseOrphanedTasks: vi.fn(),
  loadConfig: vi.fn(),
}));

vi.mock("../worktree/manager.js", () => ({ cleanupOrphanedWorktrees: mocks.cleanupOrphanedWorktrees }));
vi.mock("../run-state.js", () => ({ findLiveRuns: mocks.findLiveRuns, liveWorktreePaths: mocks.liveWorktreePaths }));
vi.mock("../tasks/claim.js", () => ({ releaseOrphanedTasks: mocks.releaseOrphanedTasks }));
vi.mock("../tasks/aven-manager.js", () => ({ AvenTaskManager: class {} }));
vi.mock("../config/load.js", () => ({ loadConfig: mocks.loadConfig }));

import { cleanCommand } from "./clean.js";

function task(ref: string, title: string): Task {
  return { ref, title } as Task;
}

describe("cleanCommand", () => {
  let dir: string;
  let output: string[];

  beforeEach(() => {
    dir = mkdtempSync(resolve(tmpdir(), "vteam-clean-"));
    vi.spyOn(process, "cwd").mockReturnValue(dir);
    output = [];
    vi.spyOn(console, "log").mockImplementation((msg: string) => {
      output.push(msg);
    });
    mocks.findLiveRuns.mockReturnValue([]);
    mocks.liveWorktreePaths.mockReturnValue([]);
    mocks.cleanupOrphanedWorktrees.mockReturnValue([]);
    mocks.loadConfig.mockReturnValue({ tasks: {} });
    mocks.releaseOrphanedTasks.mockResolvedValue({ released: [], foreignHost: [], unclaimed: [] });
  });

  afterEach(() => {
    vi.restoreAllMocks();
    vi.clearAllMocks();
    rmSync(dir, { recursive: true, force: true });
  });

  it("reports removed worktrees and how many are kept for running agents", async () => {
    mocks.liveWorktreePaths.mockReturnValue(["/wt/live"]);
    mocks.cleanupOrphanedWorktrees.mockReturnValue(["/wt/old"]);

    await cleanCommand();

    expect(output).toContain("Kept 1 worktree(s) in use by a running agent.");
    expect(output).toContain("Removed 1 worktree(s):");
    expect(output).toContain("  /wt/old");
    expect(mocks.cleanupOrphanedWorktrees).toHaveBeenCalledWith(dir, ".vteam-worktrees", ["/wt/live"]);
  });

  it("says so when there are no orphaned worktrees", async () => {
    await cleanCommand();

    expect(output).toContain("No orphaned worktrees found.");
  });

  it("falls back to a message when worktree cleanup throws", async () => {
    mocks.cleanupOrphanedWorktrees.mockImplementation(() => {
      throw new Error("not a git repo");
    });

    await cleanCommand();

    expect(output).toContain("No worktrees to clean (or not a git repo).");
  });

  it("uses worktreeDir from vteam.config.json", async () => {
    mkdirSync(resolve(dir, "vteam"));
    writeFileSync(resolve(dir, "vteam", "vteam.config.json"), JSON.stringify({ worktreeDir: "custom-wt" }));

    await cleanCommand();

    expect(mocks.cleanupOrphanedWorktrees).toHaveBeenCalledWith(dir, "custom-wt", []);
  });

  it("breaks a lock whose holder process is dead and keeps a live one", async () => {
    const locksDir = resolve(dir, "vteam", ".locks");
    const deadLock = resolve(locksDir, "dead.lock");
    const liveLock = resolve(locksDir, "live.lock");
    mkdirSync(deadLock, { recursive: true });
    mkdirSync(liveLock, { recursive: true });
    writeFileSync(resolve(deadLock, "info.json"), JSON.stringify({ pid: 2 ** 22 + 12345, timestamp: Date.now(), agent: "coder" }));
    writeFileSync(resolve(liveLock, "info.json"), JSON.stringify({ pid: process.pid, timestamp: Date.now(), agent: "improver" }));

    await cleanCommand();

    expect(output).toContain("Broke stale lock: dead.lock");
    expect(existsSync(deadLock)).toBe(false);
    expect(existsSync(liveLock)).toBe(true);
  });

  it("reports no stale locks when every lock is held by a live process", async () => {
    const liveLock = resolve(dir, "vteam", ".locks", "live.lock");
    mkdirSync(liveLock, { recursive: true });
    writeFileSync(resolve(liveLock, "info.json"), JSON.stringify({ pid: process.pid, timestamp: Date.now(), agent: "coder" }));

    await cleanCommand();

    expect(output).toContain("No stale locks found.");
  });

  it("lists tasks returned to todo, left on another host, and left unclaimed", async () => {
    mocks.releaseOrphanedTasks.mockResolvedValue({
      released: [task("APP-1", "Fix login")],
      foreignHost: [task("APP-2", "Add cache")],
      unclaimed: [task("APP-3", "Refactor db")],
    });

    await cleanCommand();

    expect(output).toContain("Returned 1 orphaned task(s) to todo:");
    expect(output).toContain("  APP-1 Fix login");
    expect(output).toContain("Left active (claimed on another host):");
    expect(output).toContain("  APP-2 Add cache");
    expect(output).toContain("Left active (no vteam claim, reset manually if stuck):");
    expect(output).toContain("  APP-3 Refactor db");
    expect(output).not.toContain("No orphaned active tasks found.");
  });

  it("says so when no active tasks are orphaned", async () => {
    await cleanCommand();

    expect(output).toContain("No orphaned active tasks found.");
  });

  it("skips task cleanup with the error message when the task sweep fails", async () => {
    mocks.releaseOrphanedTasks.mockRejectedValue(new Error("aven not found"));

    await cleanCommand();

    expect(output).toContain("Skipped task cleanup: aven not found");
    expect(output).toContain("\nDone.");
  });
});
