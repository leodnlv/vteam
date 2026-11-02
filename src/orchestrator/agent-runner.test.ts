import { describe, it, expect } from "vitest";
import { describeTool, toolErrorText } from "./agent-runner.js";

describe("describeTool", () => {
  const cwd = "/repo";

  it("shows the command for Bash", () => {
    expect(describeTool("Bash", { command: "ls -la" }, cwd)).toBe("ls -la");
  });

  it("shows file paths relative to the agent cwd", () => {
    expect(describeTool("Read", { file_path: "/repo/src/a.ts" }, cwd)).toBe("src/a.ts");
    expect(describeTool("Edit", { file_path: "/elsewhere/b.ts" }, cwd)).toBe("/elsewhere/b.ts");
  });

  it("shows the pattern and search path for Grep", () => {
    expect(describeTool("Grep", { pattern: "TODO", path: "/repo/src" }, cwd)).toBe("TODO  in src");
    expect(describeTool("Glob", { pattern: "**/*.ts" }, cwd)).toBe("**/*.ts");
  });

  it("falls back to url for unknown tools without description or path", () => {
    expect(describeTool("mcp__fetch", { url: "https://x.dev" }, cwd)).toBe("https://x.dev");
  });
});

describe("toolErrorText", () => {
  it("returns undefined for successful results", () => {
    expect(toolErrorText({ type: "tool_result", content: "fine" })).toBeUndefined();
  });

  it("extracts text from string and array error content", () => {
    expect(toolErrorText({ type: "tool_result", is_error: true, content: "File does not exist." }))
      .toBe("File does not exist.");
    expect(toolErrorText({ type: "tool_result", is_error: true, content: [{ type: "text", text: "boom" }] }))
      .toBe("boom");
  });
});
