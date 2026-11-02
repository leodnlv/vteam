#!/usr/bin/env node

import { readFileSync } from "node:fs";
import { Command } from "commander";
import { initCommand } from "./commands/init.js";
import { runCommand } from "./commands/run.js";
import { statusCommand } from "./commands/status.js";
import { dashboardCommand } from "./commands/dashboard.js";
import { cleanCommand } from "./commands/clean.js";
import { ensureMergeCommand } from "./commands/ensure-merge.js";
import { cronStartCommand, cronStatusCommand } from "./commands/cron.js";
import { skillInstallCommand, skillUninstallCommand } from "./commands/skill.js";

const { version } = JSON.parse(
  readFileSync(new URL("../package.json", import.meta.url), "utf-8"),
) as { version: string };

const program = new Command();

program
  .name("vteam")
  .description("Virtual development team framework powered by Claude Code")
  .version(version);

program
  .command("init")
  .description("Scaffold vteam/ directory with default agents and config")
  .action(initCommand);

program
  .command("run")
  .description("Run a specific agent")
  .argument("[agent]", "Agent to run (omit to list available agents)")
  .option("--focus <text>", "Priority context injected into the agent prompt")
  .action(runCommand);

program
  .command("status")
  .description("Show running agents, upcoming runs, and tasks that need attention")
  .action(statusCommand);

program
  .command("dashboard")
  .description("Show 'vteam status' full-screen, refreshed every second")
  .action(dashboardCommand);

program
  .command("clean")
  .description("Remove worktrees, break stale locks, and return orphaned active tasks to todo")
  .action(cleanCommand);

program
  .command("ensure-merge")
  .description("Record merged PRs, cancel tasks with closed PRs, and reopen tasks whose PRs need changes or conflict")
  .action(ensureMergeCommand);

const cronCmd = program
  .command("cron")
  .description("Run agents on their cron schedules in a long-lived process");

cronCmd
  .command("start")
  .description("Start the scheduler (runs in foreground)")
  .action(cronStartCommand);

cronCmd
  .command("status")
  .description("Show whether the scheduler is running and the next fire time of each job")
  .action(cronStatusCommand);

const skillCmd = program
  .command("skill")
  .description("Manage Claude Code skills from this project");

skillCmd
  .command("install")
  .description("Symlink skills from skills/ into ~/.claude/skills/")
  .action(skillInstallCommand);

skillCmd
  .command("uninstall")
  .description("Remove skill symlinks from ~/.claude/skills/")
  .action(skillUninstallCommand);

program.parse();
