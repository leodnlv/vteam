import { existsSync, mkdirSync, readFileSync, writeFileSync, appendFileSync } from "node:fs";
import { resolve, dirname, basename } from "node:path";
import { fileURLToPath } from "node:url";
import { agentConfigJsonSchema } from "../config/schema.js";

const __filename = fileURLToPath(import.meta.url);
const __dirname = dirname(__filename);

function templateDir(): string {
  // Works both in src/ (dev via tsx) and dist/ (production)
  // src/commands/init.ts → src/templates/
  // dist/commands/init.js → dist/templates/
  return resolve(__dirname, "..", "templates");
}

function copyTemplate(templateName: string, destPath: string): void {
  const src = resolve(templateDir(), templateName);
  const content = readFileSync(src, "utf-8");
  writeFileSync(destPath, content, "utf-8");
}

export async function initCommand(): Promise<void> {
  const cwd = process.cwd();
  const vteamDir = resolve(cwd, "vteam");

  if (existsSync(vteamDir)) {
    console.error("vteam/ directory already exists. Aborting.");
    process.exit(1);
  }

  console.log("Scaffolding vteam/ directory...");

  const agents = ["improver", "coder", "test-writer"];
  for (const agent of agents) {
    const agentDir = resolve(vteamDir, "agents", agent);
    mkdirSync(agentDir, { recursive: true });
    copyTemplate(`${agent}.agent.json`, resolve(agentDir, "agent.json"));
    copyTemplate(`${agent}.agent.md`, resolve(agentDir, "AGENT.md"));
    copyTemplate(`${agent}.memory.md`, resolve(agentDir, "MEMORY.md"));
  }

  writeFileSync(
    resolve(vteamDir, "agent.schema.json"),
    `${JSON.stringify(agentConfigJsonSchema(), null, 2)}\n`,
    "utf-8",
  );

  const config = JSON.parse(readFileSync(resolve(templateDir(), "vteam.config.json"), "utf-8"));
  config.tasks.project = basename(cwd);
  writeFileSync(resolve(vteamDir, "vteam.config.json"), `${JSON.stringify(config, null, 2)}\n`, "utf-8");

  const gitignorePath = resolve(cwd, ".gitignore");
  const entries = [".vteam-worktrees/", "vteam/.locks/", "vteam/.logs/", "vteam/.runs/", "vteam/.memory/"];
  if (existsSync(gitignorePath)) {
    const content = readFileSync(gitignorePath, "utf-8");
    const missing = entries.filter((e) => !content.includes(e));
    if (missing.length > 0) {
      appendFileSync(gitignorePath, `\n${missing.join("\n")}\n`);
      console.log(`  Updated .gitignore with ${missing.join(", ")}`);
    }
  } else {
    writeFileSync(gitignorePath, `${entries.join("\n")}\n`);
    console.log("  Created .gitignore");
  }

  for (const agent of agents) {
    console.log(`  Created vteam/agents/${agent}/ (agent.json, AGENT.md, MEMORY.md)`);
  }
  console.log("  Created vteam/agent.schema.json");
  console.log(`  Created vteam/vteam.config.json (aven project: ${config.tasks.project})`);
  console.log("\nDone. Edit each agent's agent.json, AGENT.md, and MEMORY.md, and vteam.config.json, to customize.");
}
