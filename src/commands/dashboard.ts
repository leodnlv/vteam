import { setTimeout as sleep } from "node:timers/promises";
import { color } from "../ansi.js";
import { renderStatus, requireVteamDir } from "./status.js";

const REFRESH_MS = 1000;
const ESC = "\x1b";
const ENTER_SCREEN = `${ESC}[?1049h${ESC}[?25l${ESC}[?7l`;
const LEAVE_SCREEN = `${ESC}[?7h${ESC}[?25h${ESC}[?1049l`;
const CURSOR_HOME = `${ESC}[H`;
const CLEAR_LINE_END = `${ESC}[K`;
const CLEAR_SCREEN_END = `${ESC}[J`;

export async function dashboardCommand(): Promise<void> {
  const cwd = process.cwd();
  requireVteamDir(cwd);
  if (!process.stdout.isTTY) {
    console.error(color.red("vteam dashboard needs a terminal. Use 'vteam status' for a one-off snapshot."));
    process.exit(1);
  }

  process.stdout.write(ENTER_SCREEN);
  const leave = () => process.stdout.write(LEAVE_SCREEN);
  process.on("exit", leave);
  const quit = () => process.exit(0);
  process.on("SIGINT", quit);
  process.on("SIGTERM", quit);

  let body: string[] = [];
  for (;;) {
    const startedAt = Date.now();
    let error: string | undefined;
    try {
      body = await renderStatus(cwd);
    } catch (err) {
      error = err instanceof Error ? err.message : String(err);
    }
    const frame = composeFrame(body, footer(new Date(), error), process.stdout.rows);
    process.stdout.write(CURSOR_HOME + frame.map((line) => line + CLEAR_LINE_END).join("\n") + CLEAR_SCREEN_END);
    await sleep(Math.max(0, REFRESH_MS - (Date.now() - startedAt)));
  }
}

export function composeFrame(body: string[], footerLine: string, rows: number | undefined): string[] {
  const lines = body.flatMap((line) => line.split("\n"));
  const available = rows ? Math.max(1, rows - 2) : lines.length;
  if (lines.length > available) {
    const hidden = lines.length - available + 1;
    lines.splice(available - 1, lines.length, color.gray(`  … ${hidden} more line(s), enlarge the terminal`));
  }
  return [...lines, "", footerLine];
}

function footer(now: Date, error: string | undefined): string {
  const time = now.toLocaleTimeString([], { hourCycle: "h23" });
  const hint = color.gray(`updated ${time} · refreshes every ${REFRESH_MS / 1000}s · Ctrl+C to quit`);
  return error ? `${color.red(`✗ refresh failed: ${error.split("\n")[0]}`)}  ${hint}` : hint;
}
