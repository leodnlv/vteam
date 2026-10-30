import { spawn, type ChildProcess } from "node:child_process";
import { Cron } from "croner";
import { resolveAgentConfig, listAgentNames } from "../config/agent.js";
import { loadConfig } from "../config/load.js";
import { MERGE_ENSURER } from "./ensure-merge.js";
import { agentLogsDir } from "../run-log.js";
import type { AgentConfig, VteamConfig } from "../types.js";

export interface ScheduledJob {
  name: string;
  cron: string;
  command: string[];
}

export async function loopStartCommand(): Promise<void> {
  const cwd = process.cwd();
  const scheduled = findScheduledJobs(cwd, loadConfig(cwd));

  if (scheduled.length === 0) {
    console.log("No agents with cron patterns found.");
    return;
  }

  const jobs: Cron[] = [];
  const running = new Map<string, ChildProcess>();

  console.log(`Starting loop with ${scheduled.length} job(s):\n`);

  for (const scheduledJob of scheduled) {
    const job = new Cron(scheduledJob.cron, () => {
      if (running.has(scheduledJob.name)) {
        console.log(`[${ts()}] ${scheduledJob.name} skipped (still running)`);
        return;
      }
      spawnRun(cwd, scheduledJob, running);
    });

    jobs.push(job);

    const next = job.nextRun();
    console.log(
      `  ${scheduledJob.name}  ${scheduledJob.cron}  next: ${next?.toISOString() ?? "—"}`,
    );
  }

  console.log("\nLoop running. Press Ctrl+C to stop.");

  const shutdown = () => {
    console.log("\nStopping...");
    for (const job of jobs) job.stop();
    for (const [name, child] of running) {
      child.kill("SIGTERM");
      console.log(`  killed ${name}`);
    }
    process.exit(0);
  };

  process.on("SIGINT", shutdown);
  process.on("SIGTERM", shutdown);
}

export async function loopStatusCommand(): Promise<void> {
  const cwd = process.cwd();
  const scheduled = findScheduledJobs(cwd, loadConfig(cwd));

  if (scheduled.length === 0) {
    console.log("No agents with cron patterns found.");
    return;
  }

  console.log(`${scheduled.length} job(s) with cron schedules:\n`);

  for (const scheduledJob of scheduled) {
    const job = new Cron(scheduledJob.cron);
    const next = job.nextRun();
    job.stop();
    console.log(
      `  ${scheduledJob.name}  ${scheduledJob.cron}  next: ${next?.toISOString() ?? "—"}`,
    );
  }
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
  const child = spawn(command, args, { cwd, stdio: "ignore" });

  running.set(job.name, child);
  console.log(`[${ts()}] ${job.name} started → ${agentLogsDir(cwd, job.name)}/`);

  child.on("close", (code) => {
    running.delete(job.name);
    console.log(`[${ts()}] ${job.name} finished (exit ${code})`);
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

function ts(): string {
  return new Date().toISOString();
}
