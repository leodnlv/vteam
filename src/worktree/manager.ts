import { execFileSync, execSync } from "node:child_process";
import { existsSync, readFileSync, rmSync } from "node:fs";
import { resolve } from "node:path";
import { slugify } from "../slugify.js";

export interface WorktreeSession {
  path: string;
  branch: string;
  remoteSha: string;
}

export function createWorktree(
  repoRoot: string,
  taskSlug: string,
  baseBranch: string,
  worktreeDir: string,
): WorktreeSession {
  const branch = `vteam/${slugify(taskSlug)}`;
  const worktreePath = resolve(repoRoot, worktreeDir, branch);

  ensureCleanWorktreePath(repoRoot, worktreePath);

  try {
    execFileSync("git", ["branch", "-D", branch], { cwd: repoRoot, stdio: "pipe" });
  } catch {
    // Branch doesn't exist — expected path
  }

  execFileSync("git", ["fetch", "origin", baseBranch], { cwd: repoRoot, stdio: "pipe" });

  execFileSync(
    "git",
    ["worktree", "add", "--no-track", "-b", branch, worktreePath, `origin/${baseBranch}`],
    { cwd: repoRoot, stdio: "pipe" },
  );

  return { path: worktreePath, branch, remoteSha: "" };
}

export function checkoutWorktree(
  repoRoot: string,
  remoteBranch: string,
  worktreeDir: string,
): WorktreeSession {
  const worktreePath = resolve(repoRoot, worktreeDir, remoteBranch);

  ensureCleanWorktreePath(repoRoot, worktreePath);

  execFileSync("git", ["fetch", "origin", remoteBranch], {
    cwd: repoRoot,
    stdio: "pipe",
  });

  execFileSync(
    "git",
    ["worktree", "add", "--detach", worktreePath, `origin/${remoteBranch}`],
    { cwd: repoRoot, stdio: "pipe" },
  );

  const remoteSha = execFileSync("git", ["rev-parse", "HEAD"], {
    cwd: worktreePath,
    encoding: "utf-8",
  }).trim();

  return { path: worktreePath, branch: remoteBranch, remoteSha };
}

export function mergeBaseBranch(worktreePath: string, baseBranch: string): string[] {
  execFileSync("git", ["fetch", "origin", baseBranch], { cwd: worktreePath, stdio: "pipe" });
  try {
    execFileSync("git", ["merge", "--no-edit", `origin/${baseBranch}`], { cwd: worktreePath, stdio: "pipe" });
    return [];
  } catch (err) {
    const conflicted = execFileSync("git", ["diff", "--name-only", "--diff-filter=U"], {
      cwd: worktreePath,
      encoding: "utf-8",
    })
      .split("\n")
      .filter(Boolean);
    if (conflicted.length === 0) throw err;
    return conflicted;
  }
}

const CONFLICT_MARKER = /^(<{7}|>{7})(?: |$)/m;

export function findUnresolvedConflicts(worktreePath: string, files: string[]): string[] {
  return files.filter((file) => {
    const path = resolve(worktreePath, file);
    return existsSync(path) && CONFLICT_MARKER.test(readFileSync(path, "utf-8"));
  });
}

function ensureCleanWorktreePath(repoRoot: string, worktreePath: string): void {
  try {
    execFileSync("git", ["worktree", "remove", "--force", worktreePath], {
      cwd: repoRoot,
      stdio: "pipe",
    });
    return;
  } catch {
    // git worktree remove failed — either nothing exists, or stale state
  }
  if (existsSync(worktreePath)) {
    rmSync(worktreePath, { recursive: true, force: true });
  }
  execSync("git worktree prune", { cwd: repoRoot, stdio: "pipe" });
}

export function removeWorktree(
  repoRoot: string,
  worktreePath: string,
): void {
  try {
    execFileSync("git", ["worktree", "remove", worktreePath, "--force"], {
      cwd: repoRoot,
      stdio: "pipe",
    });
  } catch {
    if (existsSync(worktreePath)) {
      rmSync(worktreePath, { recursive: true, force: true });
      execSync("git worktree prune", { cwd: repoRoot, stdio: "pipe" });
    }
  }
}

export function listWorktrees(
  repoRoot: string,
): Array<{ path: string; branch: string }> {
  const output = execSync("git worktree list --porcelain", {
    cwd: repoRoot,
    encoding: "utf-8",
  });

  const worktrees: Array<{ path: string; branch: string }> = [];
  let currentPath = "";

  for (const line of output.split("\n")) {
    if (line.startsWith("worktree ")) {
      currentPath = line.slice(9);
    } else if (line.startsWith("branch ")) {
      worktrees.push({ path: currentPath, branch: line.slice(7) });
    }
  }

  return worktrees;
}

export function cleanupOrphanedWorktrees(
  repoRoot: string,
  worktreeDir: string,
): string[] {
  const cleaned: string[] = [];
  const worktrees = listWorktrees(repoRoot);
  const wtBase = resolve(repoRoot, worktreeDir);

  for (const wt of worktrees) {
    if (!wt.path.startsWith(wtBase)) continue;
    removeWorktree(repoRoot, wt.path);
    cleaned.push(wt.path);
  }

  return cleaned;
}

const TRANSIENT_PUSH_ERROR =
  /connection reset|broken pipe|remote end hung up|unexpected disconnect|early EOF|RPC failed|timed out|could not resolve host|connection closed by|kex_exchange_identification/i;

export function isTransientPushError(err: unknown): boolean {
  const stderr = (err as { stderr?: Buffer | string } | null)?.stderr?.toString() ?? "";
  const message = err instanceof Error ? err.message : String(err);
  return TRANSIENT_PUSH_ERROR.test(`${message}\n${stderr}`);
}

function remoteBranchSha(worktreePath: string, branch: string): string | undefined {
  try {
    const line = execFileSync("git", ["ls-remote", "origin", `refs/heads/${branch}`], {
      cwd: worktreePath,
      encoding: "utf-8",
      stdio: "pipe",
    });
    return line.split(/\s/)[0] || undefined;
  } catch {
    return undefined;
  }
}

export async function pushBranch(
  worktreePath: string,
  branch: string,
  expectedRemoteSha: string,
  retryDelaysMs: number[] = [5_000, 15_000],
): Promise<void> {
  const head = getCommitSha(worktreePath);
  for (let attempt = 0; ; attempt++) {
    try {
      execFileSync(
        "git",
        ["push", `--force-with-lease=refs/heads/${branch}:${expectedRemoteSha}`, "origin", `HEAD:refs/heads/${branch}`],
        { cwd: worktreePath, stdio: "pipe" },
      );
      return;
    } catch (err) {
      if (attempt >= retryDelaysMs.length || !isTransientPushError(err)) throw err;
      console.warn(`Push failed with a network error, retrying in ${retryDelaysMs[attempt] / 1000}s...`);
      await new Promise((r) => setTimeout(r, retryDelaysMs[attempt]));
      // A dropped connection can still land the push, and the lease would then reject the retry.
      if (remoteBranchSha(worktreePath, branch) === head) return;
    }
  }
}

export function getCommitSha(worktreePath: string): string {
  return execSync("git rev-parse HEAD", {
    cwd: worktreePath,
    encoding: "utf-8",
  }).trim();
}
