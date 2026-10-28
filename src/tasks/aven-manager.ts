import { spawn } from "node:child_process";
import { formatTaskDescription } from "./task-manager.js";
import type { TaskIndex, TaskManager, TaskUpdate } from "./task-manager.js";
import type { Severity, Task, TaskContentBody, TaskStatus, TasksConfig } from "../types.js";

export type AvenExec = (args: string[], stdin?: string) => Promise<string>;

export class AvenError extends Error {
  constructor(
    readonly args: string[],
    readonly output: string,
  ) {
    super(`aven ${args.join(" ")} failed: ${output}`);
  }
}

export const runAven: AvenExec = (args, stdin) =>
  new Promise((resolvePromise, reject) => {
    const child = spawn("aven", args, { stdio: ["pipe", "pipe", "pipe"] });
    let stdout = "";
    let stderr = "";
    child.stdout.on("data", (chunk: Buffer) => (stdout += chunk.toString()));
    child.stderr.on("data", (chunk: Buffer) => (stderr += chunk.toString()));
    child.on("error", (err: NodeJS.ErrnoException) => {
      reject(
        err.code === "ENOENT"
          ? new Error("aven CLI not found on PATH. vteam requires aven for task management.")
          : err,
      );
    });
    child.on("close", (code) => {
      if (code === 0) resolvePromise(stdout);
      else reject(new AvenError(args, (stderr || stdout).trim()));
    });
    child.stdin.end(stdin ?? "");
  });

const PRIORITY_BY_SEVERITY: Record<Severity, string> = {
  critical: "urgent",
  high: "high",
  medium: "medium",
  low: "low",
};

const SEVERITY_BY_PRIORITY: Record<string, Severity> = {
  urgent: "critical",
  high: "high",
  medium: "medium",
  low: "low",
  none: "low",
};

const SHOW_CONCURRENCY = 8;

interface AvenTaskSummary {
  ref: string;
}

interface AvenTaskDetail {
  task: {
    ref: string;
    title: string;
    status: TaskStatus;
    priority: string;
    created_at: string;
  };
  description: string;
  metadata: Record<string, string>;
}

function isAvenError(err: unknown, code: RegExp): boolean {
  return err instanceof AvenError && code.test(err.output);
}

const MISSING_SCOPE = /unknown-(project|label)/;

function toTask(detail: AvenTaskDetail): Task {
  const { task, description, metadata } = detail;
  return {
    ref: task.ref,
    title: task.title,
    status: task.status,
    severity: SEVERITY_BY_PRIORITY[task.priority] ?? "low",
    files: (metadata.files ?? "").split(/,\s*/).filter(Boolean),
    retryCount: Number.parseInt(metadata["retry-count"] ?? "", 10) || 0,
    metadata,
    description,
    createdAt: task.created_at,
  };
}

async function mapWithLimit<T, R>(
  items: T[],
  limit: number,
  fn: (item: T) => Promise<R>,
): Promise<R[]> {
  const results: R[] = new Array(items.length);
  let next = 0;
  const worker = async () => {
    while (next < items.length) {
      const i = next++;
      results[i] = await fn(items[i]);
    }
  };
  await Promise.all(Array.from({ length: Math.min(limit, items.length) }, worker));
  return results;
}

export class AvenTaskManager implements TaskManager {
  constructor(
    private readonly config: TasksConfig,
    private readonly exec: AvenExec = runAven,
  ) {}

  listReady(): Promise<Task[]> {
    return this.list(["--status", "todo", "--ready"]);
  }

  async getIndex(): Promise<TaskIndex> {
    const all = await this.list([]);
    const byStatus = new Map<TaskStatus, Task[]>();
    for (const task of all) {
      byStatus.set(task.status, [...(byStatus.get(task.status) ?? []), task]);
    }
    return { all, byStatus, titles: all.map((t) => t.title) };
  }

  async create(body: TaskContentBody, foundBy: string): Promise<string> {
    await this.aven(["label", "create", this.config.label]);
    const output = await this.aven(
      [
        "add",
        body.title,
        "--project", this.config.project,
        "--status", "todo",
        "--priority", PRIORITY_BY_SEVERITY[body.severity],
        "--label", this.config.label,
        "--metadata", `found-by=${foundBy}`,
        "--metadata", `files=${body.files.join(", ")}`,
        "--description-stdin",
      ],
      formatTaskDescription(body),
    );
    const ref = /^created (\S+)/m.exec(output)?.[1];
    if (!ref) {
      throw new Error(`Could not parse task ref from aven output: ${output.trim()}`);
    }
    return ref;
  }

  async update(ref: string, changes: TaskUpdate): Promise<void> {
    const args = ["edit", ref];
    if (changes.status) args.push("--status", changes.status);
    for (const [key, value] of Object.entries(changes.metadata ?? {})) {
      if (value !== undefined) args.push("--metadata", `${key}=${value}`);
    }
    await this.aven(args);
  }

  private async list(filters: string[]): Promise<Task[]> {
    let summaries: AvenTaskSummary[];
    try {
      const output = await this.aven([
        "list",
        "--project", this.config.project,
        "--label", this.config.label,
        ...filters,
        "--json",
      ]);
      summaries = JSON.parse(output) as AvenTaskSummary[];
    } catch (err) {
      if (isAvenError(err, MISSING_SCOPE)) return [];
      throw err;
    }

    return mapWithLimit(summaries, SHOW_CONCURRENCY, async ({ ref }) => {
      const output = await this.aven(["show", ref, "--full", "--json"]);
      return toTask(JSON.parse(output) as AvenTaskDetail);
    });
  }

  private aven(args: string[], stdin?: string): Promise<string> {
    const scoped = this.config.workspace
      ? ["--workspace", this.config.workspace, ...args]
      : args;
    return this.exec(scoped, stdin);
  }
}
