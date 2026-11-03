import { closeSync, mkdirSync, openSync, writeSync } from "node:fs";
import { dirname, resolve } from "node:path";
import { stripAnsi } from "./ansi.js";

export interface RunLog {
  path: string;
  write(chunk: string | Uint8Array): void;
  close(): void;
}

let activeLog: RunLog | undefined;
let teeSuspended = false;

export function formatLogFilename(date: Date): string {
  return date.toISOString().replace(/[:.]/g, "-") + ".log";
}

export function agentLogsDir(cwd: string, agentName: string): string {
  return resolve(cwd, "vteam", ".logs", agentName);
}

export const RUN_LOG_ENV = "VTEAM_RUN_LOG";

export function runLogPath(cwd: string, agentName: string, now = new Date()): string {
  return resolve(agentLogsDir(cwd, agentName), formatLogFilename(now));
}

export function startRunLog(cwd: string, agentName: string, now = new Date()): RunLog {
  const path = process.env[RUN_LOG_ENV] || runLogPath(cwd, agentName, now);
  mkdirSync(dirname(path), { recursive: true });
  const fd = openSync(path, "a");

  const write = (chunk: string | Uint8Array) => {
    try {
      writeSync(fd, stripAnsi(typeof chunk === "string" ? chunk : Buffer.from(chunk).toString("utf-8")));
    } catch {
      // a failing log file must never break the run itself
    }
  };

  const teeSink = (chunk: string | Uint8Array) => {
    if (!teeSuspended) write(chunk);
  };
  const restoreStdout = tee(process.stdout, teeSink);
  const restoreStderr = tee(process.stderr, teeSink);

  const log: RunLog = {
    path,
    write,
    close() {
      restoreStdout();
      restoreStderr();
      closeSync(fd);
      if (activeLog === log) activeLog = undefined;
    },
  };
  activeLog = log;
  return log;
}

export function writeSplit(terminalLine: string, fileLine: string): void {
  if (!activeLog) {
    console.log(terminalLine);
    return;
  }
  teeSuspended = true;
  try {
    console.log(terminalLine);
  } finally {
    teeSuspended = false;
  }
  activeLog.write(`${fileLine}\n`);
}

function tee(stream: NodeJS.WriteStream, sink: (chunk: string | Uint8Array) => void): () => void {
  const original = stream.write;
  stream.write = ((chunk: string | Uint8Array, ...rest: unknown[]) => {
    sink(chunk);
    return Reflect.apply(original, stream, [chunk, ...rest]) as boolean;
  }) as typeof stream.write;
  return () => {
    stream.write = original;
  };
}
