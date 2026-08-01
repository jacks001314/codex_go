import assert from "node:assert/strict";
import { spawn } from "node:child_process";
import { once } from "node:events";
import {
  existsSync,
  mkdirSync,
  mkdtempSync,
  readFileSync,
  readdirSync,
  rmSync,
  writeFileSync,
} from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { pathToFileURL } from "node:url";
import { infraRetryDelay, runCLI, type RunCLIOptions, type RunProcessCLIOptions } from "./cli.ts";
import { HarnessAbortError } from "./runner.ts";
import { isProcessAlive, waitForProcessExit } from "./process_tree.ts";
import { readLockOwner } from "./live_lock.ts";
import { sdktestsRoot } from "./util.ts";

type FakeProcessTree = {
  workerPid: number;
  childPid: number;
  grandchildPid: number;
};

const fakeSDKSource = String.raw`
import { spawn } from "node:child_process";
import { readFileSync } from "node:fs";

class FakeThread {
  constructor(client, id) {
    this.client = client;
    this.id = id;
  }

  async runStreamed() {
    const mode = readFileSync(process.env.SDKTESTS_FAKE_MODE_FILE, "utf8").trim();
    if (mode === "block") {
      const childCode = [
        "const { spawn } = require('node:child_process')",
        "const { writeFileSync } = require('node:fs')",
        "const grandchild = spawn(process.execPath, ['-e', 'setInterval(() => {}, 1000)'], { windowsHide: true, stdio: 'ignore' })",
        "writeFileSync(process.argv[1], JSON.stringify({ workerPid: Number(process.argv[2]), childPid: process.pid, grandchildPid: grandchild.pid }))",
        "setInterval(() => {}, 1000)",
      ].join(";");
      spawn(process.execPath, ["-e", childCode, process.env.SDKTESTS_FAKE_PID_FILE, String(process.pid)], {
        windowsHide: true,
        stdio: "ignore",
      });
      await new Promise(() => {});
    }

    const index = this.client.turnIndex++;
    const messages = ["TURN1_OK", "RESUME_TOKEN_7F3A"];
    const events = [
      { type: "thread.started", thread_id: this.id },
      { type: "turn.started" },
      { type: "item.completed", item: { id: "message-" + index, type: "agent_message", text: messages[index] } },
      { type: "turn.completed", usage: { input_tokens: 1, cached_input_tokens: 0, output_tokens: 1 } },
    ];
    return {
      events: {
        async *[Symbol.asyncIterator]() {
          for (const event of events) yield event;
        },
      },
    };
  }
}

export class Codex {
  constructor() {
    this.turnIndex = 0;
    this.threadId = "11111111-1111-4111-8111-111111111111";
  }

  startThread() {
    return new FakeThread(this, this.threadId);
  }

  resumeThread(id) {
    return new FakeThread(this, id);
  }
}
`;

test("infrastructure retry delay is exponential and capped", () => {
  assert.equal(infraRetryDelay(0), 15_000);
  assert.equal(infraRetryDelay(1), 30_000);
  assert.equal(infraRetryDelay(2), 60_000);
  assert.equal(infraRetryDelay(8), 60_000);
});

test("CLI abort cleans the process tree, releases the lock, and resumes an interrupted suite", { timeout: 30000 }, async () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-cli-"));
  const fixture = createFixture(root);
  const previousMode = process.env.SDKTESTS_FAKE_MODE_FILE;
  const previousPIDFile = process.env.SDKTESTS_FAKE_PID_FILE;
  process.env.SDKTESTS_FAKE_MODE_FILE = fixture.modePath;
  process.env.SDKTESTS_FAKE_PID_FILE = fixture.pidPath;
  try {
    writeFileSync(fixture.modePath, "block\n");
    const controller = new AbortController();
    const firstRun = runCLI({
      ...fixture.runOptions,
      args: parityFromPersistentResumeArgs(fixture.sdkPath),
      signal: controller.signal,
    });
    const processTree = await waitForJSON<FakeProcessTree>(fixture.pidPath);
    const suiteDir = onlySuiteDirectory(fixture.artifactsRoot);
    assert.equal(readLockOwner(fixture.lockPath)?.pid, process.pid);
    for (const pid of Object.values(processTree)) assert.equal(isProcessAlive(pid), true);

    await assert.rejects(
      runCLI({
        ...fixture.runOptions,
        args: parityFromPersistentResumeArgs(fixture.sdkPath),
        signal: new AbortController().signal,
      }),
      /another live sdktests runner owns/,
    );

    controller.abort(new Error("integration test abort"));
    let abortedArtifact = "";
    await assert.rejects(firstRun, (error: any) => {
      abortedArtifact = error?.artifactDir ?? "";
      return error instanceof HarnessAbortError;
    });
    assert.ok(abortedArtifact);
    assert.equal(existsSync(fixture.lockPath), false);
    for (const pid of Object.values(processTree)) {
      assert.equal(await waitForProcessExit(pid), true, `process ${pid} should exit after abort`);
    }

    const interrupted = readJSON<any>(path.join(suiteDir, "suite-summary.json"));
    assert.equal(interrupted.status, "interrupted");
    assert.equal(interrupted.scenarios[0].status, "incomplete");
    assert.equal(interrupted.scenarios[0].artifactDir, abortedArtifact);
    const incompleteState = readJSON<any>(path.join(abortedArtifact, "run-state.json"));
    assert.equal(incompleteState.status, "incomplete");
    assert.deepEqual(incompleteState.completedImplementations, []);
    assert.deepEqual(readdirSync(fixture.tempRoot), []);

    writeFileSync(fixture.modePath, "complete\n");
    const resumeExitCode = await runCLI({
      ...fixture.runOptions,
      args: parityResumeArgs(fixture.sdkPath, suiteDir),
      signal: new AbortController().signal,
    });
    assert.equal(resumeExitCode, 0);
    const completed = readJSON<any>(path.join(suiteDir, "suite-summary.json"));
    assert.equal(completed.status, "completed");
    assert.equal(completed.scenarios[0].status, "completed");
    assert.equal(completed.scenarios[0].comparison.status, "pass");
    assert.notEqual(completed.scenarios[0].artifactDir, abortedArtifact);
    const completedState = readJSON<any>(path.join(completed.scenarios[0].artifactDir, "run-state.json"));
    assert.equal(completedState.status, "completed");
    assert.deepEqual(completedState.completedImplementations, ["rust", "go"]);
    assert.equal(existsSync(fixture.lockPath), false);
  } finally {
    restoreEnvironment("SDKTESTS_FAKE_MODE_FILE", previousMode);
    restoreEnvironment("SDKTESTS_FAKE_PID_FILE", previousPIDFile);
    rmSync(root, { recursive: true, force: true });
  }
});

test("CLI process handles native SIGINT and SIGTERM", {
  skip: process.platform === "win32" ? "Windows process.kill cannot deliver catchable POSIX signals" : false,
  timeout: 30000,
}, async (t) => {
  for (const [signal, expectedExitCode] of [["SIGINT", 130], ["SIGTERM", 143]] as const) {
    await t.test(signal, async () => {
      const root = mkdtempSync(path.join(os.tmpdir(), `sdktests-${signal.toLowerCase()}-`));
      const fixture = createFixture(root);
      writeFileSync(fixture.modePath, "block\n");
      const harnessPath = path.join(root, "signal-harness.mjs");
      const processOptions: RunProcessCLIOptions = {
        ...fixture.runOptions,
        args: parityFromPersistentResumeArgs(fixture.sdkPath),
      };
      writeFileSync(harnessPath, [
        `process.env.SDKTESTS_FAKE_MODE_FILE = ${JSON.stringify(fixture.modePath)};`,
        `process.env.SDKTESTS_FAKE_PID_FILE = ${JSON.stringify(fixture.pidPath)};`,
        `const { runProcessCLI } = await import(${JSON.stringify(pathToFileURL(path.join(sdktestsRoot, "src", "cli.ts")).href)});`,
        `await runProcessCLI(${JSON.stringify(processOptions)});`,
      ].join("\n"));
      const child = spawn(process.execPath, ["--experimental-strip-types", harnessPath], {
        windowsHide: true,
        stdio: ["ignore", "pipe", "pipe"],
      });
      let stderr = "";
      child.stderr?.on("data", (chunk) => { stderr += String(chunk); });
      try {
        const processTree = await waitForJSON<FakeProcessTree>(fixture.pidPath);
        assert.equal(child.kill(signal), true);
        const [code, exitSignal] = await once(child, "exit") as [number | null, NodeJS.Signals | null];
        assert.equal(exitSignal, null);
        assert.equal(code, expectedExitCode, stderr);
        assert.equal(existsSync(fixture.lockPath), false);
        for (const pid of Object.values(processTree)) {
          assert.equal(await waitForProcessExit(pid), true, `process ${pid} should exit after ${signal}`);
        }
        const suiteDir = onlySuiteDirectory(fixture.artifactsRoot);
        const summary = readJSON<any>(path.join(suiteDir, "suite-summary.json"));
        assert.equal(summary.status, "interrupted");
        assert.equal(summary.scenarios[0].status, "incomplete");
        const state = readJSON<any>(path.join(summary.scenarios[0].artifactDir, "run-state.json"));
        assert.equal(state.status, "incomplete");
      } finally {
        if (isProcessAlive(child.pid ?? 0)) child.kill("SIGKILL");
        rmSync(root, { recursive: true, force: true });
      }
    });
  }
});

function createFixture(root: string) {
  const sdkPath = path.join(root, "sdk");
  mkdirSync(path.join(sdkPath, "dist"), { recursive: true });
  writeFileSync(path.join(sdkPath, "package.json"), `${JSON.stringify({ type: "module" })}\n`);
  writeFileSync(path.join(sdkPath, "dist", "index.js"), fakeSDKSource);
  const artifactsRoot = path.join(root, "artifacts");
  const tempRoot = path.join(root, "tmp");
  const lockPath = path.join(root, "live.lock");
  mkdirSync(tempRoot, { recursive: true });
  const runOptions: Omit<RunCLIOptions, "args" | "signal"> = {
    lockPath,
    artifactsRoot,
    tempRoot,
    log: () => {},
  };
  return {
    sdkPath,
    artifactsRoot,
    tempRoot,
    lockPath,
    modePath: path.join(root, "mode.txt"),
    pidPath: path.join(root, "process-tree.json"),
    runOptions,
  };
}

function parityFromPersistentResumeArgs(sdkPath: string): string[] {
  return [
    "parity", "--all", "--from", "persistent-resume",
    "--rust", process.execPath,
    "--go", process.execPath,
    "--sdk", sdkPath,
  ];
}

function parityResumeArgs(sdkPath: string, suiteDir: string): string[] {
  return [
    "parity", "--resume", suiteDir,
    "--rust", process.execPath,
    "--go", process.execPath,
    "--sdk", sdkPath,
  ];
}

function onlySuiteDirectory(artifactsRoot: string): string {
  const root = path.join(artifactsRoot, "suites");
  const entries = readdirSync(root, { withFileTypes: true }).filter((entry) => entry.isDirectory());
  assert.equal(entries.length, 1);
  return path.join(root, entries[0].name);
}

async function waitForJSON<T>(filePath: string, timeoutMs = 10000): Promise<T> {
  const deadline = Date.now() + timeoutMs;
  while (Date.now() < deadline) {
    if (existsSync(filePath)) {
      try {
        return readJSON<T>(filePath);
      } catch {
        // The writer may still be flushing the file.
      }
    }
    await new Promise((resolve) => setTimeout(resolve, 25));
  }
  throw new Error(`timed out waiting for JSON file ${filePath}`);
}

function readJSON<T>(filePath: string): T {
  return JSON.parse(readFileSync(filePath, "utf8")) as T;
}

function restoreEnvironment(key: string, value: string | undefined): void {
  if (value === undefined) delete process.env[key];
  else process.env[key] = value;
}
