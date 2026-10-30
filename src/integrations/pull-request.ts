import { execFileSync, execSync } from "node:child_process";
import type {
  Platform,
  PRState,
  PullRequestInfo,
  ReviewablePR,
  ReviewComment,
  ReviewThread,
} from "../types.js";

export function findReviewablePR(
  platform: Platform,
  labels: string[],
  cwd: string,
): ReviewablePR | null {
  const prs =
    platform === "github"
      ? findGitHubPRsByLabels(labels, cwd)
      : findGitLabMRsByLabels(labels, cwd);
  return prs[0] ?? null;
}

export function getReviewComments(
  platform: Platform,
  prNumber: number,
  cwd: string,
): ReviewComment[] {
  return platform === "github"
    ? getGitHubReviewComments(prNumber, cwd)
    : getGitLabReviewComments(prNumber, cwd);
}

export function getPullRequest(
  platform: Platform,
  url: string,
  cwd: string,
): PullRequestInfo {
  return platform === "github"
    ? getGitHubPR(url, cwd)
    : getGitLabMR(url, cwd);
}

export function getOpenReviewThreads(
  platform: Platform,
  prNumber: number,
  cwd: string,
): ReviewThread[] {
  return platform === "github"
    ? getGitHubOpenThreads(prNumber, cwd)
    : getGitLabOpenThreads(prNumber, cwd);
}

export function replyToReviewThread(
  platform: Platform,
  prNumber: number,
  threadId: string,
  body: string,
  cwd: string,
): void {
  if (platform === "github") {
    githubGraphQL(
      `mutation($id: ID!, $body: String!) {
        addPullRequestReviewThreadReply(input: { pullRequestReviewThreadId: $id, body: $body }) { comment { id } }
      }`,
      { id: threadId, body },
      cwd,
    );
  } else {
    execFileSync(
      "glab",
      ["api", `projects/:id/merge_requests/${prNumber}/discussions/${threadId}/notes`, "-f", `body=${body}`],
      { cwd, stdio: "pipe" },
    );
  }
}

export function resolveReviewThread(
  platform: Platform,
  prNumber: number,
  threadId: string,
  cwd: string,
): void {
  if (platform === "github") {
    githubGraphQL(
      `mutation($id: ID!) {
        resolveReviewThread(input: { threadId: $id }) { thread { isResolved } }
      }`,
      { id: threadId },
      cwd,
    );
  } else {
    execFileSync(
      "glab",
      ["api", "-X", "PUT", `projects/:id/merge_requests/${prNumber}/discussions/${threadId}`, "-f", "resolved=true"],
      { cwd, stdio: "pipe" },
    );
  }
}

export function postPRComment(
  platform: Platform,
  prNumber: number,
  body: string,
  cwd: string,
): void {
  if (platform === "github") {
    execSync(`gh pr comment ${prNumber} --body ${shellEscape(body)}`, {
      cwd,
      stdio: "pipe",
    });
  } else {
    execSync(`glab mr note ${prNumber} -m ${shellEscape(body)}`, {
      cwd,
      stdio: "pipe",
    });
  }
}

export function removePRLabel(
  platform: Platform,
  prNumber: number,
  label: string,
  cwd: string,
): void {
  try {
    if (platform === "github") {
      execSync(
        `gh pr edit ${prNumber} --remove-label ${shellEscape(label)}`,
        { cwd, stdio: "pipe" },
      );
    } else {
      execSync(
        `glab mr update ${prNumber} --unlabel ${shellEscape(label)}`,
        { cwd, stdio: "pipe" },
      );
    }
  } catch {
    // Best-effort — label removal failing shouldn't block the run
  }
}

// ---- GitHub ----

interface GHPRListItem {
  number: number;
  title: string;
  headRefName: string;
  url: string;
}

function findGitHubPRsByLabels(
  labels: string[],
  cwd: string,
): ReviewablePR[] {
  const labelQuery = labels.map((l) => `label:${l}`).join(" ");
  const search = labelQuery || "is:open";

  const result = execSync(
    `gh pr list --search ${shellEscape(search)} --json number,title,headRefName,url --limit 10`,
    { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
  );

  const items: GHPRListItem[] = JSON.parse(result.trim() || "[]");

  return items.map((pr) => ({
    number: pr.number,
    title: pr.title,
    branch: pr.headRefName,
    url: pr.url,
  }));
}

export function getRepoSlug(platform: Platform, cwd: string): string {
  if (platform === "github") {
    return execSync(
      "gh repo view --json nameWithOwner --jq .nameWithOwner",
      { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
    ).trim();
  }
  const result = execSync(
    'glab api "projects/:id"',
    { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
  );
  const project = JSON.parse(result.trim()) as { path_with_namespace: string };
  return project.path_with_namespace;
}

function getGitHubReviewComments(
  prNumber: number,
  cwd: string,
): ReviewComment[] {
  const prResult = execSync(
    `gh pr view ${prNumber} --json reviews,comments`,
    { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
  );

  const prData = JSON.parse(prResult) as {
    reviews: Array<{
      author: { login: string };
      body: string;
      state: string;
      submittedAt: string;
    }>;
    comments: Array<{
      author: { login: string };
      body: string;
      createdAt: string;
    }>;
  };

  const slug = getRepoSlug("github", cwd);
  const inlineResult = execSync(
    `gh api "repos/${slug}/pulls/${prNumber}/comments"`,
    { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
  );

  const inlineComments = JSON.parse(inlineResult.trim() || "[]") as Array<{
    user: { login: string };
    body: string;
    path: string;
    line: number | null;
    original_line: number | null;
    created_at: string;
  }>;

  const comments: ReviewComment[] = [];

  for (const review of prData.reviews) {
    if (review.body?.trim()) {
      comments.push({
        author: review.author.login,
        body: review.body,
        createdAt: review.submittedAt,
      });
    }
  }

  for (const rc of inlineComments) {
    comments.push({
      author: rc.user.login,
      body: rc.body,
      path: rc.path,
      line: rc.line ?? rc.original_line ?? undefined,
      createdAt: rc.created_at,
    });
  }

  for (const c of prData.comments) {
    comments.push({
      author: c.author.login,
      body: c.body,
      createdAt: c.createdAt,
    });
  }

  return comments.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
}

function getGitHubPR(url: string, cwd: string): PullRequestInfo {
  const result = execFileSync(
    "gh",
    ["pr", "view", url, "--json", "number,title,headRefName,url,state"],
    { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
  );
  const pr = JSON.parse(result) as {
    number: number;
    title: string;
    headRefName: string;
    url: string;
    state: "OPEN" | "CLOSED" | "MERGED";
  };
  return {
    number: pr.number,
    title: pr.title,
    branch: pr.headRefName,
    url: pr.url,
    state: pr.state.toLowerCase() as PRState,
  };
}

const GITHUB_REVIEW_THREADS_QUERY = `
query($owner: String!, $name: String!, $number: Int!) {
  repository(owner: $owner, name: $name) {
    pullRequest(number: $number) {
      reviewThreads(first: 100) {
        nodes {
          id
          isResolved
          path
          line
          originalLine
          comments(first: 100) {
            nodes { author { login } body createdAt }
          }
        }
      }
    }
  }
}`;

function githubGraphQL(
  query: string,
  variables: Record<string, string | number>,
  cwd: string,
): string {
  const args = ["api", "graphql", "-f", `query=${query}`];
  for (const [key, value] of Object.entries(variables)) {
    args.push(typeof value === "number" ? "-F" : "-f", `${key}=${value}`);
  }
  return execFileSync("gh", args, { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] });
}

function getGitHubOpenThreads(prNumber: number, cwd: string): ReviewThread[] {
  const [owner, name] = getRepoSlug("github", cwd).split("/");
  const result = githubGraphQL(GITHUB_REVIEW_THREADS_QUERY, { owner, name, number: prNumber }, cwd);

  const data = JSON.parse(result) as {
    data: {
      repository: {
        pullRequest: {
          reviewThreads: {
            nodes: Array<{
              id: string;
              isResolved: boolean;
              path: string;
              line: number | null;
              originalLine: number | null;
              comments: {
                nodes: Array<{
                  author: { login: string } | null;
                  body: string;
                  createdAt: string;
                }>;
              };
            }>;
          };
        };
      };
    };
  };

  return data.data.repository.pullRequest.reviewThreads.nodes
    .filter((thread) => !thread.isResolved)
    .map((thread) => ({
      id: thread.id,
      path: thread.path,
      line: thread.line ?? thread.originalLine ?? undefined,
      comments: thread.comments.nodes.map((c) => ({
        author: c.author?.login ?? "ghost",
        body: c.body,
        createdAt: c.createdAt,
      })),
    }));
}

// ---- GitLab ----

function findGitLabMRsByLabels(
  labels: string[],
  cwd: string,
): ReviewablePR[] {
  const labelParam =
    labels.length > 0
      ? `&labels=${labels.map(encodeURIComponent).join(",")}`
      : "";

  const result = execSync(
    `glab api "projects/:id/merge_requests?state=opened${labelParam}"`,
    { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
  );

  const mrs = JSON.parse(result.trim() || "[]") as Array<{
    iid: number;
    title: string;
    source_branch: string;
    web_url: string;
  }>;

  return mrs.map((mr) => ({
    number: mr.iid,
    title: mr.title,
    branch: mr.source_branch,
    url: mr.web_url,
  }));
}

function getGitLabReviewComments(
  mrIid: number,
  cwd: string,
): ReviewComment[] {
  const result = execSync(
    `glab api "projects/:id/merge_requests/${mrIid}/discussions"`,
    { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
  );

  const discussions = JSON.parse(result.trim() || "[]") as Array<{
    notes: Array<{
      author: { username: string };
      body: string;
      system: boolean;
      resolvable: boolean;
      resolved: boolean;
      position?: { new_path: string; new_line: number };
      created_at: string;
    }>;
  }>;

  const comments: ReviewComment[] = [];

  for (const discussion of discussions) {
    for (const note of discussion.notes) {
      if (!note.system) {
        comments.push({
          author: note.author.username,
          body: note.body,
          path: note.position?.new_path,
          line: note.position?.new_line,
          createdAt: note.created_at,
        });
      }
    }
  }

  return comments.sort((a, b) => a.createdAt.localeCompare(b.createdAt));
}

function getGitLabMR(url: string, cwd: string): PullRequestInfo {
  const iid = /\/merge_requests\/(\d+)/.exec(url)?.[1];
  if (!iid) {
    throw new Error(`Not a GitLab merge request URL: ${url}`);
  }

  const result = execFileSync(
    "glab",
    ["api", `projects/:id/merge_requests/${iid}`],
    { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
  );
  const mr = JSON.parse(result) as {
    iid: number;
    title: string;
    source_branch: string;
    web_url: string;
    state: "opened" | "closed" | "locked" | "merged";
  };
  return {
    number: mr.iid,
    title: mr.title,
    branch: mr.source_branch,
    url: mr.web_url,
    state: mr.state === "opened" ? "open" : mr.state === "merged" ? "merged" : "closed",
  };
}

function getGitLabOpenThreads(mrIid: number, cwd: string): ReviewThread[] {
  const result = execFileSync(
    "glab",
    ["api", `projects/:id/merge_requests/${mrIid}/discussions?per_page=100`],
    { cwd, encoding: "utf-8", stdio: ["pipe", "pipe", "pipe"] },
  );

  const discussions = JSON.parse(result.trim() || "[]") as Array<{
    id: string;
    notes: Array<{
      author: { username: string };
      body: string;
      system: boolean;
      resolvable: boolean;
      resolved: boolean;
      position?: { new_path: string; new_line: number | null };
      created_at: string;
    }>;
  }>;

  return discussions
    .filter((d) => d.notes.some((n) => n.resolvable && !n.resolved))
    .map((d) => {
      const position = d.notes[0].position;
      return {
        id: d.id,
        path: position?.new_path,
        line: position?.new_line ?? undefined,
        comments: d.notes
          .filter((n) => !n.system)
          .map((n) => ({
            author: n.author.username,
            body: n.body,
            createdAt: n.created_at,
          })),
      };
    });
}

function shellEscape(s: string): string {
  return `'${s.replace(/'/g, "'\\''")}'`;
}
