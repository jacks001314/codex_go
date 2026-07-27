import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { cpSync, existsSync, mkdirSync, readFileSync, readdirSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { compareArtifact, type CompareResult } from "./compare.ts";
import { normalizeRecording } from "./normalize.ts";
import { currentPlatformSuite } from "./platform/index.ts";
import { terminateProcessTree } from "./process_tree.ts";
import { getScenario } from "./scenarios.ts";
import { copyCodexHome, copyFixture, copyWindowsSandboxState, maybeRunText, readJson, repoRoot, safeConfigSummary, sdktestsRoot, sha256File, snapshotFiles, writeJson, writeJsonAtomic } from "./util.ts";

type RunArgs = {
  scenario: string;
  rustPath: string;
  goPath: string;
  sdkPath: string;
  order?: ("rust" | "go")[];
  signal?: AbortSignal;
};

export type ParityRunResult = {
  artifactDir: string;
  comparison: CompareResult;
};

export async function runParity(args: RunArgs): Promise<ParityRunResult> {
  const scenario = getScenario(args.scenario);
  const platformSuite = currentPlatformSuite();
  if (scenario.platforms && !scenario.platforms.includes(platformSuite)) {
    throw new Error(`Scenario ${scenario.name} does not support platform ${platformSuite}`);
  }
  const sdkIndex = path.join(args.sdkPath, "dist", "index.js");
  if (!existsSync(sdkIndex)) {
    throw new Error(`SDK dist not found: ${sdkIndex}`);
  }
  const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
  const artifactDir = path.join(sdktestsRoot, "artifacts", `${stamp}-${scenario.name}`);
  const tmpDir = path.join(sdktestsRoot, ".tmp", `${stamp}-${scenario.name}`);
  mkdirSync(path.join(artifactDir, "raw"), { recursive: true });
  mkdirSync(path.join(artifactDir, "normalized"), { recursive: true });
  mkdirSync(tmpDir, { recursive: true });
  const runStatePath = path.join(artifactDir, "run-state.json");
  const runState: any = {
    status: "running",
    scenario: scenario.name,
    startedAt: new Date().toISOString(),
    completedImplementations: [],
  };
  writeJsonAtomic(runStatePath, runState);

  const homeSource = path.join(os.homedir(), ".codex");
  const sourceConfig = path.join(homeSource, "config.toml");
  const configSummary = safeConfigSummary(sourceConfig);

  const manifest = buildManifest(args, scenario, configSummary);
  writeJson(path.join(artifactDir, "run-manifest.json"), manifest);

  const order = args.order ?? ["rust", "go"];
  try {
    for (const impl of order) {
      throwIfAborted(args.signal);
      const workspace = path.join(tmpDir, impl, "workspace");
      const home = path.join(tmpDir, impl, "home");
      copyFixture(
        workspace,
        scenario.name === "real-coding-unittest"
          ? "coding"
          : scenario.name === "real-coding-modify"
            ? "coding_modify"
            : scenario.name === "real-coding-move-delete"
              ? "coding_move"
              : scenario.name === "real-coding-multifile-refactor"
                ? "coding_refactor"
                : scenario.name === "resume-real-coding-recovery"
                  ? "coding_refactor"
                  : scenario.name.startsWith("resume-") || scenario.name === "apply-patch-absolute-path-success"
                    ? "resume_tools"
                    : "smoke",
      );
      if (scenario.localImageFixture) {
        cpSync(path.join(repoRoot, scenario.localImageFixture), path.join(workspace, "image.png"));
      }
      let additionalDirectories: string[] | undefined;
      if (scenario.additionalDirectoryMode === "fixture") {
        const additional = path.join(tmpDir, impl, "additional");
        mkdirSync(additional, { recursive: true });
        writeFileSync(path.join(additional, "outside.txt"), "SDK_ADDITIONAL_DIRECTORY_FIXTURE", "utf8");
        additionalDirectories = [additional];
      }
      const homeCopy = copyCodexHome(homeSource, home);
      const sandboxStateCopied = impl === "go" ? copyWindowsSandboxState(homeSource, home) : [];
      const before = snapshotFiles(workspace);
      const recording = await runWorker({
        sdkIndex,
        codexPath: impl === "rust" ? args.rustPath : args.goPath,
        envHome: home,
        impl,
        scenario,
        workspace,
        workingDirectory:
          scenario.workingDirectoryMode === "missing"
            ? path.join(tmpDir, impl, "missing-workspace")
            : workspace,
        authFilesCopied: homeCopy.copied,
        sandboxStateCopied,
        additionalDirectories,
        missingWorkingDirectory: path.join(tmpDir, impl, "missing-workspace"),
      }, args.signal);
      recording.workspace = {
        before,
        after: snapshotFiles(workspace),
      };
      recording.sandboxLogs = readSandboxLogs(home);
      writeJson(path.join(artifactDir, "raw", `${impl}.json`), recording);
      writeJson(path.join(artifactDir, "normalized", `${impl}.json`), normalizeRecording(recording));
      runState.completedImplementations.push(impl);
      runState.updatedAt = new Date().toISOString();
      writeJsonAtomic(runStatePath, runState);
      if (recording.harness?.aborted) throw new HarnessAbortError("live SDK worker was aborted");
    }

    const comparison = compareArtifact(artifactDir);
    runState.status = "completed";
    runState.completedAt = new Date().toISOString();
    runState.comparison = {
      status: comparison.status,
      classification: comparison.classification,
      firstMismatch: comparison.firstMismatch,
    };
    writeJsonAtomic(runStatePath, runState);
    return { artifactDir, comparison };
  } catch (error: any) {
    runState.status = "incomplete";
    runState.completedAt = new Date().toISOString();
    runState.error = String(error?.message ?? error);
    writeJsonAtomic(runStatePath, runState);
    if (error && typeof error === "object" && typeof error.artifactDir !== "string") {
      error.artifactDir = artifactDir;
    }
    throw error;
  } finally {
    rmSync(tmpDir, { recursive: true, force: true });
  }
}

function readSandboxLogs(home: string): Record<string, string> {
  const directory = path.join(home, ".sandbox");
  if (!existsSync(directory)) {
    return {};
  }
  const logs: Record<string, string> = {};
  for (const entry of readdirSync(directory, { withFileTypes: true })) {
    if (entry.isFile() && entry.name.endsWith(".log")) {
      logs[entry.name] = readFileSync(path.join(directory, entry.name), "utf8");
    }
  }
  return logs;
}

async function runWorker(input: any, signal?: AbortSignal): Promise<any> {
  const workerInput = path.join(input.envHome, "worker-input.json");
  const workerOutput = path.join(input.envHome, "worker-output.json");
  writeJson(workerInput, input);
  const hardTimeoutMs = input.scenario.turns.reduce(
    (sum: number, turn: any) => sum + (turn.timeoutMs ?? input.scenario.timeoutMs), 0,
  ) + 15000;
  const started = Date.now();
  const child = spawn(process.execPath, ["--experimental-strip-types", path.join(sdktestsRoot, "src", "worker.ts"), workerInput, workerOutput], {
    cwd: repoRoot, detached: process.platform !== "win32", windowsHide: true, stdio: ["ignore", "pipe", "pipe"],
  });
  let stderr = "";
  child.stderr?.on("data", (chunk) => { stderr += String(chunk); });
  const result = await new Promise<{ timedOut: boolean; aborted: boolean; code: number | null }>((resolve) => {
    let settled = false;
    const settleAfterTermination = async (timedOut: boolean, aborted: boolean) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
      await terminateProcessTree(child.pid ?? 0);
      resolve({ timedOut, aborted, code: null });
    };
    const onAbort = () => { void settleAfterTermination(false, true); };
    const timer = setTimeout(() => { void settleAfterTermination(true, false); }, hardTimeoutMs);
    signal?.addEventListener("abort", onAbort, { once: true });
    if (signal?.aborted) onAbort();
    child.once("exit", (code) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      signal?.removeEventListener("abort", onAbort);
      resolve({ timedOut: false, aborted: false, code });
    });
  });
  const partial = existsSync(workerOutput) ? readJson(workerOutput) : {
    impl: input.impl, status: "error", events: [], turns: [], threadId: null, threadIds: [],
    authAvailable: input.authFilesCopied.includes("auth.json"), sandboxStateCopied: input.sandboxStateCopied,
  };
  if (result.aborted) {
    partial.status = "error";
    partial.error = { name: "HarnessAborted", message: "worker aborted by live runner" };
    partial.harness = { aborted: true, timedOut: false, workerPid: child.pid, stderr, durationMs: Date.now() - started };
  } else if (result.timedOut) {
    partial.status = "error";
    partial.error = { name: "HarnessHardTimeout", message: `worker exceeded ${hardTimeoutMs}ms` };
    partial.harness = { timedOut: true, workerPid: child.pid, stderr, durationMs: Date.now() - started };
  } else {
    partial.harness = { timedOut: false, workerPid: child.pid, exitCode: result.code, stderr, durationMs: Date.now() - started };
    if (result.code !== 0 && partial.status === "ok") {
      partial.status = "error";
      partial.error = { name: "WorkerExitError", message: `worker exited with code ${result.code}` };
    }
  }
  return partial;
}

export class HarnessAbortError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "HarnessAbortError";
  }
}

function throwIfAborted(signal?: AbortSignal): void {
  if (signal?.aborted) throw new HarnessAbortError("live SDK run was aborted");
}

function buildManifest(args: RunArgs, scenario: any, configSummary: Record<string, string>): any {
  const parityPath = path.join(repoRoot, "parity.json");
  const parity = existsSync(parityPath) ? readJson(parityPath) : {};
  const rustRepo = path.resolve(args.sdkPath, "..", "..");
  const rustUpstreamCommit = maybeRunText("git", ["rev-parse", "HEAD"], rustRepo);
  const goCommit = maybeRunText("git", ["rev-parse", "HEAD"], repoRoot);
  const goStatus = maybeRunText("git", ["status", "--short"], repoRoot);
  const sdkPackagePath = path.join(args.sdkPath, "package.json");
  const sdkPackage = existsSync(sdkPackagePath) ? readJson(sdkPackagePath) : {};
  const rustBinary = binaryInfo(args.rustPath);
  const goBinary = binaryInfo(args.goPath);
  const expectedRustBinary = parity.rustE2EBinary ?? {};
  const rustBaselineDrift =
    (Boolean(expectedRustBinary.version) && rustBinary.version !== expectedRustBinary.version) ||
    (Boolean(expectedRustBinary.sha256) && rustBinary.sha256 !== expectedRustBinary.sha256);
  return {
    generatedAt: new Date().toISOString(),
    platform: {
      suite: currentPlatformSuite(),
      os: process.platform,
      arch: process.arch,
      node: process.version,
      go: maybeRunText("go", ["version"], repoRoot),
    },
    baseline: {
      mode: "rust-binary",
      rustBinaryPath: args.rustPath,
      goCommit,
      goDirty: Boolean(goStatus?.trim()),
      parityRustBaseline: parity.rustBaseline,
      parityRustUpstreamHead: parity.rustUpstreamHead,
      rustUpstreamCommit,
      rustBaselineDrift,
      expectedRustBinary: {
        version: expectedRustBinary.version,
        sha256: expectedRustBinary.sha256,
      },
      parityRecordDrift:
        Boolean(rustUpstreamCommit) &&
        Boolean(parity.rustUpstreamHead) &&
        rustUpstreamCommit !== parity.rustUpstreamHead,
    },
    binaries: {
      rust: rustBinary,
      go: goBinary,
    },
    sdk: {
      path: args.sdkPath,
      packageName: sdkPackage.name,
      packageVersion: sdkPackage.version,
      distHash: hashTextIfExists(path.join(args.sdkPath, "dist", "index.js")),
    },
    config: {
      source: "copied isolated CODEX_HOME config.toml",
      defaults: configSummary,
      modelPassedToSdk: false,
      modelReasoningEffortPassedToSdk: false,
    },
    scenario: {
      name: scenario.name,
      variant: `${scenario.name}/${currentPlatformSuite()}`,
      turnCount: scenario.turns.length,
      turns: scenario.turns.map((turn: any) => ({
        resume: Boolean(turn.resume),
        outputSchema: turn.outputSchema ?? null,
        includeLocalImage: Boolean(turn.includeLocalImage),
      })),
      expected: scenario.expected,
      workingDirectoryMode: scenario.workingDirectoryMode ?? "fixture",
      abortBeforeRun: Boolean(scenario.abortBeforeRun),
      additionalDirectoryMode: scenario.additionalDirectoryMode ?? "none",
      localImageFixture: scenario.localImageFixture ?? null,
    },
    scenarios: [scenario.name],
    order: args.order ?? ["rust", "go"],
  };
}

function binaryInfo(binaryPath: string): any {
  return {
    path: binaryPath,
    version: maybeRunText(binaryPath, ["--version"], repoRoot),
    sha256: sha256File(binaryPath),
  };
}

function hashTextIfExists(filePath: string): string | null {
  if (!existsSync(filePath)) {
    return null;
  }
  return createHash("sha256").update(readFileSync(filePath)).digest("hex");
}
