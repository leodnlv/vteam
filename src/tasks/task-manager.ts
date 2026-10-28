import type {
  Task,
  TaskContentBody,
  TaskFilter,
  TaskMetadata,
  TaskStatus,
  Severity,
} from "../types.js";

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
  listReady(): Promise<Task[]>;
  getIndex(): Promise<TaskIndex>;
  create(body: TaskContentBody, foundBy: string): Promise<string>;
  update(ref: string, changes: TaskUpdate): Promise<void>;
}

export function matchesTaskFilter(task: Task, filter: TaskFilter): boolean {
  return Object.entries(filter).every(([key, expected]) => {
    const accepted = Array.isArray(expected) ? expected : [expected];
    return accepted.includes(task.metadata[key] || null);
  });
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
