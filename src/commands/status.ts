import { resolve } from "node:path";
import { existsSync, readdirSync } from "node:fs";
import { loadConfig } from "../config/load.js";
import { AvenTaskManager } from "../tasks/aven-manager.js";
import { listWorktrees } from "../worktree/manager.js";
import { listTaskWorkers } from "../config/agent.js";
import { matchesTaskFilter } from "../tasks/task-manager.js";
import type { Task, TaskStatus } from "../types.js";

const STATUSES: TaskStatus[] = ["inbox", "backlog", "todo", "active", "done", "canceled"];

export async function statusCommand(): Promise<void> {
  const cwd = process.cwd();

  const vteamDir = resolve(cwd, "vteam");
  if (!existsSync(vteamDir)) {
    console.error("No vteam/ directory found. Run 'vteam init' first.");
    process.exit(1);
  }

  const config = loadConfig(cwd);
  const taskManager = new AvenTaskManager(config.tasks);
  const index = await taskManager.getIndex();

  console.log("=== vteam status ===\n");
  console.log(`  aven project: ${config.tasks.project} (label: ${config.tasks.label})\n`);
  const workers = listTaskWorkers(cwd);
  const routeOf = (t: Task) =>
    workers.filter((w) => matchesTaskFilter(t, w.taskFilter ?? {})).map((w) => w.name);
  for (const status of STATUSES) {
    const count = index.byStatus.get(status)?.length ?? 0;
    if (count > 0) console.log(`  ${status.padEnd(9)}${count} tasks`);
  }
  console.log(`  ${"total".padEnd(9)}${index.all.length} tasks`);

  for (const status of ["active", "todo"] as const) {
    const tasks = index.byStatus.get(status) ?? [];
    if (tasks.length === 0) continue;
    console.log(`\n--- ${status[0].toUpperCase()}${status.slice(1)} ---`);
    for (const t of tasks) {
      const retryNote = t.retryCount > 0 ? ` (retries: ${t.retryCount})` : "";
      console.log(`  ${t.ref} [${t.severity}] ${t.title} → ${routeOf(t).join(", ") || "no agent"}${retryNote}`);
    }
  }

  const unrouted = (index.byStatus.get("todo") ?? []).filter((t) => routeOf(t).length === 0);
  if (unrouted.length > 0) {
    console.log(`\n  Warning: ${unrouted.length} todo task(s) match no input: "task" agent's taskFilter and will never be picked.`);
  }

  try {
    const worktrees = listWorktrees(cwd);
    const vteamWorktrees = worktrees.filter((w) =>
      w.branch.includes("vteam/"),
    );
    if (vteamWorktrees.length > 0) {
      console.log("\n--- Active worktrees ---");
      for (const w of vteamWorktrees) {
        console.log(`  ${w.branch} → ${w.path}`);
      }
    }
  } catch {
    // Not a git repo or no worktrees
  }

  const runsDir = resolve(cwd, "vteam", ".runs");
  if (existsSync(runsDir)) {
    const runFiles = readdirSync(runsDir)
      .filter((f) => f.endsWith(".json"))
      .sort()
      .reverse()
      .slice(0, 3);

    if (runFiles.length > 0) {
      console.log("\n--- Recent runs ---");
      for (const f of runFiles) {
        console.log(`  ${f.replace(".json", "")}`);
      }
    }
  }

  console.log();
}
