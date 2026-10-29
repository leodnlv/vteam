import { closeSync, mkdirSync, openSync, writeSync } from "node:fs";
import { resolve } from "node:path";

export interface RunLog {
  path: string;
  write(chunk: string | Uint8Array): void;
  close(): void;
}

export function formatLogFilename(date: Date): string {
  return date.toISOString().replace(/[:.]/g, "-") + ".log";
}

export function agentLogsDir(cwd: string, agentName: string): string {
  return resolve(cwd, "vteam", ".logs", agentName);
}

export function startRunLog(cwd: string, agentName: string, now = new Date()): RunLog {
  const dir = agentLogsDir(cwd, agentName);
  mkdirSync(dir, { recursive: true });
  const path = resolve(dir, formatLogFilename(now));
  const fd = openSync(path, "a");

  const write = (chunk: string | Uint8Array) => {
    try {
      if (typeof chunk === "string") writeSync(fd, chunk);
      else writeSync(fd, chunk);
    } catch {
      // a failing log file must never break the run itself
    }
  };

  const restoreStdout = tee(process.stdout, write);
  const restoreStderr = tee(process.stderr, write);

  return {
    path,
    write,
    close() {
      restoreStdout();
      restoreStderr();
      closeSync(fd);
    },
  };
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
