import { hostname } from "node:os";
import { isProcessAlive } from "../memory/lock.js";
import type { TaskManager } from "./task-manager.js";
import type { Task, TaskMetadata } from "../types.js";

export const CLAIM_KEYS = ["claimed-by", "claimed-pid", "claimed-host"] as const satisfies (keyof TaskMetadata)[];

export function claimMetadata(runId: string): TaskMetadata {
  return {
    "claimed-by": runId,
    "claimed-pid": String(process.pid),
    "claimed-host": hostname(),
  };
}

export interface OrphanSweep {
  released: Task[];
  foreignHost: Task[];
  unclaimed: Task[];
}

export async function releaseOrphanedTasks(
  taskManager: TaskManager,
  isAlive: (pid: number) => boolean = isProcessAlive,
  host: string = hostname(),
): Promise<OrphanSweep> {
  const { byStatus } = await taskManager.getIndex();
  const sweep: OrphanSweep = { released: [], foreignHost: [], unclaimed: [] };

  for (const task of byStatus.get("active") ?? []) {
    const pid = Number.parseInt(task.metadata["claimed-pid"] ?? "", 10);
    if (!pid) {
      sweep.unclaimed.push(task);
    } else if (task.metadata["claimed-host"] !== host) {
      sweep.foreignHost.push(task);
    } else if (!isAlive(pid)) {
      await taskManager.update(task.ref, { status: "todo", removeMetadata: [...CLAIM_KEYS] });
      sweep.released.push(task);
    }
  }

  return sweep;
}
