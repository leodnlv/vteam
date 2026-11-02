import { spawn } from "node:child_process";
import { createReadStream, writeFileSync, rmSync, mkdtempSync } from "node:fs";
import { resolve, join, relative, isAbsolute } from "node:path";
import * as log from "../log.js";
import { tmpdir } from "node:os";

interface AgentRunOptions {
  systemPrompt: string;
  userPrompt: string;
  cwd: string;
  model?: string;
  timeoutMs?: number;
  allowedTools?: string[];
  disallowedTools?: string[];
}

export interface AgentRunResult {
  stdout: string;
  stderr: string;
  exitCode: number;
  resultText: string | null;
  costUsd?: number;
  durationMs?: number;
}

export interface ContentBlock {
  type: string;
  text?: string;
  name?: string;
  input?: Record<string, unknown>;
  is_error?: boolean;
  content?: string | { type: string; text?: string }[];
}

interface StreamEvent {
  type: string;
  subtype?: string;
  message?: { content?: ContentBlock[] | string };
  result?: string;
  total_cost_usd?: number;
  duration_ms?: number;
  num_turns?: number;
}

function asString(value: unknown): string | undefined {
  return typeof value === "string" && value ? value : undefined;
}

function displayPath(path: string, cwd: string): string {
  const rel = relative(cwd, path);
  return rel && !rel.startsWith("..") && !isAbsolute(rel) ? rel : path;
}

export function describeTool(name: string, input: Record<string, unknown>, cwd: string): string {
  const path = asString(input.file_path) ?? asString(input.notebook_path) ?? asString(input.path);
  switch (name) {
    case "Bash":
      return asString(input.command) ?? "";
    case "Read":
    case "Write":
    case "Edit":
    case "MultiEdit":
    case "NotebookEdit":
      return path ? displayPath(path, cwd) : "";
    case "Grep":
    case "Glob": {
      const pattern = asString(input.pattern) ?? "";
      return path ? `${pattern}  in ${displayPath(path, cwd)}` : pattern;
    }
    case "WebFetch":
      return asString(input.url) ?? "";
    case "WebSearch":
      return asString(input.query) ?? "";
    default:
      return asString(input.description) ?? asString(input.command) ?? asString(input.pattern) ??
        (path ? displayPath(path, cwd) : undefined) ?? asString(input.url) ?? asString(input.query) ?? "";
  }
}

export function toolErrorText(block: ContentBlock): string | undefined {
  if (block.type !== "tool_result" || !block.is_error) return undefined;
  if (typeof block.content === "string") return block.content || "tool failed";
  const text = block.content?.map((part) => part.text ?? "").join("\n").trim();
  return text || "tool failed";
}

function renderEvent(event: StreamEvent, cwd: string): void {
  const content = event.message?.content;
  if (!Array.isArray(content)) return;

  if (event.type === "assistant") {
    for (const block of content) {
      if (block.type === "text" && block.text?.trim()) log.claudeText(block.text);
      if (block.type === "tool_use" && block.name) log.toolCall(block.name, describeTool(block.name, block.input ?? {}, cwd));
    }
  }

  if (event.type === "user") {
    for (const block of content) {
      const error = toolErrorText(block);
      if (error) log.toolError(error);
    }
  }
}

export async function runClaudeAgent(
  options: AgentRunOptions,
): Promise<AgentRunResult> {
  const tmp = mkdtempSync(join(tmpdir(), "vteam-"));
  const systemPromptFile = resolve(tmp, "system-prompt.md");

  const userPromptFile = resolve(tmp, "user-prompt.md");

  writeFileSync(systemPromptFile, options.systemPrompt, "utf-8");
  writeFileSync(userPromptFile, options.userPrompt, "utf-8");

  const args = [
    "-p",
    "--append-system-prompt-file",
    systemPromptFile,
    "--output-format",
    "stream-json",
    "--verbose",
    "--no-session-persistence",
  ];

  if (options.model) {
    args.push("--model", options.model);
  }

  if (options.allowedTools?.length) {
    args.push("--allowedTools", ...options.allowedTools);
  }

  if (options.disallowedTools?.length) {
    args.push("--disallowedTools", ...options.disallowedTools);
  }

  log.field("system", systemPromptFile);
  log.field("prompt", userPromptFile);
  console.log("");

  try {
    return await new Promise<AgentRunResult>((resolve, reject) => {
      const proc = spawn("claude", args, {
        cwd: options.cwd,
        stdio: ["pipe", "pipe", "pipe"],
        env: { ...process.env },
      });

      const timeoutMs = options.timeoutMs ?? 30 * 60 * 1000;
      const deadline = setTimeout(() => {
        log.fail(`Claude timed out after ${log.formatDuration(timeoutMs)}, sending SIGTERM`);
        proc.kill("SIGTERM");
      }, timeoutMs);

      createReadStream(userPromptFile).pipe(proc.stdin);

      let stdout = "";
      let stderr = "";
      let lineBuffer = "";
      let resultText: string | null = null;
      let stderrBuffer = "";
      let costUsd: number | undefined;
      let durationMs: number | undefined;
      let turns: number | undefined;

      const recordResult = (event: StreamEvent) => {
        if (event.result) resultText = event.result;
        costUsd = event.total_cost_usd;
        durationMs = event.duration_ms;
        turns = event.num_turns;
      };

      proc.stdout.on("data", (data: Buffer) => {
        const chunk = data.toString();
        stdout += chunk;
        lineBuffer += chunk;

        const lines = lineBuffer.split("\n");
        lineBuffer = lines.pop() ?? "";

        for (const line of lines) {
          const trimmed = line.trim();
          if (!trimmed) continue;
          let event: StreamEvent;
          try {
            event = JSON.parse(trimmed);
          } catch {
            log.step(trimmed);
            continue;
          }
          renderEvent(event, options.cwd);
          if (event.type === "result") recordResult(event);
        }
      });

      proc.stderr.on("data", (data: Buffer) => {
        const chunk = data.toString();
        stderr += chunk;
        stderrBuffer += chunk;
        const lines = stderrBuffer.split("\n");
        stderrBuffer = lines.pop() ?? "";
        for (const line of lines) {
          if (line.trim()) log.warn(`claude: ${line}`);
        }
      });

      proc.on("error", (err) => {
        reject(new Error(`Failed to spawn claude: ${err.message}`));
      });

      proc.on("close", (code) => {
        clearTimeout(deadline);
        if (stderrBuffer.trim()) log.warn(`claude: ${stderrBuffer}`);
        if (lineBuffer.trim()) {
          try {
            const event: StreamEvent = JSON.parse(lineBuffer.trim());
            if (event.type === "result") recordResult(event);
          } catch {
            // ignore
          }
        }

        log.agentExit({ exitCode: code ?? 1, durationMs, turns, costUsd });
        resolve({
          stdout,
          stderr,
          exitCode: code ?? 1,
          resultText,
          costUsd,
          durationMs,
        });
      });
    });
  } finally {
    try {
      rmSync(tmp, { recursive: true, force: true });
    } catch {
      // best-effort cleanup
    }
  }
}
