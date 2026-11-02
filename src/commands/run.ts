import {
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from "node:fs";
import { execFileSync } from "node:child_process";
import { relative, resolve } from "node:path";
import { acquireLock, type FileLock } from "../memory/lock.js";
import { buildPrompt, buildOnFinishPrompt, buildMemoryCurationPrompt, threadRef } from "../orchestrator/prompt-builder.js";
import { runClaudeAgent } from "../orchestrator/agent-runner.js";
import { parseAgentOutput } from "../orchestrator/output-schema.js";
import { AvenTaskManager } from "../tasks/aven-manager.js";
import { pickNextTask, type TaskManager } from "../tasks/task-manager.js";
import { CLAIM_KEYS, claimMetadata } from "../tasks/claim.js";
import {
  createWorktree,
  checkoutWorktree,
  removeWorktree,
  pushBranch,
  getCommitSha,
  mergeBaseBranch,
  findUnresolvedConflicts,
} from "../worktree/manager.js";
import { createMergeRequest } from "../integrations/merge-request.js";
import {
  findReviewablePR,
  getOpenReviewThreads,
  getPullRequest,
  getReviewComments,
  getRepoSlug,
  postPRComment,
  removePRLabel,
  replyToReviewThread,
  resolveReviewThread,
} from "../integrations/pull-request.js";
import { loadConfig } from "../config/load.js";
import { labelsOnTaskCreate, labelsOnTaskDone } from "../tasks/labels.js";
import { resolveAgentConfig, listAgentNames } from "../config/agent.js";
import { startRunLog } from "../run-log.js";
import * as log from "../log.js";
import { writeRunState } from "../run-state.js";
import type {
  AgentConfig,
  AgentOutput,
  VteamConfig,
  RunState,
  Task,
  PRReviewContext,
  PRFollowUpContext,
  RunOutcome,
  ThreadReply,
} from "../types.js";

function generateRunId(agent: string): string {
  const now = new Date();
  const pad = (n: number) => String(n).padStart(2, "0");
  const ts = `${now.getFullYear()}${pad(now.getMonth() + 1)}${pad(now.getDate())}-${pad(now.getHours())}${pad(now.getMinutes())}${pad(now.getSeconds())}`;
  return `${ts}-${agent}`;
}

function listAgents(cwd: string): void {
  const agentNames = listAgentNames(cwd);

  if (agentNames.length === 0) {
    console.log("No agents found. Run 'vteam init' first.");
    return;
  }

  console.log("Available agents:\n");
  for (const name of agentNames) {
    try {
      const agent = resolveAgentConfig(name, cwd);
      const flags = [
        agent.worktree ? "worktree" : null,
        agent.readOnly ? "readOnly" : null,
        agent.output ? `output: ${agent.output}` : null,
        agent.input ? `input: ${agent.input}` : null,
        agent.autoPR ? "autoPR" : null,
      ]
        .filter(Boolean)
        .join(", ");
      console.log(`  ${name}${flags ? `  (${flags})` : ""}`);
    } catch {
      console.log(`  ${name}  (invalid config)`);
    }
  }
}

interface RunOptions {
  focus?: string;
}

export async function runCommand(agentName: string | undefined, options: RunOptions): Promise<void> {
  const cwd = process.cwd();

  if (!agentName) {
    listAgents(cwd);
    return;
  }

  const agent = resolveAgentConfig(agentName, cwd);
  const runLog = startRunLog(cwd, agent.name);
  log.startClock();
  log.banner(agent.name, `log: ${relative(cwd, runLog.path)}`);
  try {
    const config = loadConfig(cwd);
    await runAgent(cwd, config, agent, options.focus);
  } catch (err) {
    runLog.write(`${err instanceof Error ? (err.stack ?? err.message) : String(err)}\n`);
    throw err;
  } finally {
    runLog.close();
  }
}

function messageOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

function hasUncommittedChanges(worktreePath: string): boolean {
  try {
    const status = execFileSync("git", ["status", "--porcelain"], {
      cwd: worktreePath,
      encoding: "utf-8",
    });
    return status.trim().length > 0;
  } catch {
    return false;
  }
}

async function runAgent(
  cwd: string,
  config: VteamConfig,
  agent: AgentConfig,
  focus?: string,
): Promise<void> {
  const runId = generateRunId(agent.name);
  const taskManager = new AvenTaskManager(config.tasks);
  const locksDir = resolve(cwd, "vteam", ".locks");
  mkdirSync(locksDir, { recursive: true });

  const startedAt = new Date().toISOString();
  let agentLock: FileLock | undefined;
  let worktreePath: string | undefined;
  let branchName: string | undefined;
  let remoteSha = "";
  let task: Task | undefined;
  let taskSettled = false;
  let reviewContext: PRReviewContext | undefined;
  let followUp: PRFollowUpContext | undefined;
  const extraNotes: string[] = [];
  let prUrl: string | undefined;
  let memoryUpdate: string | undefined;
  let runOutcome: RunOutcome | undefined;

  try {
    log.section("Setup");
    agentLock = await acquireLock(
      resolve(locksDir, agent.name),
      agent.name,
    );
    log.step("lock acquired");

    if (agent.input === "task") {
      task = await claimTask(taskManager, config, agent, runId, locksDir);
      if (!task) {
        log.step("no eligible tasks, nothing to do");
        return;
      }
      log.step(`picked task ${task.ref}: ${task.title} (${task.severity})`);

      const existingPrUrl = task.metadata["pr-url"];
      if (agent.worktree && existingPrUrl) {
        const pr = getPullRequest(config.platform, existingPrUrl, cwd);
        if (pr.state !== "open") {
          await parkTaskWithClosedPR(taskManager, task, agent.name, pr.url, pr.state);
          taskSettled = true;
          return;
        }
        followUp = { pr, threads: getOpenReviewThreads(config.platform, pr.number, cwd) };
        prUrl = pr.url;
        log.step(`following up on PR #${pr.number} (${followUp.threads.length} open review threads)`);
      }
    }

    if (agent.input === "pr") {
      const labels = agent.prTriggerLabel ? [agent.prTriggerLabel] : [];
      const pr = findReviewablePR(config.platform, labels, cwd);
      if (!pr) {
        log.step("no PRs need a review response, nothing to do");
        return;
      }
      const comments = getReviewComments(config.platform, pr.number, cwd);
      const repoSlug = getRepoSlug(config.platform, cwd);
      reviewContext = { pr, comments, repoSlug };
      log.step(`picked PR #${pr.number}: ${pr.title}${comments.length > 0 ? ` (${comments.length} comments)` : ""}`);
    }

    const runState: RunState = {
      runId,
      agent: agent.name,
      status: "started",
      startedAt,
      ...(task ? { taskRef: task.ref } : {}),
    };
    writeRunState(cwd, runState);

    let agentCwd = cwd;
    if (agent.worktree) {
      const existingPR = reviewContext?.pr ?? followUp?.pr;
      if (existingPR) {
        log.step(`checking out PR branch ${existingPR.branch}`);
        const wt = checkoutWorktree(
          cwd,
          existingPR.branch,
          config.worktreeDir,
        );
        worktreePath = wt.path;
        branchName = wt.branch;
        remoteSha = wt.remoteSha;
      } else {
        const slug = task
          ? `${task.title}-${runId}`
          : `${agent.name}-${runId}`;
        const wt = createWorktree(
          cwd,
          slug,
          config.baseBranch,
          config.worktreeDir,
        );
        worktreePath = wt.path;
        branchName = wt.branch;
        remoteSha = wt.remoteSha;
      }
      if (followUp?.pr.mergeable === "conflicting") {
        const { baseBranch } = followUp.pr;
        log.warn(`PR conflicts with ${baseBranch}, merging origin/${baseBranch}`);
        const conflictedFiles = mergeBaseBranch(worktreePath, baseBranch);
        followUp.baseMerge = { baseBranch, conflictedFiles };
        if (conflictedFiles.length) log.warn(`conflicts in ${conflictedFiles.join(", ")}`);
        else log.ok("merged cleanly");
      }
      agentCwd = worktreePath;
      runState.worktreePath = worktreePath;
      runState.branchName = branchName;
      writeRunState(cwd, runState);
      log.step("worktree ready");
      log.field("path", relative(cwd, worktreePath));
      log.field("branch", branchName);
    }

    let memoryContent: string | undefined;
    const memoryFilePath = resolve(cwd, "vteam", ".memory", agent.name, "store.md");
    if (agent.memory && existsSync(memoryFilePath)) {
      memoryContent = readFileSync(memoryFilePath, "utf-8").trim() || undefined;
    }

    const { systemPrompt, userPrompt } = await buildPrompt(
      agent,
      taskManager,
      task,
      reviewContext ?? followUp,
      focus,
      memoryContent,
    );

    log.step("prompt built");

    log.section(`Agent: ${agent.name}${agent.model ? ` (${agent.model})` : ""}`);
    runState.status = "claude_running";
    writeRunState(cwd, runState);

    const result = await runClaudeAgent({
      systemPrompt,
      userPrompt,
      cwd: agentCwd,
      model: agent.model,
      allowedTools: agent.allowedTools,
      disallowedTools: agent.disallowedTools,
    });

    if (result.exitCode !== 0) {
      throw new Error(`Claude exited with code ${result.exitCode}`);
    }

    if (!result.resultText) {
      throw new Error("Claude produced no output");
    }

    runState.status = "processing";
    writeRunState(cwd, runState);
    log.section("Result");

    const output = parseAgentOutput(result.resultText);
    memoryUpdate = output.memoryUpdate;
    runState.claudeOutput = output;
    runState.commitMessage = output.commitMessage;

    if (output.content?.type === "task") {
      const taskId = await taskManager.create(output.content.body, agent.name, labelsOnTaskCreate(config.tasks, agent.name));
      runState.tasksCreated = [taskId];
      log.ok(`created task ${taskId}: ${output.content.body.title} (${output.content.body.severity})`);
    }

    const unresolvedConflicts = worktreePath && followUp?.baseMerge
      ? findUnresolvedConflicts(worktreePath, followUp.baseMerge.conflictedFiles)
      : [];
    if (unresolvedConflicts.length > 0) {
      extraNotes.push(`Unresolved merge conflicts in: ${unresolvedConflicts.join(", ")}. Nothing was committed.`);
    }
    const commitMessage = worktreePath && commitsAgentChanges(worktreePath, output, unresolvedConflicts)
      ? output.commitMessage
      : undefined;

    if (agent.worktree && agent.readOnly && worktreePath) {
      log.step("read-only agent, nothing committed");

      if (reviewContext) {
        removeTriggerLabel(config, agent, reviewContext.pr.number, cwd);
      }
    } else if (agent.worktree && worktreePath && branchName &&
      (commitMessage || mergedBaseCleanly(followUp))) {
      if (commitMessage) {
        execFileSync("git", ["add", "-A"], { cwd: worktreePath, stdio: "pipe" });
        execFileSync(
          "git",
          ["commit", "-m", commitMessage.subject, "-m", commitMessage.body],
          { cwd: worktreePath, stdio: "pipe" },
        );
        log.ok(`committed ${commitMessage.subject}`);
      } else {
        log.step(`no changes, pushing the merge of ${followUp?.baseMerge?.baseBranch}`);
      }
      runState.commitSha = getCommitSha(worktreePath);

      await pushBranch(worktreePath, branchName, remoteSha);
      log.ok(`pushed ${branchName}`);

      if (reviewContext) {
        try {
          postPRComment(
            config.platform,
            reviewContext.pr.number,
            `Addressed review feedback in \`${runState.commitSha.slice(0, 7)}\`.`,
            cwd,
          );
          log.ok(`commented on PR #${reviewContext.pr.number}`);
        } catch (commentErr) {
          log.warn(`failed to post PR comment: ${messageOf(commentErr)}`);
        }

        removeTriggerLabel(config, agent, reviewContext.pr.number, cwd);
      }

      if (followUp) {
        log.ok(`updated PR ${followUp.pr.url}`);
        if (output.threadReplies?.length) {
          const posted = postThreadReplies(config, agent, followUp, output.threadReplies, runState.commitSha, cwd);
          const threadReplyNote = `Replied to ${posted} of ${output.threadReplies.length} review threads.`;
          extraNotes.push(threadReplyNote);
          log.step(threadReplyNote);
        }
      } else if (agent.autoPR) {
        try {
          const prBody = output.commitMessage?.body || `Automated by vteam (${agent.name}).${task ? `\n\nTask: ${task.title}` : ""}`;
          const summary = task?.title || output.commitMessage?.subject.replace(/^vteam:\s*/i, "") || "";
          const prTitle = summary
            ? `vteam: ${agent.name}: ${summary}`
            : `vteam: ${agent.name}`;
          prUrl = createMergeRequest({
            platform: config.platform,
            branch: branchName,
            baseBranch: config.baseBranch,
            title: prTitle,
            body: prBody,
            labels: agent.prCreateLabels,
            cwd,
          });
          log.ok(`created PR ${prUrl}`);
        } catch (mrErr) {
          log.warn(`PR creation failed: ${messageOf(mrErr)}`);
          log.warn("the branch was pushed, create the PR manually");
        }
      }

      if (task) {
        await taskManager.update(task.ref, {
          status: "done",
          metadata: {
            completed: new Date().toISOString(),
            branch: branchName,
            "pr-url": prUrl,
          },
          removeMetadata: [...CLAIM_KEYS],
          addLabels: labelsOnTaskDone(config.tasks, agent.name),
        });
        taskSettled = true;
        log.ok(`task ${task.ref} done`);
      }
    } else if (agent.worktree && worktreePath && branchName) {
      log.warn(`${agent.name} produced no changes (${output.status})`);
      if (output.status === "blocked" || output.status === "failed") {
        log.field("reason", output.blockerReason ?? output.summary);
      }
      if (unresolvedConflicts.length > 0) {
        log.field("conflicts", unresolvedConflicts.join(", "));
      }
      if (task) {
        const retryCount = task.retryCount + 1;
        await taskManager.update(task.ref, {
          status: "todo",
          metadata: { "retry-count": String(retryCount) },
          removeMetadata: [...CLAIM_KEYS],
        });
        taskSettled = true;
        log.field("retries", `${retryCount}/${config.tasks.maxRetries}`);
      }
    }

    if (task) {
      await addRunNote(taskManager, task, formatRunNote(agent.name, output, prUrl, extraNotes));
    }

    writeRunState(cwd, runState);

    const completedAt = new Date().toISOString();
    runState.status = "completed";
    runState.completedAt = completedAt;
    writeRunState(cwd, runState);

    runOutcome = {
      agent: agent.name,
      status: "completed",
      startedAt,
      completedAt,
      focus,
      task: task ? { title: task.title, severity: task.severity } : undefined,
      branch: branchName,
      prUrl,
      reviewedPR: reviewContext ? { number: reviewContext.pr.number, title: reviewContext.pr.title, url: reviewContext.pr.url } : undefined,
      tasksCreated: runState.tasksCreated,
      commitMessage: runState.commitMessage,
      content: output.content,
    };
  } catch (err) {
    const errorMsg = err instanceof Error ? err.message : String(err);
    const failedAt = new Date().toISOString();
    writeRunState(cwd, {
      runId,
      agent: agent.name,
      status: "failed",
      startedAt,
      error: errorMsg,
    });

    runOutcome = {
      agent: agent.name,
      status: "failed",
      startedAt,
      completedAt: failedAt,
      error: errorMsg,
      focus,
      task: task ? { title: task.title, severity: task.severity } : undefined,
      branch: branchName,
      reviewedPR: reviewContext ? { number: reviewContext.pr.number, title: reviewContext.pr.title, url: reviewContext.pr.url } : undefined,
    };
  } finally {
    if (task && !taskSettled) {
      await releaseTask(taskManager, task);
    }
    if (runOutcome) {
      await runOnFinishHook(agent, runOutcome, cwd);
      await runMemoryCuration(agent, memoryUpdate, focus, cwd);
    }
    if (worktreePath) {
      log.section("Cleanup");
      removeWorktree(cwd, worktreePath);
      log.step("worktree removed");
    }
    if (runOutcome) {
      log.finished(agent.name, runOutcome.status === "failed" ? runOutcome.error : undefined);
    }
    agentLock?.release();
    if (runOutcome?.status === "failed") {
      process.exit(1);
    }
  }
}

function commitsAgentChanges(
  worktreePath: string,
  output: AgentOutput,
  unresolvedConflicts: string[],
): boolean {
  return Boolean(output.commitMessage) &&
    (output.status === "completed" || output.status === "partial") &&
    unresolvedConflicts.length === 0 &&
    hasUncommittedChanges(worktreePath);
}

function mergedBaseCleanly(followUp: PRFollowUpContext | undefined): boolean {
  return followUp?.baseMerge?.conflictedFiles.length === 0;
}

function removeTriggerLabel(config: VteamConfig, agent: AgentConfig, prNumber: number, cwd: string): void {
  if (!agent.prTriggerLabel) return;
  try {
    removePRLabel(config.platform, prNumber, agent.prTriggerLabel, cwd);
    log.ok(`removed label "${agent.prTriggerLabel}" from PR #${prNumber}`);
  } catch (err) {
    log.warn(`failed to remove label "${agent.prTriggerLabel}" from PR #${prNumber}: ${messageOf(err)}`);
  }
}

async function claimTask(
  taskManager: TaskManager,
  config: VteamConfig,
  agent: AgentConfig,
  runId: string,
  locksDir: string,
): Promise<Task | undefined> {
  const claimLock = await acquireLock(resolve(locksDir, "task-claim"), agent.name);
  try {
    const readyTasks = await taskManager.listReady();
    const task = pickNextTask(readyTasks, agent.taskFilter ?? {}, config.tasks.maxRetries);
    if (task) {
      await taskManager.update(task.ref, { status: "active", metadata: claimMetadata(runId) });
    }
    return task;
  } finally {
    claimLock.release();
  }
}

async function parkTaskWithClosedPR(
  taskManager: TaskManager,
  task: Task,
  agentName: string,
  prUrl: string,
  state: string,
): Promise<void> {
  log.warn(`PR ${prUrl} is ${state}, moving task to backlog`);
  await taskManager.update(task.ref, { status: "backlog", removeMetadata: [...CLAIM_KEYS] });
  await addRunNote(
    taskManager,
    task,
    `${agentName}: skipped\n\nPR ${prUrl} is ${state}, so there is no branch to push to. Remove the pr-url metadata and move the task to todo to start over on a new branch.`,
  );
}

async function releaseTask(taskManager: TaskManager, task: Task): Promise<void> {
  try {
    await taskManager.update(task.ref, { status: "todo", removeMetadata: [...CLAIM_KEYS] });
  } catch (err) {
    log.warn(`failed to return task ${task.ref} to todo: ${messageOf(err)}`);
  }
}

function postThreadReplies(
  config: VteamConfig,
  agent: AgentConfig,
  followUp: PRFollowUpContext,
  replies: ThreadReply[],
  commitSha: string,
  cwd: string,
): number {
  const threadsByRef = new Map(followUp.threads.map((thread, index) => [threadRef(index), thread]));
  let posted = 0;

  for (const reply of replies) {
    const thread = threadsByRef.get(reply.thread);
    if (!thread) {
      log.warn(`skipping reply to unknown review thread ${reply.thread}`);
      continue;
    }

    try {
      replyToReviewThread(
        config.platform,
        followUp.pr.number,
        thread.id,
        `🤖 **vteam ${agent.name}:** ${reply.body}\n\nFixed in ${commitSha.slice(0, 7)}.`,
        cwd,
      );
      posted++;
    } catch (err) {
      log.warn(`failed to reply to review thread ${reply.thread}: ${messageOf(err)}`);
      continue;
    }

    if (agent.resolveThreads) {
      try {
        resolveReviewThread(config.platform, followUp.pr.number, thread.id, cwd);
      } catch (err) {
        log.warn(`failed to resolve review thread ${reply.thread}: ${messageOf(err)}`);
      }
    }
  }

  return posted;
}

function formatRunNote(
  agentName: string,
  output: AgentOutput,
  prUrl: string | undefined,
  extraNotes: string[],
): string {
  return [
    `${agentName}: ${output.status}`,
    output.summary,
    output.blockerReason ? `Blocker: ${output.blockerReason}` : "",
    ...extraNotes,
    prUrl ? `PR: ${prUrl}` : "",
  ]
    .filter(Boolean)
    .join("\n\n");
}

async function addRunNote(taskManager: TaskManager, task: Task, text: string): Promise<void> {
  try {
    await taskManager.addNote(task.ref, text);
  } catch (err) {
    log.warn(`failed to add note to task ${task.ref}: ${messageOf(err)}`);
  }
}

async function runOnFinishHook(
  agent: AgentConfig,
  outcome: RunOutcome,
  cwd: string,
): Promise<void> {
  if (!agent.onFinish) return;

  log.section(`On-finish hook${agent.onFinish.model ? ` (${agent.onFinish.model})` : ""}`);
  const { systemPrompt, userPrompt } = buildOnFinishPrompt(
    agent.onFinish,
    outcome,
  );

  try {
    await runClaudeAgent({
      systemPrompt,
      userPrompt,
      cwd,
      model: agent.onFinish.model,
      allowedTools: agent.onFinish.allowedTools,
      disallowedTools: agent.onFinish.disallowedTools,
    });
  } catch (hookErr) {
    log.warn(`on-finish hook failed: ${messageOf(hookErr)}`);
  }
}

async function runMemoryCuration(
  agent: AgentConfig,
  memoryUpdate: string | undefined,
  focus: string | undefined,
  cwd: string,
): Promise<void> {
  if (!agent.memory || !memoryUpdate) return;

  log.section(`Memory curation${agent.memory.model ? ` (${agent.memory.model})` : ""}`);

  const memoryDir = resolve(cwd, "vteam", ".memory", agent.name);
  const memoryFilePath = resolve(memoryDir, "store.md");

  let currentMemory = "";
  if (existsSync(memoryFilePath)) {
    currentMemory = readFileSync(memoryFilePath, "utf-8");
  }

  const { systemPrompt, userPrompt } = buildMemoryCurationPrompt(
    agent.memory,
    currentMemory,
    memoryUpdate,
    focus,
  );

  try {
    const result = await runClaudeAgent({
      systemPrompt,
      userPrompt,
      cwd,
      model: agent.memory.model,
      allowedTools: agent.memory.allowedTools,
      disallowedTools: agent.memory.disallowedTools,
    });

    if (result.exitCode === 0 && result.resultText) {
      mkdirSync(memoryDir, { recursive: true });
      writeFileSync(memoryFilePath, result.resultText.trim() + "\n", "utf-8");
      log.ok("memory updated");
    } else {
      log.warn("memory curation produced no output or failed");
    }
  } catch (err) {
    log.warn(`memory curation failed: ${messageOf(err)}`);
  }
}
