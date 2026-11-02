import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { findLatestRunState, writeRunState } from "./run-state.js";

let cwd: string;

beforeEach(() => {
  cwd = mkdtempSync(join(tmpdir(), "vteam-run-state-"));
});

afterEach(() => {
  rmSync(cwd, { recursive: true, force: true });
});

describe("findLatestRunState", () => {
  it("returns undefined without any runs", () => {
    expect(findLatestRunState(cwd, "coder", Date.now())).toBeUndefined();
  });

  it("returns the newest run of the agent started before the given time", () => {
    writeRunState(cwd, { runId: "20260101-100000-coder", agent: "coder", status: "completed", startedAt: "2026-01-01T10:00:00Z" });
    writeRunState(cwd, { runId: "20260101-110000-coder", agent: "coder", status: "claude_running", startedAt: "2026-01-01T11:00:00Z" });
    writeRunState(cwd, { runId: "20260101-120000-coder", agent: "coder", status: "started", startedAt: "2026-01-01T12:00:00Z" });
    writeRunState(cwd, { runId: "20260101-113000-improver", agent: "improver", status: "started", startedAt: "2026-01-01T11:30:00Z" });

    expect(findLatestRunState(cwd, "coder", Date.parse("2026-01-01T11:30:00Z"))?.runId).toBe("20260101-110000-coder");
  });

  it("does not confuse agents whose names end the same way", () => {
    writeRunState(cwd, { runId: "20260101-100000-test-coder", agent: "test-coder", status: "started", startedAt: "2026-01-01T10:00:00Z" });
    expect(findLatestRunState(cwd, "coder", Date.now())).toBeUndefined();
  });
});
