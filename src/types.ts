export type TaskStatus =
  | "inbox"
  | "backlog"
  | "todo"
  | "active"
  | "done"
  | "canceled";

export type Severity = "critical" | "high" | "medium" | "low";

export type Platform = "github" | "gitlab";

export interface Task {
  ref: string;
  title: string;
  status: TaskStatus;
  severity: Severity;
  files: string[];
  retryCount: number;
  metadata: Record<string, string>;
  description: string;
  createdAt: string;
}

export interface TaskMetadata {
  "found-by"?: string;
  files?: string;
  "retry-count"?: string;
  completed?: string;
  branch?: string;
  "pr-url"?: string;
  "claimed-by"?: string;
  "claimed-pid"?: string;
  "claimed-host"?: string;
}

export type TaskFilterValue = string | null;

export type TaskFilter = Record<string, TaskFilterValue | TaskFilterValue[]>;

export interface OnFinishConfig {
  onFinishMdPath: string;
  model?: string;
  allowedTools?: string[];
  disallowedTools?: string[];
}

export interface MemoryConfig {
  memoryMdPath: string;
  model?: string;
  allowedTools?: string[];
  disallowedTools?: string[];
}

export interface AgentConfig {
  name: string;
  agentMdPath: string;
  model?: string;
  cron?: string;
  worktree?: boolean;
  readOnly?: boolean;
  output?: "task";
  input?: "task" | "pr";
  taskFilter?: TaskFilter;
  prTriggerLabel?: string;
  autoPR?: boolean;
  prCreateLabels?: string[];
  allowedTools?: string[];
  disallowedTools?: string[];
  onFinish?: OnFinishConfig;
  memory?: MemoryConfig;
}

export interface RunOutcome {
  agent: string;
  status: "completed" | "failed";
  startedAt: string;
  completedAt: string;
  error?: string;
  focus?: string;
  task?: { title: string; severity: Severity; files: string[] };
  branch?: string;
  prUrl?: string;
  reviewedPR?: { number: number; title: string; url: string };
  tasksCreated?: string[];
  commitMessage?: CommitMessage;
  content?: AgentContent;
}

export interface ReviewComment {
  author: string;
  body: string;
  path?: string;
  line?: number;
  createdAt: string;
}

export interface ReviewablePR {
  number: number;
  title: string;
  branch: string;
  url: string;
}

export interface PRReviewContext {
  pr: ReviewablePR;
  comments: ReviewComment[];
  repoSlug: string;
}

export interface TasksConfig {
  maxRetries: number;
  project: string;
  label: string;
  workspace?: string;
}

export interface VteamConfig {
  baseBranch: string;
  platform: Platform;
  worktreeDir: string;
  tasks: TasksConfig;
}

export interface CommitMessage {
  subject: string;
  body: string;
}

export interface TaskContentBody {
  title: string;
  severity: Severity;
  description: string;
  suggestedFix?: string;
  files: string[];
}

export interface TaskContent {
  type: "task";
  body: TaskContentBody;
}

export interface GenericContent {
  type: "generic";
  body: string;
}

export type AgentContent = TaskContent | GenericContent;

export interface AgentOutput {
  status: "completed" | "partial" | "blocked" | "failed";
  summary: string;
  content?: AgentContent;
  filesChanged?: string[];
  commitMessage?: CommitMessage;
  blockerReason?: string;
  memoryUpdate?: string;
}

export interface RunState {
  runId: string;
  agent: string;
  status:
    | "started"
    | "claude_running"
    | "processing"
    | "completed"
    | "failed";
  startedAt: string;
  completedAt?: string;
  worktreePath?: string;
  branchName?: string;
  taskRef?: string;
  error?: string;
  claudeOutput?: AgentOutput;
  tasksCreated?: string[];
  commitSha?: string;
  commitMessage?: CommitMessage;
}

