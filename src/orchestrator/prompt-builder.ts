import { readFileSync } from "node:fs";
import { parse } from "../frontmatter.js";
import type { TaskManager } from "../tasks/task-manager.js";
import type { AgentConfig, Task, PRReviewContext, OnFinishConfig, MemoryConfig, RunOutcome } from "../types.js";

interface PromptParts {
  systemPrompt: string;
  userPrompt: string;
}

export async function buildPrompt(
  agent: AgentConfig,
  taskManager: TaskManager,
  task?: Task,
  review?: PRReviewContext,
  focus?: string,
  memoryContent?: string,
): Promise<PromptParts> {
  const raw = readFileSync(agent.agentMdPath, "utf-8");
  const { content } = parse(raw);

  const systemPrompt = content.trim();

  const sections: string[] = [];

  if (focus) {
    sections.push(
      `## Priority Focus\n\nThe user wants you to prioritize the following context above all other considerations:\n\n${focus}`,
    );
  }

  if (memoryContent) {
    sections.push(
      `## Agent Memory\n\nThe following is your accumulated memory from previous runs. Use it to inform your work and avoid repeating past efforts.\n\n${memoryContent}`,
    );
  }

  const index = await taskManager.getIndex();
  if (index.all.length > 0) {
    const lines = index.all.map((t) => {
      const files = t.files.join(", ");
      return `- [${t.status}] ${t.severity} | ${t.title} | ${files}`;
    });
    sections.push(
      `## Existing Tasks\n\nDo not report issues that already appear in this list.\n\n${lines.join("\n")}`,
    );
  }

  if (task) {
    sections.push(
      `## Your Task\n\nTitle: ${task.title}\nSeverity: ${task.severity}\nFiles: ${task.files.join(", ")}\n\n${task.description}`,
    );
  }

  if (review) {
    sections.push(buildReviewSection(review));
  }

  sections.push(buildOutputInstruction(agent));

  const userPrompt = sections.join("\n\n");
  return { systemPrompt, userPrompt };
}

export function buildOnFinishPrompt(
  onFinish: OnFinishConfig,
  outcome: RunOutcome,
): PromptParts {
  const raw = readFileSync(onFinish.onFinishMdPath, "utf-8");
  const { content } = parse(raw);
  const systemPrompt = content.trim();

  const sections: string[] = [];

  const outcomeLines = [
    `- Agent: ${outcome.agent}`,
    `- Status: ${outcome.status}`,
    `- Started: ${outcome.startedAt}`,
    `- Completed: ${outcome.completedAt}`,
  ];
  sections.push(`## Run Outcome\n\n${outcomeLines.join("\n")}`);

  if (outcome.focus) {
    sections.push(`## Focus Context\n\n${outcome.focus}`);
  }

  if (outcome.task) {
    sections.push(
      `## Task\n\n- Title: ${outcome.task.title}\n- Severity: ${outcome.task.severity}\n- Files: ${outcome.task.files.join(", ")}`,
    );
  }

  if (outcome.branch || outcome.prUrl) {
    const lines: string[] = [];
    if (outcome.branch) lines.push(`- Branch: ${outcome.branch}`);
    if (outcome.prUrl) lines.push(`- PR URL: ${outcome.prUrl}`);
    sections.push(`## Branch & PR\n\n${lines.join("\n")}`);
  }

  if (outcome.reviewedPR) {
    sections.push(
      `## Reviewed PR\n\n- Number: #${outcome.reviewedPR.number}\n- Title: ${outcome.reviewedPR.title}\n- URL: ${outcome.reviewedPR.url}`,
    );
  }

  if (outcome.tasksCreated?.length) {
    sections.push(`## Tasks Created\n\n${outcome.tasksCreated.map((f) => `- ${f}`).join("\n")}`);
  }

  if (outcome.commitMessage) {
    sections.push(`## Commit Message\n\n${outcome.commitMessage.subject}\n\n${outcome.commitMessage.body}`);
  }

  if (outcome.content) {
    const body = outcome.content.type === "generic"
      ? outcome.content.body
      : JSON.stringify(outcome.content.body, null, 2);
    sections.push(`## Content\n\n${body}`);
  }

  if (outcome.error) {
    sections.push(`## Error\n\n${outcome.error}`);
  }

  return { systemPrompt, userPrompt: sections.join("\n\n") };
}

export function buildMemoryCurationPrompt(
  memory: MemoryConfig,
  currentMemory: string,
  memoryUpdate: string,
  focus?: string,
): PromptParts {
  const raw = readFileSync(memory.memoryMdPath, "utf-8");
  const { content } = parse(raw);
  const systemPrompt = content.trim();

  const sections: string[] = [];

  if (focus) {
    sections.push(`## Run Context\n\nThe agent was invoked with this focus:\n\n${focus}`);
  }

  if (currentMemory) {
    sections.push(`## Current Memory\n\n${currentMemory}`);
  } else {
    sections.push(`## Current Memory\n\n(empty — this is the first memory entry)`);
  }

  sections.push(`## New Update\n\n${memoryUpdate}`);

  sections.push(
    `## Instructions\n\nReturn the complete replacement memory content. Merge the new update into the current memory according to your curation rules. Output ONLY the final memory content as plain text — no JSON, no fences.`,
  );

  return { systemPrompt, userPrompt: sections.join("\n\n") };
}

function buildOutputInstruction(agent: AgentConfig): string {
  const memoryNote = agent.memory
    ? `,\n  "memoryUpdate": "optional: brief notes about this run to remember for future runs (patterns noticed, decisions made, areas covered)"`
    : "";

  const contentInstruction = agent.output === "task"
    ? `  "content": {
    "type": "task",
    "body": {
      "title": "short descriptive title",
      "severity": "critical|high|medium|low",
      "description": "detailed description of the issue and its impact",
      "suggestedFix": "how to fix it",
      "files": ["file:line"]
    }
  }`
    : `  "content": {
    "type": "generic",
    "body": "your primary output as a string (analysis, review, report, etc.)"
  }`;

  const preamble = agent.output === "task"
    ? "Return your finding as a JSON object. Do NOT create or modify tasks yourself (no aven commands) — the orchestrator creates them from your output. Output ONLY valid JSON with no markdown fencing."
    : "After completing your work, output a JSON object as the LAST thing you produce. Do NOT run git add or git commit — the orchestrator handles committing and pushing. Output ONLY valid JSON with no markdown fencing.";

  return `## Output Format

${preamble}

{
  "status": "completed|partial|blocked|failed",
  "summary": "what you did",
${contentInstruction},
  "filesChanged": ["path/to/file1.ts"],
  "commitMessage": {
    "subject": "vteam: <short subject>",
    "body": "PR-ready description of the change"
  },
  "blockerReason": "only if status is blocked or failed"${memoryNote}
}

- "content" is optional. Include it when you have a primary deliverable (finding, review, analysis).
- "filesChanged" and "commitMessage" are optional. Include them only when you modified files.
- "blockerReason" is optional. Include it only when status is "blocked" or "failed".`;
}

function buildReviewSection(review: PRReviewContext): string {
  const lines: string[] = [
    `## Pull Request`,
    "",
    `Number: #${review.pr.number}`,
    `Repository: ${review.repoSlug}`,
    `Title: ${review.pr.title}`,
    `URL: ${review.pr.url}`,
    `Branch: ${review.pr.branch}`,
    "",
  ];

  if (review.comments.length > 0) {
    lines.push(
      `## Review Comments`,
      "",
      "Address each of the following review comments:",
      "",
    );

    for (const comment of review.comments) {
      if (comment.path) {
        lines.push(
          `### ${comment.author} on \`${comment.path}${comment.line ? `:${comment.line}` : ""}\``,
        );
      } else {
        lines.push(`### ${comment.author}`);
      }
      lines.push("");
      lines.push(comment.body);
      lines.push("");
    }
  }

  return lines.join("\n");
}
