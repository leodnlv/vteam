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
import { renderScheduler } from "./cron.js";
import type { AgentConfig, Task, VteamConfig } from "../types.js";
import type { TaskIndex } from "../tasks/task-manager.js";
import { color } from "../ansi.js";
import * as log from "../log.js";

interface RunningJob extends LiveRun {
  task?: Task;
}

interface LoadedAgents {
  agents: AgentConfig[];
  errors: string[];
}

export async function statusCommand(): Promise<void> {
  const cwd = process.cwd();
  requireVteamDir(cwd);
  console.log([...(await renderStatus(cwd)), ""].join("\n"));
}

export function requireVteamDir(cwd: string): void {
  if (!existsSync(resolve(cwd, "vteam"))) {
    console.error(color.red("No vteam/ directory found. Run 'vteam init' first."));
    process.exit(1);
  }
}

export async function renderStatus(cwd: string): Promise<string[]> {
  const config = loadConfig(cwd);
  const taskManager = new AvenTaskManager(config.tasks);
  const [index, ready] = await Promise.all([taskManager.getIndex(), taskManager.listReady()]);
  const loaded = loadAgents(cwd);
  const running = findRunningJobs(cwd, index);

  return [
    log.formatBanner("status", `aven: ${config.tasks.project} · label: ${config.tasks.label}`),
    ...renderRunning(cwd, running),
    ...renderScheduler(cwd, config),
    ...renderUpNext(loaded.agents, ready, config),
    ...renderAwaitingMerge(index),
    ...renderNeedsAttention(cwd, config, loaded, index, running),
  ];
}

function loadAgents(cwd: string): LoadedAgents {
  const loaded: LoadedAgents = { agents: [], errors: [] };
  for (const name of listAgentNames(cwd)) {
    try {
      loaded.agents.push(resolveAgentConfig(name, cwd));
    } catch (err) {
      loaded.errors.push(`agent "${name}" failed to load: ${err instanceof Error ? err.message : err}`);
    }
  }
  return loaded;
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

function renderRunning(cwd: string, running: RunningJob[]): string[] {
  const lines = [log.formatSection("Running")];
  if (running.length === 0) {
    lines.push(log.formatListItem(color.gray("·"), color.dim("nothing running")));
    return lines;
  }
  const width = Math.max(...running.map((r) => r.name.length));
  for (const job of running) {
    const elapsed = log.formatAge(Date.now() - job.lock.timestamp);
    const task = job.task ? `  ${formatTask(job.task)}` : "";
    const meta = color.gray(`pid ${job.lock.pid} · ${elapsed}`);
    lines.push(log.formatListItem(color.green("●"), `${color.bold(job.name.padEnd(width))}  ${meta}${task}`));
    if (job.state?.worktreePath) lines.push(log.formatListDetail("worktree", relative(cwd, job.state.worktreePath)));
  }
  return lines;
}

function renderUpNext(agents: AgentConfig[], ready: Task[], config: VteamConfig): string[] {
  const pickers = agents.filter((a) => a.input);
  if (pickers.length === 0) return [];
  const width = Math.max(...pickers.map((a) => a.name.length));
  return [
    log.formatSection("Up next"),
    ...pickers.map((agent) =>
      log.formatListItem(
        color.blue("•"),
        `${color.bold(agent.name.padEnd(width))}  ${describeNextPick(agent, ready, config)}`,
      ),
    ),
  ];
}

function describeNextPick(agent: AgentConfig, ready: Task[], config: VteamConfig): string {
  if (agent.input === "pr") {
    return agent.prTriggerLabel ? `open PRs labelled "${agent.prTriggerLabel}"` : "any open PR with review feedback";
  }
  const task = pickNextTask(ready, agent.taskFilter ?? {}, config.tasks.maxRetries);
  if (!task) return color.dim("nothing ready");
  const followUp = agent.worktree && task.metadata["pr-url"] ? color.gray(" (PR follow-up)") : "";
  return `${formatTask(task)}${followUp}`;
}

function renderAwaitingMerge(index: TaskIndex): string[] {
  const awaiting = (index.byStatus.get("done") ?? []).filter(
    (t) => t.metadata["pr-url"] && t.metadata.merged !== "true",
  );
  if (awaiting.length === 0) return [];
  return [
    log.formatSection(`Awaiting merge (${awaiting.length})`),
    ...awaiting.map((t) =>
      log.formatListItem(color.blue("•"), `${color.bold(t.ref)} ${t.title}  ${color.gray(t.metadata["pr-url"] ?? "")}`),
    ),
  ];
}

function renderNeedsAttention(
  cwd: string,
  config: VteamConfig,
  loaded: LoadedAgents,
  index: TaskIndex,
  running: RunningJob[],
): string[] {
  const items: string[] = [];
  const warning = (text: string) => items.push(log.formatListItem(color.yellow("!"), text));
  const failure = (text: string) => items.push(log.formatListItem(color.red("✗"), text));
  const host = hostname();
  const todo = index.byStatus.get("todo") ?? [];
  const taskAgents = loaded.agents.filter((a) => a.input === "task");

  for (const error of loaded.errors) failure(error);

  const inbox = index.byStatus.get("inbox")?.length ?? 0;
  if (inbox > 0) warning(`${inbox} task(s) in inbox awaiting triage`);

  for (const t of todo) {
    const ref = color.bold(t.ref);
    if (!taskAgents.some((a) => matchesTaskFilter(t, a.taskFilter ?? {}))) {
      failure(`${ref} matches no agent's taskFilter and will never be picked`);
    } else if (t.retryCount >= config.tasks.maxRetries) {
      failure(`${ref} hit max retries (${t.retryCount}/${config.tasks.maxRetries}) and will not be picked`);
    }
  }

  for (const t of index.byStatus.get("active") ?? []) {
    const ref = color.bold(t.ref);
    const pid = Number.parseInt(t.metadata["claimed-pid"] ?? "", 10);
    if (!pid) {
      warning(`${ref} is active without a vteam claim ${color.gray("(reset it by hand if it is stuck)")}`);
    } else if (t.metadata["claimed-host"] === host && !isProcessAlive(pid)) {
      failure(`${ref} is active but its process (pid ${pid}) is dead ${color.gray("(run 'vteam clean')")}`);
    }
  }

  const leftovers = findLeftoverWorktrees(cwd, config, running);
  if (leftovers.length > 0) {
    const paths = leftovers.map((path) => color.gray(relative(cwd, path)));
    warning(
      [`${leftovers.length} worktree(s) with no running agent ${color.gray("(run 'vteam clean')")}`, ...paths].join(
        "\n",
      ),
    );
  }

  return items.length === 0 ? [] : [log.formatSection("Needs attention"), ...items];
}

function findLeftoverWorktrees(cwd: string, config: VteamConfig, running: RunningJob[]): string[] {
  try {
    return findOrphanedWorktrees(cwd, config.worktreeDir, liveWorktreePaths(running));
  } catch {
    return [];
  }
}

const SEVERITY_COLOR: Record<Task["severity"], (text: string) => string> = {
  critical: (text) => color.bold(color.red(text)),
  high: color.red,
  medium: color.yellow,
  low: color.gray,
};

function formatTask(task: Task): string {
  return `${color.bold(task.ref)} ${SEVERITY_COLOR[task.severity](`[${task.severity}]`)} ${task.title}`;
}
