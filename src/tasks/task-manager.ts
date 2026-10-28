import type { Task, TaskContentBody, TaskMetadata, TaskStatus, Severity } from "../types.js";

export interface TaskIndex {
  all: Task[];
  byStatus: Map<TaskStatus, Task[]>;
  titles: string[];
}

export interface TaskUpdate {
  status?: TaskStatus;
  metadata?: TaskMetadata;
}

export interface TaskManager {
  listReady(actor: string): Promise<Task[]>;
  getIndex(): Promise<TaskIndex>;
  create(body: TaskContentBody, foundBy: string, actor?: string): Promise<string>;
  update(ref: string, changes: TaskUpdate): Promise<void>;
}

export function severityPriority(severity: Severity): number {
  const map: Record<Severity, number> = {
    critical: 0,
    high: 1,
    medium: 2,
    low: 3,
  };
  return map[severity];
}

export function formatTaskDescription(body: TaskContentBody): string {
  return [
    `## Description\n\n${body.description}`,
    body.suggestedFix ? `## Suggested Fix\n\n${body.suggestedFix}` : "",
    `## Affected Files\n\n${body.files.map((f) => `- \`${f}\``).join("\n")}`,
  ]
    .filter(Boolean)
    .join("\n\n");
}
