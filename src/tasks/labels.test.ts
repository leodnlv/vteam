import { describe, it, expect } from "vitest";
import { agentTaskLabel, labelsOnTaskCreate, labelsOnTaskDone, toAvenLabel } from "./labels.js";
import type { TasksConfig } from "../types.js";

const tasks: TasksConfig = { maxRetries: 3, project: "app", label: "vteam" };

describe("toAvenLabel", () => {
  it.each([
    ["vteam-coder", "vteam-coder"],
    ["Foo_Bar", "foo-bar"],
    ["a:b", "a-b"],
    ["a  /  b", "a-b"],
    ["-a-", "a"],
    ["ünï", "n"],
  ])("normalizes %s to %s like aven", (input, expected) => {
    expect(toAvenLabel(input)).toBe(expected);
  });
});

describe("task label conventions", () => {
  it("derives the agent label from tasks.label and the agent name", () => {
    expect(agentTaskLabel(tasks, "coder")).toBe("vteam-coder");
    expect(agentTaskLabel({ ...tasks, label: "Bot" }, "Security_Analyst")).toBe("bot-security-analyst");
  });

  it("labels created tasks with the creating agent", () => {
    expect(labelsOnTaskCreate(tasks, "improver")).toEqual(["vteam-improver"]);
  });

  it("labels done tasks with the finishing agent", () => {
    expect(labelsOnTaskDone(tasks, "coder")).toEqual(["vteam-coder"]);
  });
});
