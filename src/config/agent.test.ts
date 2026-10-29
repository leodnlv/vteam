import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import { resolveAgentConfig } from "./agent.js";
import { agentConfigJsonSchema } from "./schema.js";

let tmp: string;
let agentDir: string;

beforeEach(() => {
  tmp = mkdtempSync(join(tmpdir(), "vteam-agent-test-"));
  agentDir = resolve(tmp, "vteam", "agents", "bot");
  mkdirSync(agentDir, { recursive: true });
  writeFileSync(resolve(agentDir, "AGENT.md"), "You are a bot.");
});

afterEach(() => {
  rmSync(tmp, { recursive: true, force: true });
});

function writeAgentJson(config: unknown): void {
  writeFileSync(resolve(agentDir, "agent.json"), JSON.stringify(config));
}

describe("resolveAgentConfig", () => {
  it("uses defaults when agent.json is missing", () => {
    expect(resolveAgentConfig("bot", tmp)).toEqual({
      name: "bot",
      agentMdPath: resolve(agentDir, "AGENT.md"),
    });
  });

  it("reads agent settings from agent.json and drops $schema", () => {
    writeAgentJson({ $schema: "../../agent.schema.json", model: "sonnet", worktree: true, input: "task" });
    const agent = resolveAgentConfig("bot", tmp);
    expect(agent).toMatchObject({ model: "sonnet", worktree: true, input: "task" });
    expect(agent).not.toHaveProperty("$schema");
  });

  it("attaches memory and onFinish config to their markdown files", () => {
    writeAgentJson({ memory: { model: "haiku" }, onFinish: { allowedTools: ["Bash(curl *)"] } });
    writeFileSync(resolve(agentDir, "MEMORY.md"), "Curate.");
    writeFileSync(resolve(agentDir, "ON_FINISH.md"), "Notify.");
    const agent = resolveAgentConfig("bot", tmp);
    expect(agent.memory).toEqual({ memoryMdPath: resolve(agentDir, "MEMORY.md"), model: "haiku" });
    expect(agent.onFinish).toEqual({
      onFinishMdPath: resolve(agentDir, "ON_FINISH.md"),
      allowedTools: ["Bash(curl *)"],
    });
  });

  it("enables memory and onFinish from markdown files alone", () => {
    writeFileSync(resolve(agentDir, "MEMORY.md"), "Curate.");
    writeFileSync(resolve(agentDir, "ON_FINISH.md"), "Notify.");
    const agent = resolveAgentConfig("bot", tmp);
    expect(agent.memory).toEqual({ memoryMdPath: resolve(agentDir, "MEMORY.md") });
    expect(agent.onFinish).toEqual({ onFinishMdPath: resolve(agentDir, "ON_FINISH.md") });
  });

  it("rejects a memory section without MEMORY.md", () => {
    writeAgentJson({ memory: { model: "haiku" } });
    expect(() => resolveAgentConfig("bot", tmp)).toThrow('"memory" section but bot/MEMORY.md does not exist');
  });

  it("rejects an onFinish section without ON_FINISH.md", () => {
    writeAgentJson({ onFinish: { model: "haiku" } });
    expect(() => resolveAgentConfig("bot", tmp)).toThrow('"onFinish" section but bot/ON_FINISH.md does not exist');
  });

  it("rejects malformed JSON", () => {
    writeFileSync(resolve(agentDir, "agent.json"), "{ model: sonnet }");
    expect(() => resolveAgentConfig("bot", tmp)).toThrow("Invalid JSON in bot/agent.json");
  });

  it("rejects unknown keys with their path", () => {
    writeAgentJson({ modle: "sonnet", memory: { modle: "haiku" } });
    writeFileSync(resolve(agentDir, "MEMORY.md"), "Curate.");
    expect(() => resolveAgentConfig("bot", tmp)).toThrow(
      'Invalid config in bot/agent.json:\n  - memory: Unrecognized key: "modle"\n  - (root): Unrecognized key: "modle"',
    );
  });

  it("rejects cross-field rule violations", () => {
    writeAgentJson({ input: "pr" });
    expect(() => resolveAgentConfig("bot", tmp)).toThrow('input: "pr" requires worktree: true');
  });

  it.each(["AGENT.md", "MEMORY.md", "ON_FINISH.md"])("rejects leftover frontmatter in %s", (file) => {
    writeFileSync(resolve(agentDir, file), "---\nmodel: sonnet\n---\n\nPrompt.");
    expect(() => resolveAgentConfig("bot", tmp)).toThrow(`bot/${file} starts with YAML frontmatter`);
  });
});

describe("vteam/agent.schema.json", () => {
  it("matches the schema generated from agentConfigSchema", () => {
    const committed = JSON.parse(readFileSync(resolve(__dirname, "../../vteam/agent.schema.json"), "utf-8"));
    expect(committed).toEqual(agentConfigJsonSchema());
  });
});
