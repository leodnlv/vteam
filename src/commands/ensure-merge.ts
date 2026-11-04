import { mkdirSync } from "node:fs";
import { relative, resolve } from "node:path";
import { acquireLock } from "../memory/lock.js";
import { loadConfig } from "../config/load.js";
import { AvenTaskManager } from "../tasks/aven-manager.js";
import type { TaskManager } from "../tasks/task-manager.js";
import { getPullRequest, removePRLabel } from "../integrations/pull-request.js";
import { startRunLog } from "../run-log.js";
import type { PullRequestInfo, Task, VteamConfig } from "../types.js";
import { color } from "../ansi.js";
import * as log from "../log.js";

export const MERGE_ENSURER = "merge-ensurer";

export interface MergeSweep {
  merged: Task[];
  canceled: Task[];
  reopened: Task[];
  waiting: Task[];
  failed: Task[];
}

type Outcome = Exclude<keyof MergeSweep, "failed">;

export async function ensureMergeCommand(): Promise<void> {
  const cwd = process.cwd();
  const runLog = startRunLog(cwd, MERGE_ENSURER);
  log.startClock();
  log.banner("ensure-merge", `log: ${relative(cwd, runLog.path)}`);
  try {
    const config = loadConfig(cwd);
    log.section("Setup");
    const locksDir = resolve(cwd, "vteam", ".locks");
    mkdirSync(locksDir, { recursive: true });
    const lock = await acquireLock(resolve(locksDir, MERGE_ENSURER), MERGE_ENSURER);
    log.step("lock acquired");
    try {
      logSummary(await ensureMerges(new AvenTaskManager(config.tasks), config, cwd));
    } finally {
      lock.release();
    }
    log.finished(MERGE_ENSURER);
  } catch (err) {
    runLog.write(`${err instanceof Error ? (err.stack ?? err.message) : String(err)}\n`);
    log.finished(MERGE_ENSURER, err instanceof Error ? err.message : String(err));
    process.exitCode = 1;
  } finally {
    runLog.close();
  }
}

function logSummary(sweep: MergeSweep): void {
  log.section("Result");
  const counts = (["merged", "canceled", "reopened", "waiting"] as const).map((key) => `${key} ${sweep[key].length}`);
  log.ok(counts.join(" · "));
  if (sweep.failed.length > 0) log.fail(`${sweep.failed.length} task(s) failed`);
}

export async function ensureMerges(
  taskManager: TaskManager,
  config: VteamConfig,
  cwd: string,
): Promise<MergeSweep> {
  const sweep: MergeSweep = { merged: [], canceled: [], reopened: [], waiting: [], failed: [] };
  const tasks = (await taskManager.listDone()).filter(
    (task) => task.metadata["pr-url"] && task.metadata.merged !== "true",
  );
  if (tasks.length === 0) {
    log.step("no done tasks with an unmerged PR, nothing to do");
    return sweep;
  }
  log.step(`checking ${tasks.length} done task(s) with an unmerged PR`);
  log.section("Pull requests");

  for (const task of tasks) {
    try {
      sweep[await ensureMerge(taskManager, config, task, cwd)].push(task);
    } catch (err) {
      log.fail(`${task.ref} ${task.title}`);
      log.field("PR", task.metadata["pr-url"] ?? "");
      log.field("error", err instanceof Error ? err.message : String(err));
      sweep.failed.push(task);
    }
  }

  return sweep;
}

async function ensureMerge(
  taskManager: TaskManager,
  config: VteamConfig,
  task: Task,
  cwd: string,
): Promise<Outcome> {
  const pr = getPullRequest(config.platform, task.metadata["pr-url"], cwd);

  if (pr.state === "merged") {
    await taskManager.update(task.ref, { metadata: { merged: "true" } });
    report(log.ok, task, pr, "merged", "recorded merged=true");
    return "merged";
  }

  if (pr.state === "closed") {
    await taskManager.update(task.ref, { status: "canceled" });
    await addNote(taskManager, task, `${MERGE_ENSURER}: canceled\n\nPR ${pr.url} was closed without merging.`);
    report(log.warn, task, pr, "closed without merging", "task canceled");
    return "canceled";
  }

  const changesRequested = pr.labels.includes(config.mergeEnsurer.label);
  const conflicting = pr.mergeable === "conflicting";
  if (!changesRequested && !conflicting) {
    const state = ["open", REVIEW[pr.review], `no "${config.mergeEnsurer.label}" label`, MERGEABILITY[pr.mergeable]];
    report(log.step, task, pr, state.join(" · "), "none, waiting");
    return "waiting";
  }

  // Remove the label before reopening: if the task update then fails, the task stays done
  // and the label can be re-added, instead of reopening on every tick after each push.
  if (changesRequested) {
    removePRLabel(config.platform, pr.number, config.mergeEnsurer.label, cwd);
  }
  await taskManager.update(task.ref, { status: "todo", removeMetadata: ["retry-count"] });
  await addNote(taskManager, task, reopenNote(pr, config.mergeEnsurer.label, changesRequested, conflicting));
  const state = [
    "open",
    REVIEW[pr.review],
    changesRequested ? `has the "${config.mergeEnsurer.label}" label` : "",
    conflicting ? `conflicts with ${pr.baseBranch}` : "",
  ];
  report(log.ok, task, pr, state.filter(Boolean).join(" · "), "task moved to todo");
  return "reopened";
}

const REVIEW: Record<PullRequestInfo["review"], string> = {
  approved: "approved",
  changes_requested: "changes requested",
  review_required: "awaiting approval",
  none: "no reviews yet",
};

const MERGEABILITY: Record<PullRequestInfo["mergeable"], string> = {
  mergeable: "no conflicts",
  conflicting: "conflicting",
  unknown: "mergeability not computed yet",
};

function report(
  headline: (text: string) => void,
  task: Task,
  pr: PullRequestInfo,
  state: string,
  action: string,
): void {
  headline(`${color.bold(task.ref)} ${task.title}`);
  log.field("PR", `#${pr.number} ${pr.url}`);
  log.field("state", state);
  log.field("action", action);
}

function reopenNote(pr: PullRequestInfo, label: string, changesRequested: boolean, conflicting: boolean): string {
  return [
    `${MERGE_ENSURER}: reopened`,
    changesRequested ? `PR ${pr.url} has the "${label}" label: apply its open review threads.` : "",
    conflicting ? `PR ${pr.url} conflicts with ${pr.baseBranch}: merge ${pr.baseBranch} and resolve the conflicts.` : "",
  ]
    .filter(Boolean)
    .join("\n\n");
}

async function addNote(taskManager: TaskManager, task: Task, text: string): Promise<void> {
  try {
    await taskManager.addNote(task.ref, text);
  } catch (err) {
    log.warn(`failed to add note to task ${task.ref}: ${err instanceof Error ? err.message : String(err)}`);
  }
}
