import { randomUUID } from "node:crypto";
import { closeSync, existsSync, mkdirSync, openSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { isProcessAlive, terminateProcessTree, waitForProcessExit } from "./process_tree.ts";
import { sdktestsRoot } from "./util.ts";

export type LiveRunLockOwner = {
  pid: number;
  token: string;
  createdAt: string;
  cwd: string;
  argv: string[];
};

export type LiveRunLock = {
  path: string;
  owner: LiveRunLockOwner;
  release(): void;
};

export async function acquireLiveRunLock(options: {
  lockPath?: string;
  recover?: boolean;
} = {}): Promise<LiveRunLock> {
  const lockPath = path.resolve(options.lockPath ?? path.join(sdktestsRoot, ".tmp", "live-parity.lock"));
  mkdirSync(path.dirname(lockPath), { recursive: true });
  for (let attempt = 0; attempt < 3; attempt += 1) {
    const owner: LiveRunLockOwner = {
      pid: process.pid,
      token: randomUUID(),
      createdAt: new Date().toISOString(),
      cwd: process.cwd(),
      argv: process.argv.slice(1),
    };
    try {
      const fd = openSync(lockPath, "wx", 0o600);
      try {
        writeFileSync(fd, `${JSON.stringify(owner, null, 2)}\n`, "utf8");
      } finally {
        closeSync(fd);
      }
      return {
        path: lockPath,
        owner,
        release: () => releaseLiveRunLock(lockPath, owner.token),
      };
    } catch (error: any) {
      if (error?.code !== "EEXIST") throw error;
    }

    const existing = await readExistingLockOwner(lockPath);
    if (!existing) {
      if (!existsSync(lockPath)) continue;
      throw new Error(
        `live sdktests lock ${lockPath} has unreadable owner metadata; ` +
        "refusing to remove it without a verifiable process owner",
      );
    }
    if (!isProcessAlive(existing.pid)) {
      rmSync(lockPath, { force: true });
      continue;
    }
    if (!options.recover) {
      throw new Error(
        `another live sdktests runner owns ${lockPath} (pid ${existing.pid}, started ${existing.createdAt}); ` +
        `wait for it to finish or pass --recover-lock to terminate that exact process tree`,
      );
    }
    await terminateProcessTree(existing.pid);
    if (!await waitForProcessExit(existing.pid)) {
      throw new Error(`failed to recover live sdktests lock: process ${existing.pid} is still running`);
    }
    rmSync(lockPath, { force: true });
  }
  throw new Error(`failed to acquire live sdktests lock ${lockPath}`);
}

export function readLockOwner(lockPath: string): LiveRunLockOwner | null {
  if (!existsSync(lockPath)) return null;
  try {
    const value = JSON.parse(readFileSync(lockPath, "utf8"));
    return Number.isInteger(value?.pid) && typeof value?.token === "string" ? value : null;
  } catch {
    return null;
  }
}

function releaseLiveRunLock(lockPath: string, token: string): void {
  const owner = readLockOwner(lockPath);
  if (owner?.token === token) rmSync(lockPath, { force: true });
}

async function readExistingLockOwner(lockPath: string): Promise<LiveRunLockOwner | null> {
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const owner = readLockOwner(lockPath);
    if (owner || !existsSync(lockPath)) return owner;
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  return null;
}
