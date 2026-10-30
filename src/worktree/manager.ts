import { execFileSync, execSync } from "node:child_process";
import { existsSync, readFileSync, rmSync } from "node:fs";
import { resolve } from "node:path";
import { slugify } from "../slugify.js";

export interface WorktreeSession {
  path: string;
  branch: string;
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

  execFileSync(
    "git",
    ["worktree", "add", "-b", branch, worktreePath, baseBranch],
    { cwd: repoRoot, stdio: "pipe" },
  );

  return { path: worktreePath, branch };
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

  return { path: worktreePath, branch: remoteBranch };
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

export function pushBranch(
  worktreePath: string,
  branch: string,
): void {
  execFileSync("git", ["push", "--force", "origin", `HEAD:${branch}`], {
    cwd: worktreePath,
    stdio: "pipe",
  });
}

export function getCommitSha(worktreePath: string): string {
  return execSync("git rev-parse HEAD", {
    cwd: worktreePath,
    encoding: "utf-8",
  }).trim();
}
