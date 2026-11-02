import { color, stripAnsi } from "./ansi.js";
import { writeSplit } from "./run-log.js";

const DEFAULT_WIDTH = 72;
const LABEL_WIDTH = 10;

let clockStart = Date.now();

export function startClock(now = Date.now()): void {
  clockStart = now;
}

export function formatClock(ms: number): string {
  const totalSeconds = Math.max(0, Math.floor(ms / 1000));
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  const pad = (n: number) => String(n).padStart(2, "0");
  return hours > 0 ? `${hours}:${pad(minutes)}:${pad(seconds)}` : `${pad(minutes)}:${pad(seconds)}`;
}

export function formatDuration(ms: number): string {
  const totalSeconds = Math.round(ms / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) return `${hours}h${String(minutes).padStart(2, "0")}m`;
  if (minutes > 0) return `${minutes}m${String(seconds).padStart(2, "0")}s`;
  return `${seconds}s`;
}

export function truncate(text: string, width: number): string {
  if (width <= 1) return "…";
  return text.length > width ? `${text.slice(0, width - 1)}…` : text;
}

function terminalWidth(): number | undefined {
  return process.stdout.isTTY ? process.stdout.columns : undefined;
}

function prefix(glyph: string): string {
  return ` ${color.gray(formatClock(Date.now() - clockStart))}  ${glyph} `;
}

function indentContinuation(text: string, indent: number): string {
  const pad = " ".repeat(indent);
  return text
    .split("\n")
    .map((line, i) => (i === 0 || line === "" ? line : pad + line))
    .join("\n");
}

function render(glyph: string, text: string, paintText: (s: string) => string = (s) => s): string {
  const head = prefix(glyph);
  return head + paintText(indentContinuation(text, stripAnsi(head).length));
}

function out(glyph: string, text: string, paintText?: (s: string) => string): void {
  console.log(render(glyph, text, paintText));
}

function err(glyph: string, text: string, paintText?: (s: string) => string): void {
  console.error(render(glyph, text, paintText));
}

export function banner(agentName: string, logPath: string): void {
  console.log(`${color.bold(color.cyan("vteam ▸"))} ${color.bold(agentName)}   ${color.gray(`log: ${logPath}`)}`);
}

export function section(title: string): void {
  const width = Math.min(terminalWidth() ?? DEFAULT_WIDTH, DEFAULT_WIDTH);
  const head = `━━ ${title} `;
  console.log(`\n${color.bold(color.cyan(head + "━".repeat(Math.max(3, width - head.length))))}`);
}

export function step(text: string): void {
  out(color.blue("•"), text);
}

export function ok(text: string): void {
  out(color.green("✓"), text);
}

export function warn(text: string): void {
  err(color.yellow("!"), text, color.yellow);
}

export function fail(text: string): void {
  err(color.red("✗"), text, color.red);
}

export function field(label: string, value: string): void {
  const indent = stripAnsi(prefix(" ")).length;
  console.log(`${" ".repeat(indent)}${color.gray(label.padEnd(LABEL_WIDTH))}${value}`);
}

export function claudeText(text: string): void {
  out(color.magenta("❯"), text.trim());
}

export function toolCall(name: string, detail: string): void {
  const head = `${prefix(color.yellow("⚙"))}${color.bold(color.yellow(name.padEnd(6)))} `;
  if (!detail) {
    console.log(head.trimEnd());
    return;
  }

  const fileLine = head + indentContinuation(detail, stripAnsi(head).length);
  const width = terminalWidth();
  if (width === undefined) {
    console.log(fileLine);
    return;
  }

  const lines = detail.split("\n");
  const oneLine = lines.length > 1 ? `${lines[0]} …` : lines[0];
  writeSplit(head + color.dim(truncate(oneLine, width - stripAnsi(head).length)), fileLine);
}

export function toolError(message: string): void {
  const firstLine = message.trim().split("\n")[0] ?? "";
  const indent = stripAnsi(prefix(" ")).length;
  console.log(`${" ".repeat(indent)}${color.red(`✗ ${truncate(firstLine, 200)}`)}`);
}

export interface ExitSummary {
  exitCode: number;
  durationMs?: number;
  turns?: number;
  costUsd?: number;
}

export function agentExit(summary: ExitSummary): void {
  const parts = [`claude exited ${summary.exitCode}`];
  if (summary.durationMs !== undefined) parts.push(formatDuration(summary.durationMs));
  if (summary.turns !== undefined) parts.push(`${summary.turns} turns`);
  if (summary.costUsd !== undefined) parts.push(`$${summary.costUsd.toFixed(2)}`);
  const text = parts.join(" · ");
  console.log("");
  if (summary.exitCode === 0) ok(text);
  else fail(text);
}

export function finished(agentName: string, failure?: string): void {
  const elapsed = formatDuration(Date.now() - clockStart);
  if (failure) {
    console.error(`\n${color.bold(color.red(`✗ ${agentName} failed after ${elapsed}:`))} ${color.red(failure)}`);
  } else {
    console.log(`\n${color.bold(color.green(`✓ ${agentName} completed in ${elapsed}`))}`);
  }
}
