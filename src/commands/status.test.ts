import { describe, it, expect, vi, beforeEach, afterEach } from "vitest";
import { hostname } from "node:os";
import type { AgentConfig, Task } from "../types.js";
import type { TaskIndex } from "../tasks/task-manager.js";
import type * as RunStateModule from "../run-state.js";

const mocks = vi.hoisted(() => ({
  loadConfig: vi.fn(),
  listAgentNames: vi.fn(),
  resolveAgentConfig: vi.fn(),
  getIndex: vi.fn(),
  listReady: vi.fn(),
  findLiveRuns: vi.fn(),
  findOrphanedWorktrees: vi.fn(),
}));

vi.mock("../config/load.js", () => ({ loadConfig: mocks.loadConfig }));
vi.mock("../config/agent.js", () => ({
  listAgentNames: mocks.listAgentNames,
  resolveAgentConfig: mocks.resolveAgentConfig,
}));
vi.mock("../tasks/aven-manager.js", () => ({
  AvenTaskManager: class {
    getIndex = mocks.getIndex;
    listReady = mocks.listReady;
  },
}));
vi.mock("../worktree/manager.js", () => ({ findOrphanedWorktrees: mocks.findOrphanedWorktrees }));
vi.mock("../run-state.js", async (importOriginal) => ({
  ...(await importOriginal<typeof RunStateModule>()),
  findLiveRuns: mocks.findLiveRuns,
}));
vi.mock("./cron.js", () => ({ renderScheduler: () => [] }));

import { renderStatus, requireVteamDir } from "./status.js";
import { stripAnsi } from "../ansi.js";

const DEAD_PID = 2 ** 22 + 12345;
const CWD = "/project";

function task(overrides: Partial<Task> & Pick<Task, "ref" | "status">): Task {
  return {
    title: `Task ${overrides.ref}`,
    severity: "medium",
    retryCount: 0,
    metadata: {},
    description: "",
    notes: [],
    createdAt: "2026-01-01T00:00:00.000Z",
    ...overrides,
  };
}

function indexOf(tasks: Task[]): TaskIndex {
  const byStatus = new Map<Task["status"], Task[]>();
  for (const t of tasks) byStatus.set(t.status, [...(byStatus.get(t.status) ?? []), t]);
  return { all: tasks, byStatus, titles: tasks.map((t) => t.title) };
}

function agent(overrides: Partial<AgentConfig> & Pick<AgentConfig, "name">): AgentConfig {
  return { worktree: false, ...overrides } as AgentConfig;
}

function givenAgents(...agents: AgentConfig[]): void {
  mocks.listAgentNames.mockReturnValue(agents.map((a) => a.name));
  mocks.resolveAgentConfig.mockImplementation((name: string) => agents.find((a) => a.name === name));
}

function givenTasks(tasks: Task[], ready: Task[] = []): void {
  mocks.getIndex.mockResolvedValue(indexOf(tasks));
  mocks.listReady.mockResolvedValue(ready);
}

async function render(): Promise<string> {
  return stripAnsi((await renderStatus(CWD)).join("\n"));
}

describe("renderStatus", () => {
  beforeEach(() => {
    mocks.loadConfig.mockReturnValue({
      tasks: { project: "my-app", label: "vteam", maxRetries: 3 },
      worktreeDir: ".vteam-worktrees",
    });
    mocks.findLiveRuns.mockReturnValue([]);
    mocks.findOrphanedWorktrees.mockReturnValue([]);
    givenAgents();
    givenTasks([]);
  });

  afterEach(() => {
    vi.clearAllMocks();
  });

  it("shows the aven project and label in the banner", async () => {
    const output = await render();

    expect(output).toContain("aven: my-app · label: vteam");
  });

  it("says nothing is running and omits empty sections", async () => {
    const output = await render();

    expect(output).toContain("nothing running");
    expect(output).not.toContain("Up next");
    expect(output).not.toContain("Awaiting merge");
    expect(output).not.toContain("Needs attention");
  });

  it("lists a running job with its pid, claimed task and worktree path", async () => {
    const active = task({
      ref: "APP-1",
      status: "active",
      title: "Fix login",
      severity: "high",
      metadata: { "claimed-host": hostname(), "claimed-pid": "4242" },
    });
    givenTasks([active]);
    mocks.findLiveRuns.mockReturnValue([
      {
        name: "coder",
        lock: { pid: 4242, timestamp: Date.now() - 5000, agent: "coder" },
        state: { worktreePath: "/project/.vteam-worktrees/wt-1" },
      },
    ]);

    const output = await render();

    expect(output).toContain("coder");
    expect(output).toContain("pid 4242");
    expect(output).toContain("APP-1 [high] Fix login");
    expect(output).toContain("worktree .vteam-worktrees/wt-1");
  });

  it("does not attach an active task claimed on another host to a running job", async () => {
    givenTasks([
      task({
        ref: "APP-2",
        status: "active",
        metadata: { "claimed-host": "other-machine", "claimed-pid": "4242" },
      }),
    ]);
    mocks.findLiveRuns.mockReturnValue([{ name: "coder", lock: { pid: 4242, timestamp: Date.now(), agent: "coder" } }]);

    const output = await render();

    expect(output).toContain("pid 4242");
    expect(output).not.toContain("[medium]");
  });

  it("shows the next task each task agent would pick, with a follow-up marker for worktree agents", async () => {
    const ready = [
      task({ ref: "APP-3", status: "todo", severity: "low", title: "Tidy docs" }),
      task({ ref: "APP-4", status: "todo", severity: "critical", title: "Patch leak", metadata: { "pr-url": "https://github.com/o/r/pull/9" } }),
    ];
    givenAgents(agent({ name: "coder", input: "task", worktree: true }));
    givenTasks(ready, ready);

    const output = await render();

    expect(output).toContain("Up next");
    expect(output).toContain("APP-4 [critical] Patch leak (PR follow-up)");
    expect(output).not.toContain("APP-3 [low]");
  });

  it("says nothing is ready when no task is routed to a task agent", async () => {
    givenAgents(agent({ name: "coder", input: "task", taskFilter: { "found-by": "improver" } }));
    const ready = [task({ ref: "APP-5", status: "todo", metadata: { "found-by": "security-analyst" } })];
    givenTasks(ready, ready);

    const output = await render();

    expect(output).toContain("nothing ready");
  });

  it("describes what a pr agent watches for", async () => {
    givenAgents(
      agent({ name: "reviewer", input: "pr", worktree: true, prTriggerLabel: "vteam:changes-requested" }),
      agent({ name: "fixer", input: "pr", worktree: true }),
    );

    const output = await render();

    expect(output).toContain('open PRs labelled "vteam:changes-requested"');
    expect(output).toContain("any open PR with review feedback");
  });

  it("skips agents without an input in the up next section", async () => {
    givenAgents(agent({ name: "improver" }));

    const output = await render();

    expect(output).not.toContain("Up next");
  });

  it("lists done tasks with an unmerged PR and leaves merged ones out", async () => {
    givenTasks([
      task({ ref: "APP-6", status: "done", metadata: { "pr-url": "https://github.com/o/r/pull/1" } }),
      task({ ref: "APP-7", status: "done", metadata: { "pr-url": "https://github.com/o/r/pull/2", merged: "true" } }),
      task({ ref: "APP-8", status: "done" }),
    ]);

    const output = await render();

    expect(output).toContain("Awaiting merge (1)");
    expect(output).toContain("APP-6");
    expect(output).toContain("https://github.com/o/r/pull/1");
    expect(output).not.toContain("APP-7");
    expect(output).not.toContain("APP-8");
  });

  it("reports inbox tasks, unroutable tasks and retry-exhausted tasks under needs attention", async () => {
    givenAgents(agent({ name: "coder", input: "task", taskFilter: { "found-by": "improver" } }));
    givenTasks([
      task({ ref: "APP-9", status: "inbox" }),
      task({ ref: "APP-10", status: "todo", metadata: { "found-by": "someone-else" } }),
      task({ ref: "APP-11", status: "todo", retryCount: 3, metadata: { "found-by": "improver" } }),
      task({ ref: "APP-12", status: "todo", retryCount: 1, metadata: { "found-by": "improver" } }),
    ]);

    const output = await render();

    expect(output).toContain("Needs attention");
    expect(output).toContain("1 task(s) in inbox awaiting triage");
    expect(output).toContain("APP-10 matches no agent's taskFilter and will never be picked");
    expect(output).toContain("APP-11 hit max retries (3/3) and will not be picked");
    expect(output).not.toContain("APP-12 ");
  });

  it("flags active tasks without a claim and active tasks whose local process is dead", async () => {
    givenTasks([
      task({ ref: "APP-13", status: "active" }),
      task({ ref: "APP-14", status: "active", metadata: { "claimed-host": hostname(), "claimed-pid": String(DEAD_PID) } }),
      task({ ref: "APP-15", status: "active", metadata: { "claimed-host": "other-machine", "claimed-pid": String(DEAD_PID) } }),
      task({ ref: "APP-16", status: "active", metadata: { "claimed-host": hostname(), "claimed-pid": String(process.pid) } }),
    ]);

    const output = await render();

    expect(output).toContain("APP-13 is active without a vteam claim");
    expect(output).toContain(`APP-14 is active but its process (pid ${DEAD_PID}) is dead`);
    expect(output).not.toContain("APP-15");
    expect(output).not.toContain("APP-16");
  });

  it("reports an agent that fails to load without hiding the others", async () => {
    mocks.listAgentNames.mockReturnValue(["broken", "coder"]);
    mocks.resolveAgentConfig.mockImplementation((name: string) => {
      if (name === "broken") throw new Error("unknown key \"foo\"");
      return agent({ name, input: "task" });
    });

    const output = await render();

    expect(output).toContain('agent "broken" failed to load: unknown key "foo"');
    expect(output).toContain("coder");
  });

  it("lists leftover worktrees relative to the project directory", async () => {
    mocks.findOrphanedWorktrees.mockReturnValue(["/project/.vteam-worktrees/old-1"]);

    const output = await render();

    expect(output).toContain("1 worktree(s) with no running agent");
    expect(output).toContain(".vteam-worktrees/old-1");
    expect(mocks.findOrphanedWorktrees).toHaveBeenCalledWith(CWD, ".vteam-worktrees", []);
  });

  it("ignores a failure while looking for leftover worktrees", async () => {
    mocks.findOrphanedWorktrees.mockImplementation(() => {
      throw new Error("not a git repo");
    });

    const output = await render();

    expect(output).not.toContain("Needs attention");
  });
});

describe("requireVteamDir", () => {
  afterEach(() => {
    vi.restoreAllMocks();
  });

  it("exits with an error when there is no vteam directory", () => {
    const exit = vi.spyOn(process, "exit").mockImplementation((() => {
      throw new Error("exit");
    }) as never);
    const error = vi.spyOn(console, "error").mockImplementation(() => {});

    expect(() => requireVteamDir("/definitely/not/a/real/project")).toThrow("exit");

    expect(exit).toHaveBeenCalledWith(1);
    expect(stripAnsi(String(error.mock.calls[0]?.[0]))).toContain("No vteam/ directory found");
  });
});
