import { describe, it, expect } from "vitest";
import { vteamConfigSchema, agentConfigSchema, subagentConfigSchema } from "./schema.js";

describe("vteamConfigSchema", () => {
  const validConfig = {
    baseBranch: "main",
    platform: "github",
    worktreeDir: ".vteam-worktrees",
    tasks: { maxRetries: 3, project: "app" },
  };

  it("accepts a valid config", () => {
    const result = vteamConfigSchema.safeParse(validConfig);
    expect(result.success).toBe(true);
  });

  it("rejects unknown platform", () => {
    const result = vteamConfigSchema.safeParse({
      ...validConfig,
      platform: "bitbucket",
    });
    expect(result.success).toBe(false);
  });

  it("rejects missing baseBranch", () => {
    const { baseBranch: _, ...noBase } = validConfig;
    const result = vteamConfigSchema.safeParse(noBase);
    expect(result.success).toBe(false);
  });

  it("rejects negative maxRetries", () => {
    const result = vteamConfigSchema.safeParse({
      ...validConfig,
      tasks: { ...validConfig.tasks, maxRetries: -1 },
    });
    expect(result.success).toBe(false);
  });

  it("defaults label to vteam", () => {
    const result = vteamConfigSchema.safeParse(validConfig);
    expect(result.success).toBe(true);
    if (result.success) {
      expect(result.data.tasks.label).toBe("vteam");
    }
  });

  it("accepts optional workspace", () => {
    const result = vteamConfigSchema.safeParse({
      ...validConfig,
      tasks: { ...validConfig.tasks, workspace: "work" },
    });
    expect(result.success).toBe(true);
  });

  it("rejects missing project", () => {
    const result = vteamConfigSchema.safeParse({
      ...validConfig,
      tasks: { maxRetries: 3 },
    });
    expect(result.success).toBe(false);
  });

  it("rejects empty project", () => {
    const result = vteamConfigSchema.safeParse({
      ...validConfig,
      tasks: { ...validConfig.tasks, project: "" },
    });
    expect(result.success).toBe(false);
  });
});

describe("agentConfigSchema", () => {
  it("accepts taskFilter with strings, lists and null", () => {
    const result = agentConfigSchema.safeParse({
      input: "task",
      taskFilter: { "found-by": ["improver", "security-analyst", null], actor: "coder", owner: null },
    });
    expect(result.success).toBe(true);
  });

  it("rejects taskFilter without input: task", () => {
    const result = agentConfigSchema.safeParse({ taskFilter: { actor: "coder" } });
    expect(result.success).toBe(false);
  });

  it("rejects non-string taskFilter values and empty lists", () => {
    expect(agentConfigSchema.safeParse({ input: "task", taskFilter: { n: 1 } }).success).toBe(false);
    expect(agentConfigSchema.safeParse({ input: "task", taskFilter: { n: [] } }).success).toBe(false);
  });

  it("rejects unknown keys", () => {
    expect(agentConfigSchema.safeParse({ modle: "sonnet" }).success).toBe(false);
    expect(agentConfigSchema.safeParse({ memory: { modle: "haiku" } }).success).toBe(false);
  });

  it("accepts $schema, memory and onFinish sections", () => {
    const result = agentConfigSchema.safeParse({
      $schema: "../../agent.schema.json",
      memory: { model: "haiku" },
      onFinish: { model: "haiku", allowedTools: ["Bash(curl *)"] },
    });
    expect(result.success).toBe(true);
  });

  it("accepts empty config (bare AGENT.md)", () => {
    const result = agentConfigSchema.safeParse({});
    expect(result.success).toBe(true);
  });

  it("accepts full config", () => {
    const result = agentConfigSchema.safeParse({
      model: "sonnet",
      worktree: true,
      input: "task",
      autoPR: true,
      prCreateLabels: ["vteam"],
      allowedTools: ["Read", "Glob"],
    });
    expect(result.success).toBe(true);
  });

  it("accepts autoPR without worktree", () => {
    const result = agentConfigSchema.safeParse({
      autoPR: true,
      worktree: false,
    });
    expect(result.success).toBe(true);
  });

  it("rejects input pr without worktree", () => {
    const result = agentConfigSchema.safeParse({
      input: "pr",
      worktree: false,
    });
    expect(result.success).toBe(false);
  });

  it("accepts input pr with worktree", () => {
    const result = agentConfigSchema.safeParse({
      input: "pr",
      worktree: true,
    });
    expect(result.success).toBe(true);
  });

  it("accepts input task without worktree", () => {
    const result = agentConfigSchema.safeParse({
      input: "task",
    });
    expect(result.success).toBe(true);
  });

  it("rejects invalid input value", () => {
    const result = agentConfigSchema.safeParse({
      input: "invalid",
    });
    expect(result.success).toBe(false);
  });

  it("accepts output task", () => {
    const result = agentConfigSchema.safeParse({
      output: "task",
    });
    expect(result.success).toBe(true);
  });

  it("rejects invalid output value", () => {
    const result = agentConfigSchema.safeParse({
      output: "invalid",
    });
    expect(result.success).toBe(false);
  });

  it("accepts output task with worktree", () => {
    const result = agentConfigSchema.safeParse({
      output: "task",
      worktree: true,
    });
    expect(result.success).toBe(true);
  });

  it("accepts prTriggerLabel with input pr", () => {
    const result = agentConfigSchema.safeParse({
      input: "pr",
      prTriggerLabel: "vteam:changes-requested",
      worktree: true,
    });
    expect(result.success).toBe(true);
  });

  it("accepts valid cron pattern", () => {
    const result = agentConfigSchema.safeParse({
      cron: "0 */6 * * *",
    });
    expect(result.success).toBe(true);
  });

  it("rejects invalid cron pattern", () => {
    const result = agentConfigSchema.safeParse({
      cron: "not a cron",
    });
    expect(result.success).toBe(false);
  });

  it("accepts allowedTools", () => {
    const result = agentConfigSchema.safeParse({
      allowedTools: ["Read", "Glob", "Grep", "Bash(git *)"],
    });
    expect(result.success).toBe(true);
  });

  it("accepts disallowedTools", () => {
    const result = agentConfigSchema.safeParse({
      disallowedTools: ["Write", "Bash(rm *)"],
    });
    expect(result.success).toBe(true);
  });

  it("accepts both allowedTools and disallowedTools", () => {
    const result = agentConfigSchema.safeParse({
      allowedTools: ["Read", "Edit", "Bash(git *)"],
      disallowedTools: ["Bash(rm *)"],
    });
    expect(result.success).toBe(true);
  });

  it("accepts readOnly with worktree", () => {
    const result = agentConfigSchema.safeParse({
      readOnly: true,
      worktree: true,
    });
    expect(result.success).toBe(true);
  });

  it("rejects readOnly without worktree", () => {
    const result = agentConfigSchema.safeParse({
      readOnly: true,
    });
    expect(result.success).toBe(false);
  });

  it("rejects readOnly with worktree false", () => {
    const result = agentConfigSchema.safeParse({
      readOnly: true,
      worktree: false,
    });
    expect(result.success).toBe(false);
  });

  it("rejects readOnly with autoPR", () => {
    const result = agentConfigSchema.safeParse({
      readOnly: true,
      worktree: true,
      autoPR: true,
    });
    expect(result.success).toBe(false);
  });

  it("accepts readOnly with input pr", () => {
    const result = agentConfigSchema.safeParse({
      readOnly: true,
      worktree: true,
      input: "pr",
    });
    expect(result.success).toBe(true);
  });

  it("accepts readOnly with input task", () => {
    const result = agentConfigSchema.safeParse({
      readOnly: true,
      worktree: true,
      input: "task",
    });
    expect(result.success).toBe(true);
  });
});

describe("subagentConfigSchema", () => {
  it("accepts empty config", () => {
    const result = subagentConfigSchema.safeParse({});
    expect(result.success).toBe(true);
  });

  it("accepts model and tool permissions", () => {
    const result = subagentConfigSchema.safeParse({
      model: "haiku",
      allowedTools: ["Bash(curl *)"],
      disallowedTools: ["Write"],
    });
    expect(result.success).toBe(true);
  });

  it("accepts only model", () => {
    const result = subagentConfigSchema.safeParse({
      model: "sonnet",
    });
    expect(result.success).toBe(true);
  });
});
