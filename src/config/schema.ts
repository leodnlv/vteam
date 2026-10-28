import { z } from "zod";
import { Cron } from "croner";

export function isValidCronExpression(expr: string): boolean {
  const fields = expr.trim().split(/\s+/);
  if (fields.length !== 5) return false;
  try {
    const job = new Cron(expr);
    job.stop();
    return true;
  } catch {
    return false;
  }
}

const taskFilterValueSchema = z.string().min(1).nullable();

export const agentFrontmatterSchema = z
  .object({
    model: z.string().optional(),
    cron: z
      .string()
      .refine(isValidCronExpression, {
        message:
          "Invalid cron expression (expected 5 fields: minute hour day month weekday)",
      })
      .optional(),
    worktree: z.boolean().optional(),
    readOnly: z.boolean().optional(),
    output: z.enum(["task"]).optional(),
    input: z.enum(["task", "pr"]).optional(),
    taskFilter: z
      .record(
        z.string().min(1),
        z.union([taskFilterValueSchema, z.array(taskFilterValueSchema).min(1)]),
      )
      .optional(),
    prTriggerLabel: z.string().optional(),
    autoPR: z.boolean().optional(),
    prCreateLabels: z.array(z.string()).optional(),
    allowedTools: z.array(z.string()).optional(),
    disallowedTools: z.array(z.string()).optional(),
  })
  .refine((agent) => agent.input !== "pr" || agent.worktree, {
    message: 'input: "pr" requires worktree: true',
  })
  .refine((agent) => !agent.readOnly || agent.worktree, {
    message: "readOnly: true requires worktree: true",
  })
  .refine((agent) => !agent.readOnly || !agent.autoPR, {
    message: "readOnly: true is incompatible with autoPR: true",
  })
  .refine((agent) => agent.output !== "task" || !agent.autoPR, {
    message: 'output: "task" is incompatible with autoPR: true',
  })
  .refine((agent) => agent.output !== "task" || !agent.readOnly, {
    message: 'output: "task" is incompatible with readOnly: true',
  })
  .refine((agent) => !agent.taskFilter || agent.input === "task", {
    message: 'taskFilter requires input: "task"',
  });

export const onFinishFrontmatterSchema = z.object({
  model: z.string().optional(),
  allowedTools: z.array(z.string()).optional(),
  disallowedTools: z.array(z.string()).optional(),
});

export const memoryFrontmatterSchema = z.object({
  model: z.string().optional(),
  allowedTools: z.array(z.string()).optional(),
  disallowedTools: z.array(z.string()).optional(),
});

export const vteamConfigSchema = z.object({
  baseBranch: z.string(),
  platform: z.enum(["github", "gitlab"]),
  worktreeDir: z.string(),
  tasks: z.object({
    maxRetries: z.number().int().nonnegative(),
    project: z.string().min(1),
    label: z.string().min(1).default("vteam"),
    workspace: z.string().min(1).optional(),
  }),
});
