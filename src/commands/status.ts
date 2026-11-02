import { resolve, relative } from "node:path";
import { existsSync } from "node:fs";
import { hostname } from "node:os";
import { loadConfig } from "../config/load.js";
import { listAgentNames, resolveAgentConfig } from "../config/agent.js";
import { AvenTaskManager } from "../tasks/aven-manager.js";
import { matchesTaskFilter, pickNextTask } from "../tasks/task-manager.js";
import { isProcessAlive } from "../memory/lock.js";
import { findOrphanedWorktrees } from "../worktree/manager.js";
import { findLiveRuns, liveWorktreePaths, type LiveRun } from "../run-state.js";
import { findScheduledJobs, nextRunOf, readSchedulerPid } from "./cron.js";
import type { AgentConfig, Task, VteamConfig } from "../types.js";
import type { TaskIndex } from "../tasks/task-manager.js";

interface RunningJob extends LiveRun {
  task?: Task;
}

export async function statusCommand(): Promise<void> {
  const cwd = process.cwd();
  if (!existsSync(resolve(cwd, "vteam"))) {
    console.error("No vteam/ directory found. Run 'vteam init' first.");
    process.exit(1);
  }

  const config = loadConfig(cwd);
  const taskManager = new AvenTaskManager(config.tasks);
  const [index, ready] = await Promise.all([taskManager.getIndex(), taskManager.listReady()]);
  const agents = loadAgents(cwd);
  const running = findRunningJobs(cwd, index);

  console.log(`=== vteam status ===  (aven: ${config.tasks.project}, label: ${config.tasks.label})`);
  printRunning(cwd, running);
  printScheduler(cwd, config);
  printUpNext(agents, ready, config);
  printAwaitingMerge(index);
  printNeedsAttention(cwd, config, agents, index, running);
  console.log();
}

function loadAgents(cwd: string): AgentConfig[] {
  return listAgentNames(cwd).flatMap((name) => {
    try {
      return [resolveAgentConfig(name, cwd)];
    } catch (err) {
      console.error(`Skipping agent "${name}": ${err instanceof Error ? err.message : err}`);
      return [];
    }
  });
}

function findRunningJobs(cwd: string, index: TaskIndex): RunningJob[] {
  const host = hostname();
  const active = index.byStatus.get("active") ?? [];
  return findLiveRuns(cwd).map((run) => ({
    ...run,
    task: active.find(
      (t) => t.metadata["claimed-host"] === host && t.metadata["claimed-pid"] === String(run.lock.pid),
    ),
  }));
}

function printRunning(cwd: string, running: RunningJob[]): void {
  console.log("\nRunning");
  if (running.length === 0) {
    console.log("  nothing");
    return;
  }
  const width = Math.max(...running.map((r) => r.name.length));
  for (const job of running) {
    const elapsed = formatDuration(Date.now() - job.lock.timestamp);
    const task = job.task ? `  ${formatTask(job.task)}` : "";
    console.log(`  ${job.name.padEnd(width)}  pid ${job.lock.pid}  ${elapsed}${task}`);
    if (job.state?.worktreePath) {
      console.log(`  ${"".padEnd(width)}  worktree ${relative(cwd, job.state.worktreePath)}`);
    }
  }
}

function printScheduler(cwd: string, config: VteamConfig): void {
  const jobs = findScheduledJobs(cwd, config);
  if (jobs.length === 0) {
    console.log("\nScheduler: no agents have a cron schedule");
    return;
  }
  const pid = readSchedulerPid(cwd);
  console.log(
    pid ? `\nScheduler: running (pid ${pid})` : "\nScheduler: not running (start it with 'vteam cron start')",
  );
  const nameWidth = Math.max(...jobs.map((j) => j.name.length));
  const cronWidth = Math.max(...jobs.map((j) => j.cron.length));
  for (const job of jobs) {
    const next = nextRunOf(job.cron);
    const when = next ? `next ${formatClock(next)} (in ${formatDuration(next.getTime() - Date.now())})` : "no next run";
    console.log(`  ${job.name.padEnd(nameWidth)}  ${job.cron.padEnd(cronWidth)}  ${when}`);
  }
}

function printUpNext(agents: AgentConfig[], ready: Task[], config: VteamConfig): void {
  const pickers = agents.filter((a) => a.input);
  if (pickers.length === 0) return;
  console.log("\nUp next");
  const width = Math.max(...pickers.map((a) => a.name.length));
  for (const agent of pickers) {
    console.log(`  ${agent.name.padEnd(width)}  ${describeNextPick(agent, ready, config)}`);
  }
}

function describeNextPick(agent: AgentConfig, ready: Task[], config: VteamConfig): string {
  if (agent.input === "pr") {
    return agent.prTriggerLabel ? `open PRs labelled "${agent.prTriggerLabel}"` : "any open PR with review feedback";
  }
  const task = pickNextTask(ready, agent.taskFilter ?? {}, config.tasks.maxRetries);
  if (!task) return "nothing ready";
  const followUp = agent.worktree && task.metadata["pr-url"] ? " (PR follow-up)" : "";
  return `${formatTask(task)}${followUp}`;
}

function printAwaitingMerge(index: TaskIndex): void {
  const awaiting = (index.byStatus.get("done") ?? []).filter(
    (t) => t.metadata["pr-url"] && t.metadata.merged !== "true",
  );
  if (awaiting.length === 0) return;
  console.log(`\nAwaiting merge (${awaiting.length})`);
  for (const t of awaiting) console.log(`  ${t.ref} ${t.title}  ${t.metadata["pr-url"]}`);
}

function printNeedsAttention(
  cwd: string,
  config: VteamConfig,
  agents: AgentConfig[],
  index: TaskIndex,
  running: RunningJob[],
): void {
  const lines: string[] = [];
  const host = hostname();
  const todo = index.byStatus.get("todo") ?? [];
  const taskAgents = agents.filter((a) => a.input === "task");

  const inbox = index.byStatus.get("inbox")?.length ?? 0;
  if (inbox > 0) lines.push(`${inbox} task(s) in inbox awaiting triage`);

  for (const t of todo) {
    if (!taskAgents.some((a) => matchesTaskFilter(t, a.taskFilter ?? {}))) {
      lines.push(`${t.ref} matches no agent's taskFilter and will never be picked`);
    } else if (t.retryCount >= config.tasks.maxRetries) {
      lines.push(`${t.ref} hit max retries (${t.retryCount}/${config.tasks.maxRetries}) and will not be picked`);
    }
  }

  for (const t of index.byStatus.get("active") ?? []) {
    const pid = Number.parseInt(t.metadata["claimed-pid"] ?? "", 10);
    if (!pid) {
      lines.push(`${t.ref} is active without a vteam claim (reset it by hand if it is stuck)`);
    } else if (t.metadata["claimed-host"] === host && !isProcessAlive(pid)) {
      lines.push(`${t.ref} is active but its process (pid ${pid}) is dead (run 'vteam clean')`);
    }
  }

  const leftovers = findLeftoverWorktrees(cwd, config, running);
  if (leftovers.length > 0) {
    lines.push(`${leftovers.length} worktree(s) with no running agent (run 'vteam clean'):`);
    for (const path of leftovers) lines.push(`  ${relative(cwd, path)}`);
  }

  if (lines.length === 0) return;
  console.log("\nNeeds attention");
  for (const line of lines) console.log(`  ${line}`);
}

function findLeftoverWorktrees(cwd: string, config: VteamConfig, running: RunningJob[]): string[] {
  try {
    return findOrphanedWorktrees(cwd, config.worktreeDir, liveWorktreePaths(running));
  } catch {
    return [];
  }
}

function formatTask(task: Task): string {
  return `${task.ref} [${task.severity}] ${task.title}`;
}

function formatClock(date: Date): string {
  const sameDay = date.toDateString() === new Date().toDateString();
  const time = date.toLocaleTimeString([], { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
  return sameDay ? time : `${date.toLocaleDateString([], { month: "short", day: "numeric" })} ${time}`;
}

export function formatDuration(ms: number): string {
  const minutes = Math.floor(Math.max(ms, 0) / 60_000);
  if (minutes < 1) return "<1m";
  const days = Math.floor(minutes / 1440);
  const hours = Math.floor((minutes % 1440) / 60);
  const mins = minutes % 60;
  if (days > 0) return `${days}d ${hours}h`;
  if (hours > 0) return `${hours}h ${mins}m`;
  return `${mins}m`;
}
