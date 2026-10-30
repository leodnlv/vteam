import { z } from "zod";
import type { AgentOutput } from "../types.js";

const severitySchema = z.enum(["critical", "high", "medium", "low"]);

const taskContentBodySchema = z.object({
  title: z.string().min(1),
  severity: severitySchema,
  description: z.string().min(1),
  suggestedFix: z.string().optional(),
  files: z.array(z.string()).min(1),
});

const taskContentSchema = z.object({
  type: z.literal("task"),
  body: taskContentBodySchema,
});

const genericContentSchema = z.object({
  type: z.literal("generic"),
  body: z.string(),
});

const agentContentSchema = z.discriminatedUnion("type", [
  taskContentSchema,
  genericContentSchema,
]);

const commitMessageSchema = z.object({
  subject: z.string().min(1),
  body: z.string(),
});

export const agentOutputSchema = z.object({
  status: z.enum(["completed", "partial", "blocked", "failed"]),
  summary: z.string(),
  content: agentContentSchema.optional(),
  filesChanged: z.array(z.string()).optional(),
  commitMessage: commitMessageSchema.optional(),
  blockerReason: z.string().optional(),
  memoryUpdate: z.string().optional(),
  threadReplies: z
    .array(z.object({ thread: z.string().min(1), body: z.string().min(1) }))
    .optional(),
});

function stripMarkdownFences(text: string): string {
  const fenced = text.match(/^\s*```(?:json)?\s*\n([\s\S]*?)\n\s*```\s*$/);
  if (fenced) return fenced[1].trim();
  return text.trim();
}

function findJsonObject(text: string): string | null {
  let braceStart = -1;
  while ((braceStart = text.indexOf("{", braceStart + 1)) !== -1) {
    const candidate = text.slice(braceStart);
    const lastBrace = candidate.lastIndexOf("}");
    if (lastBrace === -1) continue;
    const slice = candidate.slice(0, lastBrace + 1);
    try {
      JSON.parse(slice);
      return slice;
    } catch {
      // not valid JSON from this '{', try the next one
    }
  }
  return null;
}

function extractJson(text: string): string {
  const stripped = stripMarkdownFences(text);
  return findJsonObject(stripped) ?? findJsonObject(text) ?? stripped;
}

export function parseAgentOutput(resultText: string): AgentOutput {
  const json = JSON.parse(extractJson(resultText));
  return agentOutputSchema.parse(json);
}
