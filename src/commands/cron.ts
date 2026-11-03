import { spawn, type ChildProcess } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import { dirname, relative, resolve } from "node:path";
import { Cron } from "croner";
import { isProcessAlive } from "../memory/lock.js";
import { resolveAgentConfig, listAgentNames } from "../config/agent.js";
import { loadConfig } from "../config/load.js";
import { MERGE_ENSURER } from "./ensure-merge.js";
import { RUN_LOG_ENV, runLogPath } from "../run-log.js";
import type { AgentConfig, VteamConfig } from "../types.js";
import { color } from "../ansi.js";
import * as log from "../log.js";

export interface ScheduledJob {
  name: string;
  cron: string;
  command: string[];
}

export function schedulerPidPath(cwd: string): string {
  return resolve(cwd, "vteam", ".locks", "cron.pid");
}

export function readSchedulerPid(cwd: string): number | undefined {
  const path = schedulerPidPath(cwd);
  if (!existsSync(path)) return undefined;
  const pid = Number.parseInt(readFileSync(path, "utf-8"), 10);
  return pid && isProcessAlive(pid) ? pid : undefined;
}

export function nextRunOf(cron: string): Date | null {
  const job = new Cron(cron);
  const next = job.nextRun();
  job.stop();
  return next;
}

export async function cronStartCommand(): Promise<void> {
  const cwd = process.cwd();
  const config = loadConfig(cwd);
  const scheduled = findScheduledJobs(cwd, config);
  log.useWallClock();
  log.banner("cron start", `pid ${process.pid}`);

  if (scheduled.length === 0) {
    console.log("");
    log.warn("No agents with cron patterns found.");
    return;
  }

  const runningPid = readSchedulerPid(cwd);
  if (runningPid) {
    console.log("");
    log.fail(`vteam cron is already running (pid ${runningPid}).`);
    process.exit(1);
  }
  const pidPath = schedulerPidPath(cwd);
  mkdirSync(dirname(pidPath), { recursive: true });
  writeFileSync(pidPath, String(process.pid));

  const jobs: Cron[] = [];
  const running = new Map<string, ChildProcess>();

  for (const scheduledJob of scheduled) {
    jobs.push(
      new Cron(scheduledJob.cron, () => {
        if (running.has(scheduledJob.name)) {
          log.warn(`${scheduledJob.name} skipped (still running)`);
          return;
        }
        spawnRun(cwd, scheduledJob, running);
      }),
    );
  }

  console.log(renderScheduler(cwd, config).join("\n"));
  log.section("Activity");
  log.step(`waiting for jobs ${color.gray("(Ctrl+C to stop)")}`);

  const shutdown = () => {
    console.log("");
    log.step("stopping scheduler");
    for (const job of jobs) job.stop();
    for (const [name, child] of running) {
      child.kill("SIGTERM");
      log.warn(`killed ${name}`);
    }
    rmSync(pidPath, { force: true });
    log.ok("scheduler stopped");
    process.exit(0);
  };

  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);
}

export async function cronStatusCommand(): Promise<void> {
  const cwd = process.cwd();
  console.log([log.formatBanner("cron status"), ...renderScheduler(cwd, loadConfig(cwd)), ""].join("\n"));
}

export function renderScheduler(cwd: string, config: VteamConfig): string[] {
  const lines = [log.formatSection("Scheduler")];
  const jobs = findScheduledJobs(cwd, config);
  if (jobs.length === 0) {
    lines.push(log.formatListItem(color.gray("·"), color.dim("no agents have a cron schedule")));
    return lines;
  }
  const pid = readSchedulerPid(cwd);
  lines.push(
    pid
      ? log.formatListItem(color.green("✓"), `running ${color.gray(`(pid ${pid})`)}`)
      : log.formatListItem(color.yellow("!"), color.yellow("not running (start it with 'vteam cron start')")),
  );

  const nameWidth = Math.max(...jobs.map((j) => j.name.length));
  const cronWidth = Math.max(...jobs.map((j) => j.cron.length));
  for (const job of jobs) {
    const next = nextRunOf(job.cron);
    const when = next
      ? `next ${log.formatTimeOfDay(next)} ${color.gray(`(in ${log.formatAge(next.getTime() - Date.now())})`)}`
      : color.dim("no next run");
    lines.push(
      log.formatListItem(
        color.blue("•"),
        `${color.bold(job.name.padEnd(nameWidth))}  ${color.gray(job.cron.padEnd(cronWidth))}  ${when}`,
      ),
    );
  }
  return lines;
}

function spawnRun(
  cwd: string,
  job: ScheduledJob,
  running: Map<string, ChildProcess>,
): void {
  const { command, args } = buildChildArgs(
    process.execPath,
    process.execArgv,
    process.argv[1],
    job.command,
  );
  const logPath = relative(cwd, runLogPath(cwd, job.name));
  const startedAt = Date.now();
  const child = spawn(command, args, {
    cwd,
    stdio: "ignore",
    env: { ...process.env, [RUN_LOG_ENV]: resolve(cwd, logPath) },
  });

  running.set(job.name, child);
  log.step(`${color.bold(job.name)} started`);
  log.field("log", logPath);

  child.on("error", (err) => {
    running.delete(job.name);
    log.fail(`${job.name} could not start: ${err.message}`);
  });

  child.on("close", (code, signal) => {
    running.delete(job.name);
    const elapsed = log.formatDuration(Date.now() - startedAt);
    if (code === 0) {
      log.ok(`${color.bold(job.name)} finished in ${elapsed}`);
      return;
    }
    log.fail(`${job.name} ${signal ? `killed by ${signal}` : `exited ${code}`} after ${elapsed}`);
    log.field("log", logPath);
  });
}

export function buildChildArgs(
  execPath: string,
  execArgv: string[],
  scriptPath: string,
  command: string[],
): { command: string; args: string[] } {
  return {
    command: execPath,
    args: [...execArgv, scriptPath, ...command],
  };
}

export function findScheduledJobs(cwd: string, config: VteamConfig): ScheduledJob[] {
  const jobs: ScheduledJob[] = findCronAgents(cwd).flatMap((agent) =>
    agent.cron ? [{ name: agent.name, cron: agent.cron, command: ["run", agent.name] }] : [],
  );
  if (config.mergeEnsurer.cron) {
    jobs.push({ name: MERGE_ENSURER, cron: config.mergeEnsurer.cron, command: ["ensure-merge"] });
  }
  return jobs;
}

export function findCronAgents(cwd: string): AgentConfig[] {
  const names = listAgentNames(cwd);
  const agents: AgentConfig[] = [];

  for (const name of names) {
    try {
      const agent = resolveAgentConfig(name, cwd);
      if (agent.cron) agents.push(agent);
    } catch {
      // skip agents with invalid config
    }
  }

  return agents;
}
