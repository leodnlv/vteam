import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdirSync, writeFileSync, rmSync } from "node:fs";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import { findCronAgents, formatLogFilename, buildChildArgs } from "./loop.js";
import { isValidCronExpression } from "../config/schema.js";

describe("findCronAgents", () => {
  let dir: string;

  beforeEach(() => {
    dir = join(tmpdir(), `vteam-test-${Date.now()}-${Math.random().toString(36).slice(2)}`);
    mkdirSync(resolve(dir, "vteam", "agents"), { recursive: true });
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  function addAgent(name: string, frontmatter: string): void {
    const agentDir = resolve(dir, "vteam", "agents", name);
    mkdirSync(agentDir, { recursive: true });
    writeFileSync(
      resolve(agentDir, "AGENT.md"),
      `---\n${frontmatter}\n---\nAgent prompt.`,
    );
  }

  it("returns agents with cron patterns", () => {
    addAgent("reviewer", 'cron: "0 */6 * * *"');
    addAgent("fixer", "worktree: true");

    const agents = findCronAgents(dir);
    expect(agents).toHaveLength(1);
    expect(agents[0].name).toBe("reviewer");
    expect(agents[0].cron).toBe("0 */6 * * *");
  });

  it("returns empty array when no agents have cron", () => {
    addAgent("fixer", "worktree: true");
    expect(findCronAgents(dir)).toHaveLength(0);
  });

  it("returns empty array when no agents exist", () => {
    expect(findCronAgents(dir)).toHaveLength(0);
  });

  it("skips agents with invalid frontmatter", () => {
    addAgent("good", 'cron: "0 */6 * * *"');
    addAgent("bad", 'cron: "not a cron"');

    const agents = findCronAgents(dir);
    expect(agents).toHaveLength(1);
    expect(agents[0].name).toBe("good");
  });
});

describe("isValidCronExpression", () => {
  it.each([
    "* * * * *",
    "0 */6 * * *",
    "30 2 * * 1",
    "0 0 1 * *",
    "*/15 * * * *",
    "0 9-17 * * 1-5",
    "0 0 1,15 * *",
  ])("accepts valid expression: %s", (expr) => {
    expect(isValidCronExpression(expr)).toBe(true);
  });

  it.each(["", "* * *", "* * * * * *", "invalid", "a b c d e"])(
    "rejects invalid expression: %s",
    (expr) => {
      expect(isValidCronExpression(expr)).toBe(false);
    },
  );
});

describe("buildChildArgs", () => {
  it("spawns node with the script path and agent name", () => {
    const result = buildChildArgs(
      "/usr/local/bin/node",
      [],
      "/project/dist/bin.js",
      "improver",
    );
    expect(result.command).toBe("/usr/local/bin/node");
    expect(result.args).toEqual(["/project/dist/bin.js", "run", "improver"]);
  });

  it("forwards execArgv so node loader flags reach the child process", () => {
    const result = buildChildArgs(
      "/usr/local/bin/node",
      ["--import", "tsx/esm/register"],
      "src/bin.ts",
      "improver",
    );
    expect(result.args).toEqual([
      "--import",
      "tsx/esm/register",
      "src/bin.ts",
      "run",
      "improver",
    ]);
  });
});

describe("formatLogFilename", () => {
  it("uses ISO 8601 with colons/dots replaced by dashes", () => {
    const date = new Date("2026-04-21T08:40:00.000Z");
    expect(formatLogFilename(date)).toBe("2026-04-21T08-40-00-000Z.log");
  });
});
