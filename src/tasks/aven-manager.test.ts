import { describe, it, expect, beforeEach, afterEach } from "vitest";
import { execFileSync } from "node:child_process";
import { mkdtempSync, rmSync } from "node:fs";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { AvenError, AvenTaskManager, runAven, type AvenExec } from "./aven-manager.js";
import type { TaskContentBody, TasksConfig } from "../types.js";

const config: TasksConfig = {
  maxRetries: 3,
  project: "app",
  label: "vteam",
  defaultActor: "coder",
};

const finding: TaskContentBody = {
  title: "Null check missing",
  severity: "critical",
  description: "Crashes on empty input.",
  suggestedFix: "Guard the input.",
  files: ["src/a.ts:10", "src/b.ts"],
};

function detail(ref: string, overrides: Record<string, unknown> = {}) {
  return JSON.stringify({
    task: {
      ref,
      title: `Task ${ref}`,
      status: "todo",
      priority: "high",
      created_at: "2026-10-01T00:00:00Z",
    },
    description: "body",
    metadata: { files: "a.ts, b.ts", "retry-count": "2" },
    ...overrides,
  });
}

function fakeExec(handler: (args: string[]) => string) {
  const calls: { args: string[]; stdin?: string }[] = [];
  const exec: AvenExec = async (args, stdin) => {
    calls.push({ args, stdin });
    return handler(args);
  };
  return { exec, calls };
}

describe("AvenTaskManager", () => {
  it("creates a labelled todo task with metadata and returns its ref", async () => {
    const { exec, calls } = fakeExec((args) =>
      args[0] === "add" ? 'created APP-7KQ9 ref=7KQ9 project=app status=todo priority=urgent title="x"\n' : "",
    );

    const ref = await new AvenTaskManager(config, exec).create(finding, "improver", "fixer");

    expect(ref).toBe("APP-7KQ9");
    expect(calls[0].args).toEqual(["label", "create", "vteam"]);
    expect(calls[1].args).toEqual([
      "add", "Null check missing",
      "--project", "app",
      "--status", "todo",
      "--priority", "urgent",
      "--label", "vteam",
      "--metadata", "found-by=improver",
      "--metadata", "actor=fixer",
      "--metadata", "files=src/a.ts:10, src/b.ts",
      "--description-stdin",
    ]);
    expect(calls[1].stdin).toContain("## Suggested Fix\n\nGuard the input.");
    expect(calls[1].stdin).toContain("- `src/b.ts`");
  });

  it("omits actor metadata when no actor is given", async () => {
    const { exec, calls } = fakeExec((args) => (args[0] === "add" ? "created APP-0001 ref=0001\n" : ""));

    await new AvenTaskManager(config, exec).create(finding, "improver");

    expect(calls[1].args.join(" ")).not.toContain("actor=");
  });

  it("throws when the created ref cannot be parsed", async () => {
    const { exec } = fakeExec(() => "something unexpected");
    await expect(new AvenTaskManager(config, exec).create(finding, "x")).rejects.toThrow(
      "Could not parse task ref",
    );
  });

  it("lists only tasks assigned to a non-default actor", async () => {
    const { exec, calls } = fakeExec((args) =>
      args[0] === "list" ? JSON.stringify([{ ref: "APP-0001" }]) : detail(args[1]),
    );

    const tasks = await new AvenTaskManager(config, exec).listReady("test-writer");

    expect(calls[0].args).toEqual([
      "list", "--project", "app", "--label", "vteam",
      "--status", "todo", "--ready", "--metadata", "actor=test-writer", "--json",
    ]);
    expect(calls[1].args).toEqual(["show", "APP-0001", "--full", "--json"]);
    expect(calls.filter((c) => c.args[0] === "list")).toHaveLength(1);
    expect(tasks).toEqual([
      {
        ref: "APP-0001",
        title: "Task APP-0001",
        status: "todo",
        severity: "high",
        files: ["a.ts", "b.ts"],
        retryCount: 2,
        actor: undefined,
        description: "body",
        createdAt: "2026-10-01T00:00:00Z",
      },
    ]);
  });

  it("adds unassigned tasks for the default actor", async () => {
    const { exec, calls } = fakeExec((args) => {
      if (args[0] !== "list") return detail(args[1]);
      return JSON.stringify([{ ref: args.includes("--missing-metadata") ? "APP-FREE" : "APP-MINE" }]);
    });

    const tasks = await new AvenTaskManager(config, exec).listReady("coder");

    expect(tasks.map((t) => t.ref)).toEqual(["APP-MINE", "APP-FREE"]);
    expect(calls.find((c) => c.args.includes("--missing-metadata"))?.args).toEqual([
      "list", "--project", "app", "--label", "vteam",
      "--status", "todo", "--ready", "--missing-metadata", "actor", "--json",
    ]);
  });

  it("treats every task as unassigned when the actor field does not exist yet", async () => {
    const exec: AvenExec = async (args) => {
      if (args[0] === "show") return detail(args[1]);
      if (args.some((a) => a.startsWith("actor") || a === "--missing-metadata")) {
        throw new AvenError(args, "Error: error unknown-metadata-field");
      }
      return JSON.stringify([{ ref: "APP-0001" }]);
    };

    const manager = new AvenTaskManager(config, exec);

    expect((await manager.listReady("coder")).map((t) => t.ref)).toEqual(["APP-0001"]);
    expect(await manager.listReady("test-writer")).toEqual([]);
  });

  it("maps priority none and missing metadata to safe defaults", async () => {
    const { exec } = fakeExec((args) =>
      args[0] === "list"
        ? JSON.stringify([{ ref: "APP-0002" }])
        : detail(args[1], {
            task: { ref: "APP-0002", title: "Human task", status: "todo", priority: "none", created_at: "" },
            metadata: {},
          }),
    );

    const [task] = await new AvenTaskManager(config, exec).listReady("test-writer");

    expect(task.severity).toBe("low");
    expect(task.files).toEqual([]);
    expect(task.retryCount).toBe(0);
    expect(task.actor).toBeUndefined();
  });

  it("groups the index by status", async () => {
    const { exec } = fakeExec((args) => {
      if (args[0] === "list") return JSON.stringify([{ ref: "A" }, { ref: "B" }, { ref: "C" }]);
      const status = args[1] === "C" ? "canceled" : "todo";
      return detail(args[1], {
        task: { ref: args[1], title: args[1], status, priority: "low", created_at: "" },
      });
    });

    const index = await new AvenTaskManager(config, exec).getIndex();

    expect(index.titles).toEqual(["A", "B", "C"]);
    expect(index.byStatus.get("todo")?.map((t) => t.ref)).toEqual(["A", "B"]);
    expect(index.byStatus.get("canceled")?.map((t) => t.ref)).toEqual(["C"]);
  });

  it("returns no tasks when the project or label does not exist yet", async () => {
    const exec: AvenExec = async (args) => {
      throw new AvenError(args, "Error: error unknown-project input=app");
    };
    expect(await new AvenTaskManager(config, exec).listReady("coder")).toEqual([]);
  });

  it("propagates other aven errors", async () => {
    const exec: AvenExec = async (args) => {
      throw new AvenError(args, "Error: database locked");
    };
    await expect(new AvenTaskManager(config, exec).getIndex()).rejects.toThrow("database locked");
  });

  it("updates status and skips undefined metadata", async () => {
    const { exec, calls } = fakeExec(() => "");

    await new AvenTaskManager(config, exec).update("APP-0001", {
      status: "done",
      metadata: { branch: "vteam/x", "pr-url": undefined },
    });

    expect(calls[0].args).toEqual([
      "edit", "APP-0001", "--status", "done", "--metadata", "branch=vteam/x",
    ]);
  });

  it("prefixes every command with the configured workspace", async () => {
    const { exec, calls } = fakeExec(() => "");

    await new AvenTaskManager({ ...config, workspace: "work" }, exec).update("APP-0001", {
      status: "active",
    });

    expect(calls[0].args).toEqual(["--workspace", "work", "edit", "APP-0001", "--status", "active"]);
  });
});

function avenInstalled(): boolean {
  try {
    execFileSync("aven", ["--version"], { stdio: "ignore" });
    return true;
  } catch {
    return false;
  }
}

describe.skipIf(!avenInstalled())("AvenTaskManager against real aven", () => {
  let tmp: string;
  let manager: AvenTaskManager;

  beforeEach(() => {
    tmp = mkdtempSync(join(tmpdir(), "vteam-aven-test-"));
    const db = join(tmp, "aven.sqlite");
    manager = new AvenTaskManager(config, (args, stdin) => runAven(["--db", db, ...args], stdin));
  });

  afterEach(() => {
    rmSync(tmp, { recursive: true, force: true });
  });

  it("routes tasks by actor, sending unassigned ones to the default actor", async () => {
    const unassigned = await manager.create({ ...finding, title: "Unassigned" }, "improver");
    expect((await manager.listReady("coder")).map((t) => t.ref)).toEqual([unassigned]);
    expect(await manager.listReady("test-writer")).toEqual([]);

    const forTests = await manager.create({ ...finding, title: "Needs tests" }, "improver", "test-writer");
    expect((await manager.listReady("test-writer")).map((t) => t.ref)).toEqual([forTests]);
    expect((await manager.listReady("coder")).map((t) => t.ref)).toEqual([unassigned]);

    const forCoder = await manager.create({ ...finding, title: "Refactor" }, "improver", "coder");
    expect((await manager.listReady("coder")).map((t) => t.ref).sort()).toEqual(
      [unassigned, forCoder].sort(),
    );
  });

  it("round-trips a task through its lifecycle", async () => {
    expect(await manager.listReady("coder")).toEqual([]);

    const ref = await manager.create(finding, "improver");

    const [ready] = await manager.listReady("coder");
    expect(ready).toMatchObject({
      ref,
      title: "Null check missing",
      status: "todo",
      severity: "critical",
      files: ["src/a.ts:10", "src/b.ts"],
      retryCount: 0,
    });
    expect(ready.description).toContain("Crashes on empty input.");

    await manager.update(ref, { status: "active" });
    expect(await manager.listReady("coder")).toEqual([]);

    await manager.update(ref, { status: "todo", metadata: { "retry-count": "1" } });
    expect((await manager.listReady("coder"))[0].retryCount).toBe(1);

    await manager.update(ref, { status: "done", metadata: { branch: "vteam/fix" } });
    const index = await manager.getIndex();
    expect(index.byStatus.get("done")?.map((t) => t.ref)).toEqual([ref]);
  });
});
