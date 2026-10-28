import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, mkdirSync, writeFileSync } from "node:fs";
import { rmSync } from "node:fs";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import { loadConfig } from "./load.js";

let tmp: string;

beforeEach(() => {
  tmp = mkdtempSync(join(tmpdir(), "vteam-config-test-"));
});

afterEach(() => {
  rmSync(tmp, { recursive: true, force: true });
});

function writeConfig(data: unknown): void {
  const dir = resolve(tmp, "vteam");
  mkdirSync(dir, { recursive: true });
  writeFileSync(
    resolve(dir, "vteam.config.json"),
    JSON.stringify(data, null, 2),
  );
}

describe("loadConfig", () => {
  it("throws when vteam.config.json does not exist", () => {
    expect(() => loadConfig(tmp)).toThrow("vteam.config.json not found");
  });

  it("parses a valid config and returns typed data", () => {
    writeConfig({
      baseBranch: "main",
      platform: "github",
      worktreeDir: ".vteam-worktrees",
      tasks: { maxRetries: 3, project: "app" },
    });

    const config = loadConfig(tmp);
    expect(config.baseBranch).toBe("main");
    expect(config.platform).toBe("github");
    expect(config.tasks.maxRetries).toBe(3);
  });

  it("throws with field path on missing required field", () => {
    writeConfig({
      platform: "github",
      worktreeDir: ".vteam-worktrees",
      tasks: { maxRetries: 3, project: "app" },
    });

    expect(() => loadConfig(tmp)).toThrow("Invalid vteam.config.json");
    expect(() => loadConfig(tmp)).toThrow("baseBranch");
  });

  it("throws with field path on invalid platform", () => {
    writeConfig({
      baseBranch: "main",
      platform: "bitbucket",
      worktreeDir: ".vteam-worktrees",
      tasks: { maxRetries: 3, project: "app" },
    });

    expect(() => loadConfig(tmp)).toThrow("platform");
  });

  it("throws on invalid JSON", () => {
    const dir = resolve(tmp, "vteam");
    mkdirSync(dir, { recursive: true });
    writeFileSync(resolve(dir, "vteam.config.json"), "not json{{{");

    expect(() => loadConfig(tmp)).toThrow();
  });

  it("reports multiple errors at once", () => {
    writeConfig({
      worktreeDir: ".vteam-worktrees",
      tasks: { maxRetries: 3, project: "app" },
    });

    try {
      loadConfig(tmp);
      expect.unreachable("should have thrown");
    } catch (err) {
      const msg = (err as Error).message;
      expect(msg).toContain("baseBranch");
      expect(msg).toContain("platform");
    }
  });
});
