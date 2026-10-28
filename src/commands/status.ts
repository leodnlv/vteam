import { resolve } from "node:path";
import { existsSync, readdirSync } from "node:fs";
import { loadConfig } from "../config/load.js";
import { AvenTaskManager } from "../tasks/aven-manager.js";
import { listWorktrees } from "../worktree/manager.js";
import { listAgentNames } from "../config/agent.js";
import type { TaskStatus } from "../types.js";

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
  console.log(`  aven project: ${config.tasks.project} (label: ${config.tasks.label})`);
  console.log(`  default actor: ${config.tasks.defaultActor}\n`);
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
      const actor = t.actor ?? `${config.tasks.defaultActor} (default)`;
      console.log(`  ${t.ref} [${t.severity}] ${t.title} → ${actor}${retryNote}`);
    }
  }

  const agentNames = new Set(listAgentNames(cwd));
  const openTasks = [...(index.byStatus.get("todo") ?? []), ...(index.byStatus.get("active") ?? [])];
  const unknownActors = new Set(
    openTasks.map((t) => t.actor ?? config.tasks.defaultActor).filter((a) => !agentNames.has(a)),
  );
  for (const actor of unknownActors) {
    console.log(`\n  Warning: tasks are routed to "${actor}", but vteam/agents/${actor}/AGENT.md does not exist.`);
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
