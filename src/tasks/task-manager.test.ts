import { describe, it, expect } from "vitest";
import { matchesTaskFilter, pickNextTask } from "./task-manager.js";
import type { Task } from "../types.js";

function task(metadata: Record<string, string>): Task {
  return {
    ref: "APP-0001",
    title: "T",
    status: "todo",
    severity: "low",
    files: [],
    retryCount: 0,
    metadata,
    description: "",
    notes: [],
    createdAt: "",
  };
}

describe("matchesTaskFilter", () => {
  it("matches an exact value", () => {
    expect(matchesTaskFilter(task({ "found-by": "improver" }), { "found-by": "improver" })).toBe(true);
    expect(matchesTaskFilter(task({ "found-by": "human" }), { "found-by": "improver" })).toBe(false);
  });

  it("ORs values within a list", () => {
    const filter = { "found-by": ["improver", "security-analyst"] };
    expect(matchesTaskFilter(task({ "found-by": "security-analyst" }), filter)).toBe(true);
    expect(matchesTaskFilter(task({}), filter)).toBe(false);
  });

  it("ANDs different keys", () => {
    const filter = { "found-by": "improver", actor: "coder" };
    expect(matchesTaskFilter(task({ "found-by": "improver", actor: "coder" }), filter)).toBe(true);
    expect(matchesTaskFilter(task({ "found-by": "improver" }), filter)).toBe(false);
  });

  it("treats null as a missing or empty key", () => {
    expect(matchesTaskFilter(task({}), { actor: null })).toBe(true);
    expect(matchesTaskFilter(task({ actor: "" }), { actor: null })).toBe(true);
    expect(matchesTaskFilter(task({ actor: "x" }), { actor: [null, "y"] })).toBe(false);
  });

  it("matches everything with an empty filter", () => {
    expect(matchesTaskFilter(task({ any: "thing" }), {})).toBe(true);
  });
});

describe("pickNextTask", () => {
  const make = (ref: string, overrides: Partial<Task>): Task => ({ ...task({}), ref, ...overrides });

  it("picks the highest severity, then the oldest", () => {
    const ready = [
      make("LOW", { severity: "low", createdAt: "2026-01-01" }),
      make("NEW-HIGH", { severity: "high", createdAt: "2026-03-01" }),
      make("OLD-HIGH", { severity: "high", createdAt: "2026-02-01" }),
    ];
    expect(pickNextTask(ready, {}, 3)?.ref).toBe("OLD-HIGH");
  });

  it("skips tasks outside the filter or at max retries", () => {
    const ready = [
      make("OTHER", { severity: "critical", metadata: { "found-by": "human" } }),
      make("SPENT", { severity: "critical", retryCount: 3, metadata: { "found-by": "improver" } }),
      make("OK", { severity: "low", metadata: { "found-by": "improver" } }),
    ];
    expect(pickNextTask(ready, { "found-by": "improver" }, 3)?.ref).toBe("OK");
  });

  it("returns undefined when nothing qualifies", () => {
    expect(pickNextTask([], {}, 3)).toBeUndefined();
  });
});
