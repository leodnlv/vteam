import { describe, it, expect, beforeEach, afterEach, vi } from "vitest";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import { formatLogFilename, startRunLog, writeSplit } from "./run-log.js";

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

describe("run log formatting", () => {
  let dir: string;

  beforeEach(() => {
    dir = mkdtempSync(join(tmpdir(), "vteam-run-log-"));
  });

  afterEach(() => {
    rmSync(dir, { recursive: true, force: true });
  });

  it("strips ANSI colors from the file", () => {
    const log = startRunLog(dir, "coder");
    const writeSpy = vi.spyOn(process.stdout, "write");
    process.stdout.write("\x1b[32m✓ ok\x1b[39m\n");
    log.close();
    writeSpy.mockRestore();

    expect(readFileSync(log.path, "utf-8")).toBe("✓ ok\n");
  });

  it("writeSplit sends the short line to the terminal and the full line to the file", () => {
    const log = startRunLog(dir, "coder");
    const consoleSpy = vi.spyOn(console, "log").mockImplementation(() => {});
    writeSplit("short…", "short and complete");
    log.close();

    expect(consoleSpy).toHaveBeenCalledWith("short…");
    consoleSpy.mockRestore();
    expect(readFileSync(log.path, "utf-8")).toBe("short and complete\n");
  });
});
