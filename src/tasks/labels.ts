import type { TasksConfig } from "../types.js";

// Mirrors aven's label normalization, so the labels vteam writes match the ones aven stores.
export function toAvenLabel(name: string): string {
  return name
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, "-")
    .replace(/^-+|-+$/g, "");
}

export function agentTaskLabel(tasks: TasksConfig, agentName: string): string {
  return toAvenLabel(`${tasks.label}-${agentName}`);
}

export function labelsOnTaskCreate(tasks: TasksConfig, agentName: string): string[] {
  return [agentTaskLabel(tasks, agentName)];
}

export function labelsOnTaskDone(tasks: TasksConfig, agentName: string): string[] {
  return [agentTaskLabel(tasks, agentName)];
}
