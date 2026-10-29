import { describe, it, expect } from "vitest";
import { agentConfigSchema } from "./schema.js";

function valid(fm: Record<string, unknown>): void {
  const result = agentConfigSchema.safeParse(fm);
  expect(result.success, `Expected valid but got: ${!result.success ? result.error.issues.map((i) => i.message).join(", ") : ""}`).toBe(true);
}

function invalid(fm: Record<string, unknown>, messageFragment?: string): void {
  const result = agentConfigSchema.safeParse(fm);
  expect(result.success).toBe(false);
  if (messageFragment && !result.success) {
    const messages = result.error.issues.map((i) => i.message).join("; ");
    expect(messages).toContain(messageFragment);
  }
}

describe("agent config combinations", () => {
  describe("output: task", () => {
    it("standalone", () => valid({ output: "task" }));

    it("with worktree", () => valid({ output: "task", worktree: true }));

    it("without worktree", () => valid({ output: "task", worktree: false }));

    it("with input: task", () => valid({ output: "task", input: "task" }));

    it("with input: pr and worktree", () =>
      valid({ output: "task", input: "pr", worktree: true }));

    it("rejects autoPR", () =>
      invalid({ output: "task", autoPR: true }, "output: \"task\" is incompatible with autoPR"));

    it("rejects readOnly", () =>
      invalid({ output: "task", readOnly: true, worktree: true }, "output: \"task\" is incompatible with readOnly"));

    it("rejects autoPR even with worktree", () =>
      invalid({ output: "task", worktree: true, autoPR: true }, "output: \"task\" is incompatible with autoPR"));
  });

  describe("worktree + readOnly", () => {
    it("readOnly requires worktree", () =>
      invalid({ readOnly: true }, "readOnly: true requires worktree: true"));

    it("readOnly with worktree false", () =>
      invalid({ readOnly: true, worktree: false }, "readOnly: true requires worktree: true"));

    it("readOnly with worktree true", () =>
      valid({ readOnly: true, worktree: true }));

    it("readOnly rejects autoPR", () =>
      invalid({ readOnly: true, worktree: true, autoPR: true }, "readOnly: true is incompatible with autoPR"));
  });

  describe("input: pr", () => {
    it("requires worktree", () =>
      invalid({ input: "pr" }, 'input: "pr" requires worktree: true'));

    it("with worktree false", () =>
      invalid({ input: "pr", worktree: false }, 'input: "pr" requires worktree: true'));

    it("with worktree true", () =>
      valid({ input: "pr", worktree: true }));

    it("with readOnly", () =>
      valid({ input: "pr", worktree: true, readOnly: true }));

    it("with autoPR", () =>
      valid({ input: "pr", worktree: true, autoPR: true }));
  });

  describe("common agent archetypes", () => {
    it("improver: output task, no worktree, read-only tools", () =>
      valid({
        output: "task",
        allowedTools: ["Read", "Glob", "Grep"],
      }));

    it("coder: worktree, input task, autoPR", () =>
      valid({
        worktree: true,
        input: "task",
        autoPR: true,
        prCreateLabels: ["vteam"],
      }));

    it("review-responder: worktree, input pr, trigger label", () =>
      valid({
        worktree: true,
        input: "pr",
        prTriggerLabel: "vteam:changes-requested",
      }));

    it("test-writer: worktree, input task, autoPR", () =>
      valid({
        worktree: true,
        input: "task",
        autoPR: true,
        prCreateLabels: ["vteam"],
      }));

    it("worktree scanner: worktree + output task (scan in isolation)", () =>
      valid({
        worktree: true,
        output: "task",
      }));

    it("readOnly auditor: worktree + readOnly, no task output", () =>
      valid({
        worktree: true,
        readOnly: true,
      }));

    it("bare agent: empty config", () =>
      valid({}));
  });

  describe("invalid archetypes", () => {
    it("task producer that also creates PRs", () =>
      invalid({ output: "task", autoPR: true }, "output: \"task\" is incompatible with autoPR"));

    it("readOnly task producer", () =>
      invalid(
        { output: "task", readOnly: true, worktree: true },
        "output: \"task\" is incompatible with readOnly",
      ));

    it("pr input without worktree", () =>
      invalid({ input: "pr" }, 'input: "pr" requires worktree: true'));

    it("readOnly without worktree", () =>
      invalid({ readOnly: true }, "readOnly: true requires worktree: true"));

    it("readOnly + autoPR", () =>
      invalid(
        { readOnly: true, worktree: true, autoPR: true },
        "readOnly: true is incompatible with autoPR",
      ));
  });
});
