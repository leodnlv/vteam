import { mkdirSync, rmSync, readFileSync, writeFileSync, existsSync, statSync, renameSync } from "node:fs";
import { resolve } from "node:path";

const STALE_MS = 30 * 60 * 1000; // 30 minutes

export interface LockInfo {
  pid: number;
  timestamp: number;
  agent: string;
}

export interface FileLock {
  path: string;
  release: () => void;
}

export function isProcessAlive(pid: number): boolean {
  try {
    process.kill(pid, 0);
    return true;
  } catch {
    return false;
  }
}

export function readLiveLock(lockDir: string): LockInfo | undefined {
  try {
    const info: LockInfo = JSON.parse(readFileSync(resolve(lockDir, "info.json"), "utf-8"));
    return isProcessAlive(info.pid) ? info : undefined;
  } catch {
    return undefined;
  }
}

function isLockStale(lockDir: string): boolean {
  try {
    const infoPath = resolve(lockDir, "info.json");
    if (!existsSync(infoPath)) {
      const stat = statSync(lockDir);
      return Date.now() - stat.mtimeMs > STALE_MS;
    }
    const info: LockInfo = JSON.parse(readFileSync(infoPath, "utf-8"));
    return !isProcessAlive(info.pid);
  } catch {
    return true;
  }
}

function removeStaleLock(lockDir: string): boolean {
  const graveyard = `${lockDir}.stale-${process.pid}-${Date.now()}-${Math.random().toString(36).slice(2)}`;
  try {
    renameSync(lockDir, graveyard);
  } catch {
    return false;
  }
  if (!isLockStale(graveyard)) {
    try {
      renameSync(graveyard, lockDir);
    } catch {
      // Lock was re-created meanwhile; the displaced holder's release is a no-op by pid check
    }
    return false;
  }
  rmSync(graveyard, { recursive: true, force: true });
  return true;
}

function ownsLock(lockDir: string): boolean {
  try {
    const info: LockInfo = JSON.parse(readFileSync(resolve(lockDir, "info.json"), "utf-8"));
    return info.pid === process.pid;
  } catch {
    return false;
  }
}

function sleep(ms: number): Promise<void> {
  return new Promise((r) => setTimeout(r, ms));
}

export async function acquireLock(
  resourcePath: string,
  agent: string,
  timeoutMs = 30_000,
): Promise<FileLock> {
  const lockDir = resourcePath + ".lock";
  const deadline = Date.now() + timeoutMs;

  while (Date.now() < deadline) {
    try {
      mkdirSync(lockDir);
      writeFileSync(
        resolve(lockDir, "info.json"),
        JSON.stringify({
          pid: process.pid,
          timestamp: Date.now(),
          agent,
        } satisfies LockInfo),
      );

      return {
        path: lockDir,
        release: () => {
          try {
            if (ownsLock(lockDir)) rmSync(lockDir, { recursive: true });
          } catch {
            // Already released
          }
        },
      };
    } catch (err: unknown) {
      if (err instanceof Error && "code" in err && (err as NodeJS.ErrnoException).code === "EEXIST") {
        if (isLockStale(lockDir) && removeStaleLock(lockDir)) continue;
        await sleep(500 + Math.random() * 500);
        continue;
      }
      throw err;
    }
  }

  throw new Error(`Timeout acquiring lock on ${resourcePath}`);
}

export function breakStaleLock(resourcePath: string): boolean {
  const lockDir = resourcePath + ".lock";
  return existsSync(lockDir) && isLockStale(lockDir) && removeStaleLock(lockDir);
}

export function breakLock(resourcePath: string): boolean {
  const lockDir = resourcePath + ".lock";
  if (existsSync(lockDir)) {
    rmSync(lockDir, { recursive: true });
    return true;
  }
  return false;
}
