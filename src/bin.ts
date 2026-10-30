#!/usr/bin/env node

import { Command } from "commander";
import { initCommand } from "./commands/init.js";
import { runCommand } from "./commands/run.js";
import { statusCommand } from "./commands/status.js";
import { cleanCommand } from "./commands/clean.js";
import { ensureMergeCommand } from "./commands/ensure-merge.js";
import { loopStartCommand, loopStatusCommand } from "./commands/loop.js";
import { skillInstallCommand, skillUninstallCommand } from "./commands/skill.js";

const program = new Command();

program
  .name("vteam")
  .description("Virtual development team framework powered by Claude Code")
  .version("0.1.0");

program
  .command("init")
  .description("Scaffold vteam/ directory with default agents and task folders")
  .action(initCommand);

program
  .command("run")
  .description("Run a specific agent")
  .argument("[agent]", "Agent to run (omit to list available agents)")
  .option("--focus <text>", "Priority context injected into the agent prompt")
  .action(runCommand);

program
  .command("status")
  .description("Show task board status")
  .action(statusCommand);

program
  .command("clean")
  .description("Remove orphaned worktrees, break stale locks")
  .action(cleanCommand);

program
  .command("ensure-merge")
  .description("Record merged PRs, cancel tasks with closed PRs, and reopen tasks whose PRs need changes or conflict")
  .action(ensureMergeCommand);

const loopCmd = program
  .command("loop")
  .description("Run agents on their cron schedules in a long-lived process");

loopCmd
  .command("start")
  .description("Start the scheduler (runs in foreground)")
  .action(loopStartCommand);

loopCmd
  .command("status")
  .description("Show agents with cron schedules and next fire times")
  .action(loopStatusCommand);

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
