import { describe, it, expect } from "vitest";
import { parse, stringify } from "./frontmatter.js";

describe("parse", () => {
  it("extracts data and content from valid frontmatter", () => {
    const result = parse("---\ntitle: Hello\n---\nBody text.\n");
    expect(result.data).toEqual({ title: "Hello" });
    expect(result.content).toBe("Body text.\n");
  });

  it("returns empty data when no frontmatter present", () => {
    const result = parse("Just plain markdown.");
    expect(result.data).toEqual({});
    expect(result.content).toBe("Just plain markdown.");
  });

  it("returns empty data when closing delimiter is missing", () => {
    const result = parse("---\ntitle: Oops\nNo closing.");
    expect(result.data).toEqual({});
    expect(result.content).toBe("---\ntitle: Oops\nNo closing.");
  });

  it("strips BOM before parsing", () => {
    const result = parse("\uFEFF---\nkey: val\n---\nbody");
    expect(result.data).toEqual({ key: "val" });
  });

  it("parses string values", () => {
    const { data } = parse("---\nname: some-agent\n---\n");
    expect(data.name).toBe("some-agent");
  });

  it("parses boolean true", () => {
    const { data } = parse("---\nworktree: true\n---\n");
    expect(data.worktree).toBe(true);
  });

  it("parses boolean false", () => {
    const { data } = parse("---\nautoPR: false\n---\n");
    expect(data.autoPR).toBe(false);
  });

  it("parses integers", () => {
    const { data } = parse("---\nretry-count: 3\n---\n");
    expect(data["retry-count"]).toBe(3);
  });

  it("parses negative integers", () => {
    const { data } = parse("---\noffset: -5\n---\n");
    expect(data.offset).toBe(-5);
  });

  it("parses floats", () => {
    const { data } = parse("---\nweight: 1.5\n---\n");
    expect(data.weight).toBe(1.5);
  });

  it("parses null variants", () => {
    const { data } = parse("---\na: null\nb: ~\nc:\n---\n");
    expect(data.a).toBeNull();
    expect(data.b).toBeNull();
    expect(data.c).toBeNull();
  });

  it("parses flow arrays of strings", () => {
    const { data } = parse("---\nfiles: [src/a.ts, src/b.ts]\n---\n");
    expect(data.files).toEqual(["src/a.ts", "src/b.ts"]);
  });

  it("parses empty arrays", () => {
    const { data } = parse("---\nfiles: []\n---\n");
    expect(data.files).toEqual([]);
  });

  it("parses arrays with mixed types", () => {
    const { data } = parse("---\nmix: [hello, 42, true]\n---\n");
    expect(data.mix).toEqual(["hello", 42, true]);
  });

  it("parses double-quoted strings", () => {
    const { data } = parse('---\nname: "hello world"\n---\n');
    expect(data.name).toBe("hello world");
  });

  it("parses single-quoted strings", () => {
    const { data } = parse("---\nname: 'hello world'\n---\n");
    expect(data.name).toBe("hello world");
  });

  it("skips comment lines", () => {
    const { data } = parse("---\n# this is a comment\ntitle: kept\n---\n");
    expect(data).toEqual({ title: "kept" });
  });

  it("skips blank lines in yaml block", () => {
    const { data } = parse("---\na: 1\n\nb: 2\n---\n");
    expect(data).toEqual({ a: 1, b: 2 });
  });

  it("handles values containing colons", () => {
    const { data } = parse("---\nlabel: vteam:changes-requested\n---\n");
    expect(data.label).toBe("vteam:changes-requested");
  });

  it("handles ISO date strings as strings", () => {
    const { data } = parse("---\ncreated: 2026-04-19T10:00:00Z\n---\n");
    expect(data.created).toBe("2026-04-19T10:00:00Z");
    expect(typeof data.created).toBe("string");
  });

  it("preserves multiline body content", () => {
    const { content } = parse("---\ntitle: X\n---\nLine 1\n\nLine 2\n");
    expect(content).toBe("Line 1\n\nLine 2\n");
  });

  it("handles arrays with file:line references", () => {
    const { data } = parse("---\nfiles: [src/auth.ts:45, src/db.ts:100]\n---\n");
    expect(data.files).toEqual(["src/auth.ts:45", "src/db.ts:100"]);
  });

  it("parses block-style arrays", () => {
    const { data } = parse(
      "---\nallowedTools:\n  - Read\n  - Glob\nprCreateLabels:\n  - vteam\n  - automated\n---\n"
    );
    expect(data.allowedTools).toEqual(["Read", "Glob"]);
    expect(data.prCreateLabels).toEqual(["vteam", "automated"]);
  });

  it("parses single-item block arrays", () => {
    const { data } = parse("---\npaths:\n  - src/\n---\n");
    expect(data.paths).toEqual(["src/"]);
  });

  it("treats key with no value and no list items as null", () => {
    const { data } = parse("---\nempty:\nnext: value\n---\n");
    expect(data.empty).toBeNull();
    expect(data.next).toBe("value");
  });
});

describe("stringify", () => {
  it("wraps data in --- delimiters with body", () => {
    const result = stringify("Body.", { title: "Hello" });
    expect(result).toBe("---\ntitle: Hello\n---\nBody.\n");
  });

  it("serializes strings", () => {
    const result = stringify("", { name: "agent" });
    expect(result).toContain("name: agent");
  });

  it("serializes booleans", () => {
    const result = stringify("", { enabled: true, disabled: false });
    expect(result).toContain("enabled: true");
    expect(result).toContain("disabled: false");
  });

  it("serializes numbers", () => {
    const result = stringify("", { count: 42 });
    expect(result).toContain("count: 42");
  });

  it("serializes null", () => {
    const result = stringify("", { empty: null });
    expect(result).toContain("empty: null");
  });

  it("serializes arrays", () => {
    const result = stringify("", { files: ["a.ts", "b.ts"] });
    expect(result).toContain("files: [a.ts, b.ts]");
  });

  it("serializes empty arrays", () => {
    const result = stringify("", { files: [] });
    expect(result).toContain("files: []");
  });

  it("omits undefined values", () => {
    const result = stringify("", { a: "keep", b: undefined });
    expect(result).toContain("a: keep");
    expect(result).not.toContain("b:");
  });

  it("quotes strings that look like booleans", () => {
    const result = stringify("", { val: "true" });
    expect(result).toContain('val: "true"');
  });

  it("quotes strings that look like numbers", () => {
    const result = stringify("", { val: "42" });
    expect(result).toContain('val: "42"');
  });

  it("quotes strings that look like null", () => {
    const result = stringify("", { val: "null" });
    expect(result).toContain('val: "null"');
  });

  it("quotes empty strings", () => {
    const result = stringify("", { val: "" });
    expect(result).toContain('val: ""');
  });

  it("quotes strings containing special characters", () => {
    const result = stringify("", { url: "https://example.com" });
    expect(result).toContain('url: "https://example.com"');
  });

  it("escapes double quotes inside quoted strings", () => {
    const result = stringify("", { msg: 'say "hello"' });
    expect(result).toContain('msg: "say \\"hello\\""');
  });

  it("normalizes body to end with newline", () => {
    expect(stringify("no newline", {})).toMatch(/no newline\n$/);
    expect(stringify("has newline\n", {})).toMatch(/has newline\n$/);
    expect(stringify("has newline\n", {})).not.toMatch(/has newline\n\n$/);
  });
});

describe("roundtrip", () => {
  it("parse(stringify(...)) preserves simple data", () => {
    const data = { title: "Test", severity: "high", count: 3, enabled: true };
    const body = "## Description\n\nSome text.";
    const { data: parsed, content } = parse(stringify(body, data));
    expect(parsed).toEqual(data);
    expect(content.trim()).toBe(body);
  });

  it("roundtrips arrays", () => {
    const data = { files: ["src/a.ts:10", "src/b.ts:20"] };
    const { data: parsed } = parse(stringify("body", data));
    expect(parsed.files).toEqual(data.files);
  });

  it("roundtrips empty arrays", () => {
    const data = { items: [] };
    const { data: parsed } = parse(stringify("body", data));
    expect(parsed.items).toEqual([]);
  });

  it("roundtrips booleans and numbers", () => {
    const data = { active: false, retries: 0 };
    const { data: parsed } = parse(stringify("body", data));
    expect(parsed.active).toBe(false);
    expect(parsed.retries).toBe(0);
  });

  it("roundtrips null", () => {
    const data = { ref: null };
    const { data: parsed } = parse(stringify("body", data));
    expect(parsed.ref).toBeNull();
  });

  it("roundtrips a realistic task frontmatter", () => {
    const data = {
      title: "Missing null check in auth",
      created: "2026-04-19T10:00:00Z",
      status: "todo",
      severity: "high",
      "found-by": "improver",
      files: ["src/auth.ts:45", "src/middleware.ts:12"],
      "retry-count": 0,
    };
    const body = "## Description\n\nThe auth module can crash.";
    const { data: parsed, content } = parse(stringify(body, data));
    expect(parsed).toEqual(data);
    expect(content.trim()).toBe(body);
  });

  it("roundtrips a realistic agent frontmatter", () => {
    const data = {
      model: "sonnet",
      worktree: true,
      input: "task",
      autoPR: true,
      prCreateLabels: ["vteam", "automated"],
    };
    const body = "# Code Reviewer\n\nYou review code.";
    const { data: parsed, content } = parse(stringify(body, data));
    expect(parsed).toEqual(data);
    expect(content.trim()).toBe(body);
  });
});
