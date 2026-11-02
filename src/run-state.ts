import { existsSync, mkdirSync, readdirSync, readFileSync, writeFileSync } from "node:fs";
import { resolve } from "node:path";
import { readLiveLock, type LockInfo } from "./memory/lock.js";
import type { RunState } from "./types.js";

const TRANSIENT_LOCKS = new Set(["task-claim"]);

export interface LiveRun {
  name: string;
  lock: LockInfo;
  state?: RunState;
}

function runsDir(cwd: string): string {
  return resolve(cwd, "vteam", ".runs");
}

export function writeRunState(cwd: string, state: RunState): void {
  const dir = runsDir(cwd);
  mkdirSync(dir, { recursive: true });
  writeFileSync(resolve(dir, `${state.runId}.json`), JSON.stringify(state, null, 2));
}

export function findLiveRuns(cwd: string): LiveRun[] {
  const locksDir = resolve(cwd, "vteam", ".locks");
  if (!existsSync(locksDir)) return [];
  return readdirSync(locksDir)
    .filter((f) => f.endsWith(".lock"))
    .map((f) => f.slice(0, -".lock".length))
    .filter((name) => !TRANSIENT_LOCKS.has(name))
    .flatMap((name) => {
      const lock = readLiveLock(resolve(locksDir, `${name}.lock`));
      return lock ? [{ name, lock, state: findLatestRunState(cwd, name, lock.timestamp) }] : [];
    });
}

export function liveWorktreePaths(runs: LiveRun[]): string[] {
  return runs.flatMap((r) => (r.state?.worktreePath ? [r.state.worktreePath] : []));
}

export function findLatestRunState(cwd: string, agent: string, startedBefore: number): RunState | undefined {
  const dir = runsDir(cwd);
  if (!existsSync(dir)) return undefined;
  return readdirSync(dir)
    .filter((f) => f.endsWith(`-${agent}.json`))
    .sort()
    .reverse()
    .map((f) => readRunState(resolve(dir, f)))
    .find((s): s is RunState => s?.agent === agent && Date.parse(s.startedAt) <= startedBefore);
}

function readRunState(path: string): RunState | undefined {
  try {
    return JSON.parse(readFileSync(path, "utf-8")) as RunState;
  } catch {
    return undefined;
  }
}
