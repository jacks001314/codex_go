import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import { existsSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { acquireLiveRunLock } from "./live_lock.ts";
import { isProcessAlive, processTreeKillCommand, terminateProcessTree, waitForProcessExit } from "./process_tree.ts";

test("live run lock rejects an active owner and releases by token", async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-lock-"));
  const lockPath = path.join(root, "live.lock");
  try {
    const lock = await acquireLiveRunLock({ lockPath });
    await assert.rejects(acquireLiveRunLock({ lockPath }), /another live sdktests runner owns/);
    lock.release();
    assert.equal(existsSync(lockPath), false);
    const next = await acquireLiveRunLock({ lockPath });
    next.release();
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("live run lock replaces a stale owner", async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-lock-"));
  const lockPath = path.join(root, "live.lock");
  try {
    writeFileSync(lockPath, JSON.stringify({
      pid: 2147483000, token: "stale", createdAt: new Date(0).toISOString(), cwd: root, argv: [],
    }));
    const lock = await acquireLiveRunLock({ lockPath });
    assert.equal(lock.owner.pid, process.pid);
    lock.release();
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("live run lock preserves unreadable ownership metadata", async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-lock-"));
  const lockPath = path.join(root, "live.lock");
  try {
    writeFileSync(lockPath, "not-json");
    await assert.rejects(acquireLiveRunLock({ lockPath, recover: true }), /unreadable owner metadata/);
    assert.equal(existsSync(lockPath), true);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("explicit lock recovery terminates the exact owner process", async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-lock-"));
  const lockPath = path.join(root, "live.lock");
  const child = spawn(process.execPath, ["-e", "setInterval(() => {}, 1000)"], { windowsHide: true });
  await once(child, "spawn");
  try {
    writeFileSync(lockPath, JSON.stringify({
      pid: child.pid, token: "recover", createdAt: new Date().toISOString(), cwd: root, argv: [],
    }));
    const lock = await acquireLiveRunLock({ lockPath, recover: true });
    assert.equal(await waitForProcessExit(child.pid ?? 0), true);
    lock.release();
  } finally {
    await terminateProcessTree(child.pid ?? 0);
    rmSync(root, { recursive: true, force: true });
  }
});

test("terminateProcessTree removes a nested process tree", async () => {
  const script = [
    "const {spawn}=require('node:child_process')",
    "const child=spawn(process.execPath,['-e','setInterval(() => {}, 1000)'],{windowsHide:true,stdio:'ignore'})",
    "console.log(child.pid)",
    "setInterval(() => {}, 1000)",
  ].join(";");
  const parent = spawn(process.execPath, ["-e", script], {
    detached: process.platform !== "win32", windowsHide: true, stdio: ["ignore", "pipe", "ignore"],
  });
  const [chunk] = await once(parent.stdout!, "data");
  const childPID = Number(String(chunk).trim());
  assert.equal(isProcessAlive(parent.pid ?? 0), true);
  assert.equal(isProcessAlive(childPID), true);
  try {
    await terminateProcessTree(parent.pid ?? 0);
    assert.equal(await waitForProcessExit(parent.pid ?? 0), true);
    assert.equal(await waitForProcessExit(childPID), true);
  } finally {
    await terminateProcessTree(childPID);
    await terminateProcessTree(parent.pid ?? 0);
  }
});

test("Windows process-tree cleanup uses taskkill tree mode", () => {
  assert.deepEqual(processTreeKillCommand(42, "win32"), {
    command: "taskkill", args: ["/PID", "42", "/T", "/F"],
  });
  assert.equal(processTreeKillCommand(42, "linux"), null);
});
