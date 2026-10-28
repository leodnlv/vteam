import { resolve } from "node:path";
import { existsSync, readdirSync, readFileSync } from "node:fs";
import { cleanupOrphanedWorktrees } from "../worktree/manager.js";
import { breakStaleLock } from "../memory/lock.js";
import { loadConfig } from "../config/load.js";
import { AvenTaskManager } from "../tasks/aven-manager.js";
import { releaseOrphanedTasks } from "../tasks/claim.js";
import type { Task } from "../types.js";

export async function cleanCommand(): Promise<void> {
  const cwd = process.cwd();
  const config = loadConfigSafe(cwd);
  const worktreeDir = config?.worktreeDir ?? ".vteam-worktrees";

  console.log("=== vteam clean ===\n");

  // Clean up worktrees
  try {
    const cleaned = cleanupOrphanedWorktrees(cwd, worktreeDir);
    if (cleaned.length > 0) {
      console.log(`Removed ${cleaned.length} worktree(s):`);
      for (const p of cleaned) {
        console.log(`  ${p}`);
      }
    } else {
      console.log("No orphaned worktrees found.");
    }
  } catch {
    console.log("No worktrees to clean (or not a git repo).");
  }

  // Clean up locks
  const locksDir = resolve(cwd, "vteam", ".locks");
  if (existsSync(locksDir)) {
    const lockDirs = readdirSync(locksDir).filter((f) => f.endsWith(".lock"));
    let brokenCount = 0;
    for (const lockDir of lockDirs) {
      const lockPath = resolve(locksDir, lockDir.replace(".lock", ""));
      if (breakStaleLock(lockPath)) {
        console.log(`Broke stale lock: ${lockDir}`);
        brokenCount++;
      }
    }
    if (brokenCount === 0) {
      console.log("No stale locks found.");
    }
  }

  await releaseOrphanedTasksStep(cwd);

  console.log("\nDone.");
}

async function releaseOrphanedTasksStep(cwd: string): Promise<void> {
  let sweep;
  try {
    sweep = await releaseOrphanedTasks(new AvenTaskManager(loadConfig(cwd).tasks));
  } catch (err) {
    console.log(`Skipped task cleanup: ${err instanceof Error ? err.message : err}`);
    return;
  }

  const print = (header: string, tasks: Task[]) => {
    if (tasks.length === 0) return;
    console.log(header);
    for (const t of tasks) console.log(`  ${t.ref} ${t.title}`);
  };

  if (sweep.released.length === 0) console.log("No orphaned active tasks found.");
  print(`Returned ${sweep.released.length} orphaned task(s) to todo:`, sweep.released);
  print("Left active (claimed on another host):", sweep.foreignHost);
  print("Left active (no vteam claim, reset manually if stuck):", sweep.unclaimed);
}

function loadConfigSafe(cwd: string): { worktreeDir?: string } | null {
  try {
    const configPath = resolve(cwd, "vteam", "vteam.config.json");
    return JSON.parse(readFileSync(configPath, "utf-8"));
  } catch {
    return null;
  }
}
