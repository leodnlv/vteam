import { mkdirSync } from "node:fs";
import { resolve } from "node:path";
import { acquireLock } from "../memory/lock.js";
import { loadConfig } from "../config/load.js";
import { AvenTaskManager } from "../tasks/aven-manager.js";
import type { TaskManager } from "../tasks/task-manager.js";
import { getPullRequest, removePRLabel } from "../integrations/pull-request.js";
import { startRunLog } from "../run-log.js";
import type { PullRequestInfo, Task, VteamConfig } from "../types.js";

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
  try {
    const config = loadConfig(cwd);
    const locksDir = resolve(cwd, "vteam", ".locks");
    mkdirSync(locksDir, { recursive: true });
    const lock = await acquireLock(resolve(locksDir, MERGE_ENSURER), MERGE_ENSURER);
    try {
      const sweep = await ensureMerges(new AvenTaskManager(config.tasks), config, cwd);
      console.log(
        `\nMerged: ${sweep.merged.length}, canceled: ${sweep.canceled.length}, reopened: ${sweep.reopened.length}, ` +
          `waiting: ${sweep.waiting.length}, failed: ${sweep.failed.length}`,
      );
    } finally {
      lock.release();
    }
  } catch (err) {
    runLog.write(`${err instanceof Error ? (err.stack ?? err.message) : String(err)}\n`);
    throw err;
  } finally {
    runLog.close();
  }
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
  console.log(`Checking ${tasks.length} done task(s) with an unmerged PR...\n`);

  for (const task of tasks) {
    try {
      sweep[await ensureMerge(taskManager, config, task, cwd)].push(task);
    } catch (err) {
      console.error(`${task.ref} "${task.title}" (${task.metadata["pr-url"]}): failed —`, err instanceof Error ? err.message : err);
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
    log(task, pr, "merged. Recorded merged=true.");
    return "merged";
  }

  if (pr.state === "closed") {
    await taskManager.update(task.ref, { status: "canceled" });
    await addNote(taskManager, task, `${MERGE_ENSURER}: canceled\n\nPR ${pr.url} was closed without merging.`);
    log(task, pr, "closed without merging. Task canceled.");
    return "canceled";
  }

  const changesRequested = pr.labels.includes(config.mergeEnsurer.label);
  const conflicting = pr.mergeable === "conflicting";
  if (!changesRequested && !conflicting) {
    log(task, pr, `open, ${REVIEW[pr.review]}, no "${config.mergeEnsurer.label}" label, ${MERGEABILITY[pr.mergeable]}. Waiting.`);
    return "waiting";
  }

  // Remove the label before reopening: if the task update then fails, the task stays done
  // and the label can be re-added, instead of reopening on every tick after each push.
  if (changesRequested) {
    removePRLabel(config.platform, pr.number, config.mergeEnsurer.label, cwd);
  }
  await taskManager.update(task.ref, { status: "todo", removeMetadata: ["retry-count"] });
  await addNote(taskManager, task, reopenNote(pr, config.mergeEnsurer.label, changesRequested, conflicting));
  const reasons = [changesRequested ? `has the "${config.mergeEnsurer.label}" label` : "", conflicting ? `conflicts with ${pr.baseBranch}` : ""];
  log(task, pr, `open, ${REVIEW[pr.review]}, ${reasons.filter(Boolean).join(" and ")}. Task moved to todo.`);
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

function log(task: Task, pr: PullRequestInfo, message: string): void {
  console.log(`${task.ref} "${task.title}"\n  PR #${pr.number} ${pr.url}\n  ${message}`);
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
    console.error(`Failed to add note to task ${task.ref}:`, err instanceof Error ? err.message : err);
  }
}
