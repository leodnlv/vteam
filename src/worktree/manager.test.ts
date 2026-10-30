import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { mkdtempSync, mkdirSync, writeFileSync, existsSync } from "node:fs";
import { rmSync } from "node:fs";
import { execFileSync, execSync } from "node:child_process";
import { resolve, join } from "node:path";
import { tmpdir } from "node:os";
import {
  createWorktree,
  checkoutWorktree,
  removeWorktree,
  listWorktrees,
  mergeBaseBranch,
  findUnresolvedConflicts,
} from "./manager.js";

const WT_DIR = ".vteam-worktrees";

let repo: string;

function initRepo(): string {
  const dir = mkdtempSync(join(tmpdir(), "vteam-wt-test-"));
  execFileSync("git", ["init", dir], { stdio: "pipe" });
  execFileSync("git", ["-C", dir, "config", "user.email", "test@test.com"], { stdio: "pipe" });
  execFileSync("git", ["-C", dir, "config", "user.name", "Test"], { stdio: "pipe" });
  writeFileSync(resolve(dir, "file.txt"), "init\n");
  execFileSync("git", ["-C", dir, "add", "-A"], { stdio: "pipe" });
  execFileSync("git", ["-C", dir, "commit", "-m", "initial"], { stdio: "pipe" });
  return dir;
}

function initRepoWithOrigin(): { local: string; origin: string } {
  const origin = initRepo();
  execFileSync("git", ["-C", origin, "checkout", "-b", "feature-branch"], { stdio: "pipe" });
  writeFileSync(resolve(origin, "feature.txt"), "feature\n");
  execFileSync("git", ["-C", origin, "add", "-A"], { stdio: "pipe" });
  execFileSync("git", ["-C", origin, "commit", "-m", "feature commit"], { stdio: "pipe" });
  execFileSync("git", ["-C", origin, "checkout", "main"], { stdio: "pipe" });

  const local = mkdtempSync(join(tmpdir(), "vteam-wt-test-local-"));
  execFileSync("git", ["clone", origin, local], { stdio: "pipe" });
  execFileSync("git", ["-C", local, "config", "user.email", "test@test.com"], { stdio: "pipe" });
  execFileSync("git", ["-C", local, "config", "user.name", "Test"], { stdio: "pipe" });
  return { local, origin };
}

beforeEach(() => {
  repo = initRepo();
});

afterEach(() => {
  const worktrees = listWorktrees(repo);
  const wtBase = resolve(repo, WT_DIR);
  for (const wt of worktrees) {
    if (wt.path.startsWith(wtBase)) {
      removeWorktree(repo, wt.path);
    }
  }
  rmSync(repo, { recursive: true, force: true });
});

describe("createWorktree", () => {
  it("creates a worktree from scratch", () => {
    const wt = createWorktree(repo, "my-task", "main", WT_DIR);
    expect(existsSync(wt.path)).toBe(true);
    expect(wt.branch).toBe("vteam/my-task");
    expect(existsSync(resolve(wt.path, "file.txt"))).toBe(true);
  });

  it("succeeds when worktree already exists at the same path", () => {
    const wt1 = createWorktree(repo, "my-task", "main", WT_DIR);
    expect(existsSync(wt1.path)).toBe(true);

    const wt2 = createWorktree(repo, "my-task", "main", WT_DIR);
    expect(existsSync(wt2.path)).toBe(true);
    expect(wt2.branch).toBe("vteam/my-task");
  });

  it("succeeds when orphaned directory exists without git metadata", () => {
    const expectedPath = resolve(repo, WT_DIR, "vteam/my-task");
    mkdirSync(expectedPath, { recursive: true });
    writeFileSync(resolve(expectedPath, "stale.txt"), "leftover");

    const wt = createWorktree(repo, "my-task", "main", WT_DIR);
    expect(existsSync(wt.path)).toBe(true);
    expect(existsSync(resolve(wt.path, "file.txt"))).toBe(true);
    expect(existsSync(resolve(wt.path, "stale.txt"))).toBe(false);
  });

  it("succeeds when git metadata is stale (directory manually deleted)", () => {
    const wt1 = createWorktree(repo, "my-task", "main", WT_DIR);
    rmSync(wt1.path, { recursive: true, force: true });

    const wt2 = createWorktree(repo, "my-task", "main", WT_DIR);
    expect(existsSync(wt2.path)).toBe(true);
    expect(wt2.branch).toBe("vteam/my-task");
  });
});

describe("checkoutWorktree", () => {
  let local: string;
  let origin: string;

  beforeEach(() => {
    const repos = initRepoWithOrigin();
    local = repos.local;
    origin = repos.origin;
  });

  afterEach(() => {
    const worktrees = listWorktrees(local);
    const wtBase = resolve(local, WT_DIR);
    for (const wt of worktrees) {
      if (wt.path.startsWith(wtBase)) {
        removeWorktree(local, wt.path);
      }
    }
    rmSync(local, { recursive: true, force: true });
    rmSync(origin, { recursive: true, force: true });
  });

  it("checks out a remote branch into a worktree", () => {
    const wt = checkoutWorktree(local, "feature-branch", WT_DIR);
    expect(existsSync(wt.path)).toBe(true);
    expect(wt.branch).toBe("feature-branch");
    expect(existsSync(resolve(wt.path, "feature.txt"))).toBe(true);
  });

  it("succeeds when worktree already exists at the same path", () => {
    const wt1 = checkoutWorktree(local, "feature-branch", WT_DIR);
    expect(existsSync(wt1.path)).toBe(true);

    const wt2 = checkoutWorktree(local, "feature-branch", WT_DIR);
    expect(existsSync(wt2.path)).toBe(true);
    expect(wt2.branch).toBe("feature-branch");
  });

  it("succeeds when orphaned directory exists without git metadata", () => {
    const expectedPath = resolve(local, WT_DIR, "feature-branch");
    mkdirSync(expectedPath, { recursive: true });
    writeFileSync(resolve(expectedPath, "stale.txt"), "leftover");

    const wt = checkoutWorktree(local, "feature-branch", WT_DIR);
    expect(existsSync(wt.path)).toBe(true);
    expect(existsSync(resolve(wt.path, "feature.txt"))).toBe(true);
    expect(existsSync(resolve(wt.path, "stale.txt"))).toBe(false);
  });

  it("succeeds when git metadata is stale (directory manually deleted)", () => {
    const wt1 = checkoutWorktree(local, "feature-branch", WT_DIR);
    rmSync(wt1.path, { recursive: true, force: true });

    const wt2 = checkoutWorktree(local, "feature-branch", WT_DIR);
    expect(existsSync(wt2.path)).toBe(true);
    expect(wt2.branch).toBe("feature-branch");
  });

  it("succeeds when branch is already checked out in another worktree", () => {
    execFileSync("git", ["-C", local, "fetch", "origin", "feature-branch"], { stdio: "pipe" });
    execFileSync(
      "git",
      ["-C", local, "worktree", "add", resolve(local, "dev-worktree"), "origin/feature-branch", "-b", "feature-branch"],
      { stdio: "pipe" },
    );

    const wt = checkoutWorktree(local, "feature-branch", WT_DIR);
    expect(existsSync(wt.path)).toBe(true);
    expect(wt.branch).toBe("feature-branch");
    expect(existsSync(resolve(wt.path, "feature.txt"))).toBe(true);

    execFileSync("git", ["-C", local, "worktree", "remove", "--force", resolve(local, "dev-worktree")], { stdio: "pipe" });
  });

  it("resets to latest remote state when local branch exists", () => {
    execFileSync("git", ["-C", local, "fetch", "origin", "feature-branch"], { stdio: "pipe" });
    execFileSync(
      "git",
      ["-C", local, "branch", "feature-branch", "origin/feature-branch"],
      { stdio: "pipe" },
    );

    const wt = checkoutWorktree(local, "feature-branch", WT_DIR);
    expect(existsSync(wt.path)).toBe(true);

    const log = execSync("git log --oneline -1", {
      cwd: wt.path,
      encoding: "utf-8",
    });
    expect(log).toContain("feature commit");
  });
});

describe("merging the base branch into a PR branch", () => {
  let tmp: string;
  let clone: string;

  function git(...args: string[]): string {
    return execFileSync("git", args, { cwd: clone, encoding: "utf-8", stdio: "pipe" }).trim();
  }

  function commitFile(file: string, content: string, message: string): void {
    writeFileSync(join(clone, file), content);
    git("add", file);
    git("commit", "-q", "-m", message);
  }

  function parentCount(): number {
    return git("rev-list", "--parents", "-n", "1", "HEAD").split(" ").length - 1;
  }

  function advanceMain(file: string, content: string): void {
    git("checkout", "-q", "main");
    commitFile(file, content, "main moves on");
    git("push", "-q", "origin", "main");
    git("checkout", "-q", "feature");
  }

  beforeEach(() => {
    tmp = mkdtempSync(join(tmpdir(), "vteam-merge-test-"));
    const origin = join(tmp, "origin.git");
    clone = join(tmp, "clone");
    execFileSync("git", ["init", "-q", "--bare", "-b", "main", origin]);
    execFileSync("git", ["clone", "-q", origin, clone], { stdio: "pipe" });
    git("config", "user.email", "test@test.com");
    git("config", "user.name", "Test");
    git("checkout", "-q", "-b", "main");
    commitFile("thing.ts", "one\n", "init");
    commitFile("other.ts", "a\n", "other");
    git("push", "-q", "origin", "main");
    git("checkout", "-q", "-b", "feature");
    commitFile("thing.ts", "feature\n", "feature change");
  });

  afterEach(() => {
    rmSync(tmp, { recursive: true, force: true });
  });

  it("merges the base branch and reports no conflicts when it applies cleanly", () => {
    advanceMain("other.ts", "b\n");

    expect(mergeBaseBranch(clone, "main")).toEqual([]);
    expect(parentCount()).toBe(2);
  });

  it("leaves the merge in progress and lists conflicted files, which commit as a merge once resolved", () => {
    advanceMain("thing.ts", "main\n");

    const conflicted = mergeBaseBranch(clone, "main");

    expect(conflicted).toEqual(["thing.ts"]);
    expect(findUnresolvedConflicts(clone, conflicted)).toEqual(["thing.ts"]);

    writeFileSync(join(clone, "thing.ts"), "feature and main\n");
    expect(findUnresolvedConflicts(clone, conflicted)).toEqual([]);

    git("add", "-A");
    git("commit", "-q", "-m", "vteam: resolve conflicts", "-m", "Merged main.");
    expect(parentCount()).toBe(2);
  });

  it("ignores missing files and lines that only resemble conflict markers", () => {
    writeFileSync(join(clone, "doc.md"), "Title\n=======\n<<<<<<<not a marker\n");

    expect(findUnresolvedConflicts(clone, ["doc.md", "deleted.ts"])).toEqual([]);
  });
});
