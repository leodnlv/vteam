import { existsSync, mkdirSync, readFileSync, writeFileSync, appendFileSync } from "node:fs";
import { resolve, dirname, basename } from "node:path";
import { fileURLToPath } from "node:url";

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

  const dirs = [
    resolve(vteamDir, "agents", "code-reviewer"),
    resolve(vteamDir, "agents", "refactorer"),
    resolve(vteamDir, "agents", "review-responder"),
    resolve(vteamDir, "agents", "test-writer"),
  ];

  for (const dir of dirs) {
    mkdirSync(dir, { recursive: true });
  }

  copyTemplate("code-reviewer.agent.md", resolve(vteamDir, "agents", "code-reviewer", "AGENT.md"));
  copyTemplate("refactorer.agent.md", resolve(vteamDir, "agents", "refactorer", "AGENT.md"));
  copyTemplate("review-responder.agent.md", resolve(vteamDir, "agents", "review-responder", "AGENT.md"));
  copyTemplate("test-writer.agent.md", resolve(vteamDir, "agents", "test-writer", "AGENT.md"));

  copyTemplate("code-reviewer.memory.md", resolve(vteamDir, "agents", "code-reviewer", "MEMORY.md"));
  copyTemplate("refactorer.memory.md", resolve(vteamDir, "agents", "refactorer", "MEMORY.md"));
  copyTemplate("review-responder.memory.md", resolve(vteamDir, "agents", "review-responder", "MEMORY.md"));
  copyTemplate("test-writer.memory.md", resolve(vteamDir, "agents", "test-writer", "MEMORY.md"));

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

  console.log("  Created vteam/agents/code-reviewer/AGENT.md");
  console.log("  Created vteam/agents/refactorer/AGENT.md");
  console.log("  Created vteam/agents/review-responder/AGENT.md");
  console.log("  Created vteam/agents/test-writer/AGENT.md");
  console.log("  Created MEMORY.md for each agent");
  console.log(`  Created vteam/vteam.config.json (aven project: ${config.tasks.project})`);
  console.log("\nDone. Edit the AGENT.md and MEMORY.md files and vteam.config.json to customize.");
}
