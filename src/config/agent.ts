import { existsSync, readFileSync, readdirSync } from "node:fs";
import { resolve } from "node:path";
import { agentConfigSchema } from "./schema.js";
import type { AgentConfig } from "../types.js";

function assertNoFrontmatter(path: string, label: string): void {
  const raw = readFileSync(path, "utf-8");
  if (raw.replace(/^\uFEFF/, "").startsWith("---")) {
    throw new Error(
      `${label} starts with YAML frontmatter, which is no longer supported. Move its fields to agent.json and leave only the prompt in the markdown file.`,
    );
  }
}

function readAgentJson(path: string, name: string): unknown {
  if (!existsSync(path)) return {};
  try {
    return JSON.parse(readFileSync(path, "utf-8"));
  } catch (err) {
    throw new Error(`Invalid JSON in ${name}/agent.json: ${(err as Error).message}`);
  }
}

export function resolveAgentConfig(
  name: string,
  cwd: string,
): AgentConfig {
  const agentDir = resolve(cwd, "vteam", "agents", name);
  const agentMdPath = resolve(agentDir, "AGENT.md");

  if (!existsSync(agentMdPath)) {
    throw new Error(`Agent "${name}" not found at ${agentMdPath}`);
  }
  assertNoFrontmatter(agentMdPath, `${name}/AGENT.md`);

  const result = agentConfigSchema.safeParse(readAgentJson(resolve(agentDir, "agent.json"), name));
  if (!result.success) {
    const issues = result.error.issues
      .map((i) => `  - ${i.path.length > 0 ? i.path.join(".") : "(root)"}: ${i.message}`)
      .join("\n");
    throw new Error(`Invalid config in ${name}/agent.json:\n${issues}`);
  }
  const { $schema: _schema, memory: memoryConfig, onFinish: onFinishConfig, ...agentConfig } = result.data;

  const onFinishMdPath = resolve(agentDir, "ON_FINISH.md");
  const hasOnFinish = existsSync(onFinishMdPath);
  if (onFinishConfig && !hasOnFinish) {
    throw new Error(`${name}/agent.json has an "onFinish" section but ${name}/ON_FINISH.md does not exist`);
  }
  if (hasOnFinish) assertNoFrontmatter(onFinishMdPath, `${name}/ON_FINISH.md`);

  const memoryMdPath = resolve(agentDir, "MEMORY.md");
  const hasMemory = existsSync(memoryMdPath);
  if (memoryConfig && !hasMemory) {
    throw new Error(`${name}/agent.json has a "memory" section but ${name}/MEMORY.md does not exist`);
  }
  if (hasMemory) assertNoFrontmatter(memoryMdPath, `${name}/MEMORY.md`);

  return {
    name,
    agentMdPath,
    ...agentConfig,
    ...(hasOnFinish ? { onFinish: { onFinishMdPath, ...onFinishConfig } } : {}),
    ...(hasMemory ? { memory: { memoryMdPath, ...memoryConfig } } : {}),
  };
}

export function listAgentNames(cwd: string): string[] {
  const agentsDir = resolve(cwd, "vteam", "agents");
  if (!existsSync(agentsDir)) return [];

  return readdirSync(agentsDir, { withFileTypes: true })
    .filter(
      (e) =>
        e.isDirectory() &&
        existsSync(resolve(agentsDir, e.name, "AGENT.md")),
    )
    .map((e) => e.name);
}

export function listTaskWorkers(cwd: string): AgentConfig[] {
  return listAgentNames(cwd)
    .map((name) => resolveAgentConfig(name, cwd))
    .filter((agent) => agent.input === "task");
}
