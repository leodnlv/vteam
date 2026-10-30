import {
  existsSync,
  mkdirSync,
  readFileSync,
  writeFileSync,
} from "node:fs";
import { execFileSync } from "node:child_process";
import { resolve } from "node:path";
import { acquireLock, type FileLock } from "../memory/lock.js";
import { buildPrompt, buildOnFinishPrompt, buildMemoryCurationPrompt, threadRef } from "../orchestrator/prompt-builder.js";
import { runClaudeAgent } from "../orchestrator/agent-runner.js";
import { parseAgentOutput } from "../orchestrator/output-schema.js";
import { AvenTaskManager } from "../tasks/aven-manager.js";
import { matchesTaskFilter, severityPriority, type TaskManager } from "../tasks/task-manager.js";
import { CLAIM_KEYS, claimMetadata } from "../tasks/claim.js";
import {
  createWorktree,
  checkoutWorktree,
  removeWorktree,
  pushBranch,
  getCommitSha,
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
import { resolveAgentConfig, listAgentNames } from "../config/agent.js";
import { startRunLog } from "../run-log.js";
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

function writeRunState(cwd: string, state: RunState): void {
  const runsDir = resolve(cwd, "vteam", ".runs");
  mkdirSync(runsDir, { recursive: true });
  writeFileSync(
    resolve(runsDir, `${state.runId}.json`),
    JSON.stringify(state, null, 2),
  );
}

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
  console.log(`Logging to ${runLog.path}`);
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
  let task: Task | undefined;
  let taskSettled = false;
  let reviewContext: PRReviewContext | undefined;
  let followUp: PRFollowUpContext | undefined;
  let threadReplyNote: string | undefined;
  let prUrl: string | undefined;
  let memoryUpdate: string | undefined;
  let runOutcome: RunOutcome | undefined;

  try {
    console.log("Acquiring lock...");
    agentLock = await acquireLock(
      resolve(locksDir, agent.name),
      agent.name,
    );

    if (agent.input === "task") {
      task = await claimTask(taskManager, config, agent, runId, locksDir);
      if (!task) {
        console.log("No eligible tasks. Nothing to do.");
        return;
      }
      console.log(`Picked task: ${task.ref} ${task.title} (${task.severity})`);

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
        console.log(
          `Following up on PR #${pr.number} (${followUp.threads.length} open review threads)`,
        );
      }
    }

    if (agent.input === "pr") {
      const labels = agent.prTriggerLabel ? [agent.prTriggerLabel] : [];
      const pr = findReviewablePR(config.platform, labels, cwd);
      if (!pr) {
        console.log("No PRs need review response. Nothing to do.");
        return;
      }
      const comments = getReviewComments(config.platform, pr.number, cwd);
      const repoSlug = getRepoSlug(config.platform, cwd);
      reviewContext = { pr, comments, repoSlug };
      console.log(
        `Picked PR #${pr.number}: ${pr.title}${comments.length > 0 ? ` (${comments.length} comments)` : ""}`,
      );
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
        console.log(`Checking out PR branch: ${existingPR.branch}...`);
        const wt = checkoutWorktree(
          cwd,
          existingPR.branch,
          config.worktreeDir,
        );
        worktreePath = wt.path;
        branchName = wt.branch;
      } else {
        console.log("Creating worktree...");
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
      }
      agentCwd = worktreePath;
      runState.worktreePath = worktreePath;
      runState.branchName = branchName;
      writeRunState(cwd, runState);
      console.log(`Worktree: ${worktreePath} (branch: ${branchName})`);
    }

    let memoryContent: string | undefined;
    const memoryFilePath = resolve(cwd, "vteam", ".memory", agent.name, "store.md");
    if (agent.memory && existsSync(memoryFilePath)) {
      memoryContent = readFileSync(memoryFilePath, "utf-8").trim() || undefined;
    }

    console.log("Building prompt...");
    const { systemPrompt, userPrompt } = await buildPrompt(
      agent,
      taskManager,
      task,
      reviewContext ?? followUp,
      focus,
      memoryContent,
    );

    console.log(`Running ${agent.name} agent...`);
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

    const output = parseAgentOutput(result.resultText);
    memoryUpdate = output.memoryUpdate;
    runState.claudeOutput = output;
    runState.commitMessage = output.commitMessage;

    if (output.content?.type === "task") {
      const taskId = await taskManager.create(output.content.body, agent.name);
      runState.tasksCreated = [taskId];
      console.log(`Created task: ${taskId} ${output.content.body.title} (${output.content.body.severity})`);
    }

    if (agent.worktree && agent.readOnly && worktreePath) {
      console.log(`\n${agent.name} finished (readOnly). No commit/push.`);

      if (reviewContext && agent.prTriggerLabel) {
        removePRLabel(
          config.platform,
          reviewContext.pr.number,
          agent.prTriggerLabel,
          cwd,
        );
        console.log(`Removed label "${agent.prTriggerLabel}" from PR #${reviewContext.pr.number}.`);
      }
    } else if (agent.worktree && worktreePath && branchName &&
      output.commitMessage &&
      (output.status === "completed" || output.status === "partial") &&
      hasUncommittedChanges(worktreePath)) {
      console.log(`\n${agent.name} made changes. Committing...`);
      execFileSync("git", ["add", "-A"], { cwd: worktreePath, stdio: "pipe" });
      execFileSync(
        "git",
        ["commit", "-m", output.commitMessage.subject, "-m", output.commitMessage.body],
        { cwd: worktreePath, stdio: "pipe" },
      );
      runState.commitSha = getCommitSha(worktreePath);

      console.log(`Pushing...`);
      pushBranch(worktreePath, branchName);

      if (reviewContext) {
        try {
          postPRComment(
            config.platform,
            reviewContext.pr.number,
            `Addressed review feedback in \`${runState.commitSha.slice(0, 7)}\`.`,
            cwd,
          );
          console.log(`Comment posted on PR #${reviewContext.pr.number}.`);
        } catch (commentErr) {
          console.error(
            "Failed to post PR comment:",
            commentErr instanceof Error ? commentErr.message : commentErr,
          );
        }

        if (agent.prTriggerLabel) {
          removePRLabel(
            config.platform,
            reviewContext.pr.number,
            agent.prTriggerLabel,
            cwd,
          );
          console.log(`Removed label "${agent.prTriggerLabel}" from PR #${reviewContext.pr.number}.`);
        }
      }

      if (followUp) {
        console.log(`Pushed to existing PR: ${followUp.pr.url}`);
        if (output.threadReplies?.length) {
          const posted = postThreadReplies(config, agent, followUp, output.threadReplies, runState.commitSha, cwd);
          threadReplyNote = `Replied to ${posted} of ${output.threadReplies.length} review threads.`;
          console.log(threadReplyNote);
        }
      } else if (agent.autoPR) {
        console.log("Creating pull request...");
        try {
          const prBody = output.commitMessage.body || `Automated by vteam (${agent.name}).${task ? `\n\nTask: ${task.title}` : ""}`;
          const summary = task?.title || output.commitMessage.subject.replace(/^vteam:\s*/i, "") || "";
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
          console.log(`PR created: ${prUrl}`);
        } catch (mrErr) {
          console.error(
            "PR creation failed:",
            mrErr instanceof Error ? mrErr.message : mrErr,
          );
          console.log("Branch was pushed. Create the PR manually.");
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
        });
        taskSettled = true;
        console.log("Task completed.");
      }
    } else if (agent.worktree && worktreePath && branchName) {
      console.log(`\n${agent.name} did not produce changes.`);
      if (output.status === "blocked" || output.status === "failed") {
        console.log(`Reason: ${output.blockerReason ?? output.summary}`);
      }
      if (task) {
        const retryCount = task.retryCount + 1;
        await taskManager.update(task.ref, {
          status: "todo",
          metadata: { "retry-count": String(retryCount) },
          removeMetadata: [...CLAIM_KEYS],
        });
        taskSettled = true;
        console.log(`Retry count: ${retryCount}/${config.tasks.maxRetries}`);
      }
    }

    if (task) {
      await addRunNote(taskManager, task, formatRunNote(agent.name, output, prUrl, threadReplyNote));
    }

    writeRunState(cwd, runState);

    console.log(`\n${agent.name} finished.`);
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
      task: task ? { title: task.title, severity: task.severity, files: task.files } : undefined,
      branch: branchName,
      prUrl,
      reviewedPR: reviewContext ? { number: reviewContext.pr.number, title: reviewContext.pr.title, url: reviewContext.pr.url } : undefined,
      tasksCreated: runState.tasksCreated,
      commitMessage: runState.commitMessage,
      content: output.content,
    };
  } catch (err) {
    const errorMsg = err instanceof Error ? err.message : String(err);
    console.error(`${agent.name} failed:`, errorMsg);
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
      task: task ? { title: task.title, severity: task.severity, files: task.files } : undefined,
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
      console.log("Cleaning up worktree...");
      removeWorktree(cwd, worktreePath);
    }
    agentLock?.release();
    if (runOutcome?.status === "failed") {
      process.exit(1);
    }
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
    const [task] = readyTasks
      .filter((t) => matchesTaskFilter(t, agent.taskFilter ?? {}))
      .filter((t) => t.retryCount < config.tasks.maxRetries)
      .sort(
        (a, b) =>
          severityPriority(a.severity) - severityPriority(b.severity) ||
          a.createdAt.localeCompare(b.createdAt),
      );
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
  console.log(`PR ${prUrl} is ${state}. Moving task to backlog.`);
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
    console.error(
      `Failed to return task ${task.ref} to todo:`,
      err instanceof Error ? err.message : err,
    );
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
      console.error(`Skipping reply to unknown review thread ${reply.thread}.`);
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
      console.error(
        `Failed to reply to review thread ${reply.thread}:`,
        err instanceof Error ? err.message : err,
      );
      continue;
    }

    if (agent.resolveThreads) {
      try {
        resolveReviewThread(config.platform, followUp.pr.number, thread.id, cwd);
      } catch (err) {
        console.error(
          `Failed to resolve review thread ${reply.thread}:`,
          err instanceof Error ? err.message : err,
        );
      }
    }
  }

  return posted;
}

function formatRunNote(
  agentName: string,
  output: AgentOutput,
  prUrl: string | undefined,
  threadReplyNote: string | undefined,
): string {
  return [
    `${agentName}: ${output.status}`,
    output.summary,
    output.blockerReason ? `Blocker: ${output.blockerReason}` : "",
    threadReplyNote ?? "",
    prUrl ? `PR: ${prUrl}` : "",
  ]
    .filter(Boolean)
    .join("\n\n");
}

async function addRunNote(taskManager: TaskManager, task: Task, text: string): Promise<void> {
  try {
    await taskManager.addNote(task.ref, text);
  } catch (err) {
    console.error(
      `Failed to add note to task ${task.ref}:`,
      err instanceof Error ? err.message : err,
    );
  }
}

async function runOnFinishHook(
  agent: AgentConfig,
  outcome: RunOutcome,
  cwd: string,
): Promise<void> {
  if (!agent.onFinish) return;

  console.log(`\nRunning on-finish hook for ${agent.name}...`);
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
    console.log("On-finish hook completed.");
  } catch (hookErr) {
    console.error(
      "On-finish hook failed:",
      hookErr instanceof Error ? hookErr.message : hookErr,
    );
  }
}

async function runMemoryCuration(
  agent: AgentConfig,
  memoryUpdate: string | undefined,
  focus: string | undefined,
  cwd: string,
): Promise<void> {
  if (!agent.memory || !memoryUpdate) return;

  console.log(`\nRunning memory curation for ${agent.name}...`);

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
      console.log("Memory updated.");
    } else {
      console.error("Memory curation produced no output or failed.");
    }
  } catch (err) {
    console.error(
      "Memory curation failed:",
      err instanceof Error ? err.message : err,
    );
  }
}
