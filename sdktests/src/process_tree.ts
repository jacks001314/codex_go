import { spawn } from "node:child_process";

export function isProcessAlive(pid: number): boolean {
  if (!Number.isInteger(pid) || pid <= 0) return false;
  try {
    process.kill(pid, 0);
    return true;
  } catch (error: any) {
    return error?.code === "EPERM";
  }
}

export function processTreeKillCommand(pid: number, platform = process.platform): { command: string; args: string[] } | null {
  if (!Number.isInteger(pid) || pid <= 0) return null;
  if (platform === "win32") {
    return { command: "taskkill", args: ["/PID", String(pid), "/T", "/F"] };
  }
  return null;
}

export async function terminateProcessTree(pid: number, platform = process.platform): Promise<void> {
  if (!Number.isInteger(pid) || pid <= 0 || pid === process.pid) return;
  const command = processTreeKillCommand(pid, platform);
  if (command) {
    await new Promise<void>((resolve) => {
      const killer = spawn(command.command, command.args, { windowsHide: true, stdio: "ignore" });
      killer.once("exit", () => resolve());
      killer.once("error", () => resolve());
    });
    return;
  }

  const descendants = await unixProcessDescendants(pid);
  for (const target of [...descendants.reverse(), pid]) {
    try {
      process.kill(-target, "SIGKILL");
    } catch {
      // The process may not be a process-group leader.
    }
    try {
      process.kill(target, "SIGKILL");
    } catch {
      // The process may already have exited.
    }
  }
}

export async function waitForProcessExit(pid: number, timeoutMs = 5000): Promise<boolean> {
  const deadline = Date.now() + timeoutMs;
  while (isProcessAlive(pid) && Date.now() < deadline) {
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  return !isProcessAlive(pid);
}

async function unixProcessDescendants(rootPID: number): Promise<number[]> {
  const output = await new Promise<string>((resolve) => {
    const child = spawn("ps", ["-eo", "pid=,ppid="], { stdio: ["ignore", "pipe", "ignore"] });
    let stdout = "";
    let settled = false;
    child.stdout?.on("data", (chunk) => { stdout += String(chunk); });
    const finish = () => {
      if (settled) return;
      settled = true;
      resolve(stdout);
    };
    child.once("close", finish);
    child.once("error", finish);
  });
  const children = new Map<number, number[]>();
  for (const line of output.split(/\r?\n/)) {
    const [pidText, parentText] = line.trim().split(/\s+/, 2);
    const childPID = Number(pidText);
    const parentPID = Number(parentText);
    if (!Number.isInteger(childPID) || !Number.isInteger(parentPID)) continue;
    const entries = children.get(parentPID) ?? [];
    entries.push(childPID);
    children.set(parentPID, entries);
  }
  const descendants: number[] = [];
  const visited = new Set<number>([rootPID]);
  const visit = (parentPID: number) => {
    for (const childPID of children.get(parentPID) ?? []) {
      if (visited.has(childPID)) continue;
      visited.add(childPID);
      descendants.push(childPID);
      visit(childPID);
    }
  };
  visit(rootPID);
  return descendants;
}
