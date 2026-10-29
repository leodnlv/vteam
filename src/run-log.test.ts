import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import { formatLogFilename, startRunLog } from "./run-log.js";

describe("formatLogFilename", () => {
  it("uses ISO 8601 with colons/dots replaced by dashes", () => {
    const date = new Date("2026-04-21T08:40:00.000Z");
    expect(formatLogFilename(date)).toBe("2026-04-21T08-40-00-000Z.log");
  });
});

describe("startRunLog", () => {
  let dir: string;

  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), "vteam-run-log-"));
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("writes a per-run file under vteam/.logs/<agent>/", () => {
    const date = new Date("2026-04-21T08:40:00.000Z");
    const log = startRunLog(dir, "coder", date);
    log.close();

    expect(log.path).toBe(resolve(dir, "vteam", ".logs", "coder", "2026-04-21T08-40-00-000Z.log"));
  });

  it("tees stdout and stderr into the file until closed", () => {
    const originalStdout = process.stdout.write;
    const originalStderr = process.stderr.write;
    const log = startRunLog(dir, "coder");

    process.stdout.write("out line\n");
    process.stderr.write(Buffer.from("err line\n"));
    log.write("direct line\n");
    log.close();

    expect(readFileSync(log.path, "utf-8")).toBe("out line\nerr line\ndirect line\n");
    expect(process.stdout.write).toBe(originalStdout);
    expect(process.stderr.write).toBe(originalStderr);
  });
});
