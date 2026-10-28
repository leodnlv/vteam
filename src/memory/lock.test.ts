import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { mkdtempSync, mkdirSync, writeFileSync, existsSync, rmSync } from "node:fs";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import { acquireLock, breakLock } from "./lock.js";

vi.mock("node:fs", async (importOriginal) => {
  const actual = await importOriginal();
  return { ...actual, rmSync: vi.fn(actual.rmSync) };
});

let tmp: string;

beforeEach(() => {
  tmp = mkdtempSync(join(tmpdir(), "vteam-lock-test-"));
});

afterEach(() => {
  rmSync(tmp, { recursive: true, force: true });
});

describe("acquireLock", () => {
  it("acquires a lock and creates the lock directory", async () => {
    const resource = resolve(tmp, "my-agent");
    const lock = await acquireLock(resource, "test-agent");
    expect(existsSync(resource + ".lock")).toBe(true);
    lock.release();
  });

  it("releases the lock by removing the directory", async () => {
    const resource = resolve(tmp, "my-agent");
    const lock = await acquireLock(resource, "test-agent");
    lock.release();
    expect(existsSync(resource + ".lock")).toBe(false);
  });

  it("writes lock info with PID and agent name", async () => {
    const resource = resolve(tmp, "my-agent");
    const lock = await acquireLock(resource, "improver");

    const info = JSON.parse(
      require("node:fs").readFileSync(resolve(resource + ".lock", "info.json"), "utf-8"),
    );
    expect(info.pid).toBe(process.pid);
    expect(info.agent).toBe("improver");
    expect(typeof info.timestamp).toBe("number");

    lock.release();
  });

  it("times out when lock is held by a live process", async () => {
    const resource = resolve(tmp, "my-agent");
    const lock = await acquireLock(resource, "holder");

    await expect(
      acquireLock(resource, "waiter", 1000),
    ).rejects.toThrow("Timeout");

    lock.release();
  });

  it("breaks stale lock from dead process and acquires", async () => {
    const resource = resolve(tmp, "my-agent");
    const lockDir = resource + ".lock";
    mkdirSync(lockDir);
    writeFileSync(
      resolve(lockDir, "info.json"),
      JSON.stringify({ pid: 999999, timestamp: Date.now(), agent: "dead" }),
    );

    const lock = await acquireLock(resource, "new-agent", 5000);
    expect(existsSync(lockDir)).toBe(true);
    lock.release();
  });

  it("calls rmSync with force: true when removing a stale lock", async () => {
    const resource = resolve(tmp, "my-agent");
    const lockDir = resource + ".lock";
    mkdirSync(lockDir);
    writeFileSync(
      resolve(lockDir, "info.json"),
      JSON.stringify({ pid: 999999, timestamp: Date.now(), agent: "dead" }),
    );

    vi.mocked(rmSync).mockClear();

    const lock = await acquireLock(resource, "new-agent", 5000);

    const staleRemovalCall = vi.mocked(rmSync).mock.calls.find(
      ([path, opts]) => path === lockDir && (opts as { force?: boolean })?.force === true,
    );
    expect(staleRemovalCall).toBeDefined();

    lock.release();
  });

  it("breaks stale lock older than 30 minutes", async () => {
    const resource = resolve(tmp, "my-agent");
    const lockDir = resource + ".lock";
    mkdirSync(lockDir);
    writeFileSync(
      resolve(lockDir, "info.json"),
      JSON.stringify({
        pid: process.pid,
        timestamp: Date.now() - 31 * 60 * 1000,
        agent: "old",
      }),
    );

    const lock = await acquireLock(resource, "new-agent", 5000);
    expect(existsSync(lockDir)).toBe(true);
    lock.release();
  });
});

describe("breakLock", () => {
  it("returns true and removes existing lock", () => {
    const resource = resolve(tmp, "my-agent");
    mkdirSync(resource + ".lock");
    expect(breakLock(resource)).toBe(true);
    expect(existsSync(resource + ".lock")).toBe(false);
  });

  it("returns false when no lock exists", () => {
    const resource = resolve(tmp, "no-lock");
    expect(breakLock(resource)).toBe(false);
  });
});
