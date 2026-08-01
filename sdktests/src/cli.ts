import path from "node:path";
import { compareArtifact } from "./compare.ts";
import { acquireLiveRunLock } from "./live_lock.ts";
import { runParity } from "./runner.ts";
import { currentPlatformSuite } from "./platform/index.ts";
import { scenarios } from "./scenarios.ts";
import {
  buildSuiteIdentity,
  createSuiteSummary,
  finishSuite,
  loadSuiteSummary,
  remainingSuiteScenarios,
  selectScenarioNames,
  suiteExitCode,
  updateSuiteScenario,
  type SuiteSummary,
} from "./suite.ts";
import { latestArtifactDir, sdktestsRoot } from "./util.ts";

export type RunCLIOptions = {
  args: string[];
  signal: AbortSignal;
  lockPath?: string;
  artifactsRoot?: string;
  tempRoot?: string;
  log?: (message: string) => void;
};

export type RunProcessCLIOptions = Omit<RunCLIOptions, "signal" | "args"> & { args?: string[] };

export async function runCLI(runOptions: RunCLIOptions): Promise<number> {
  const args = [...runOptions.args];
  const command = args.shift() ?? "smoke";
  const options = parseOptions(args);
  const log = runOptions.log ?? console.log;
  if (command === "report") {
    const artifact = path.resolve(stringOption(options.artifact) ?? latestArtifactDir());
    const result = compareArtifact(artifact);
    log(`sdktests report: ${result.status} (${result.classification})`);
    log(path.join(artifact, "report.md"));
    return exitCode(result.status);
  }
  if (command !== "smoke" && command !== "parity") {
    throw new Error(`Unknown command: ${command}`);
  }

  validatePlatformOption(options.platform);
  const lock = await acquireLiveRunLock({
    lockPath: runOptions.lockPath,
    recover: options["recover-lock"] === true,
  });
  try {
    if (command === "smoke") {
      const model = stringOption(options.model);
      const modelReasoningEffort = parseReasoningEffort(options["reasoning-effort"]);
      const result = await runParity({
        scenario: "streaming-smoke",
        rustPath: required(options.rust, "--rust"),
        goPath: required(options.go, "--go"),
        sdkPath: required(options.sdk, "--sdk"),
        order: parseOrder(options.order),
        signal: runOptions.signal,
        artifactsRoot: runOptions.artifactsRoot,
        tempRoot: runOptions.tempRoot,
        model,
        modelReasoningEffort,
      });
      log(`sdktests smoke: ${result.comparison.status} (${result.comparison.classification})`);
      log(`sdktests smoke artifact: ${result.artifactDir}`);
      return exitCode(result.comparison.status);
    }
    return await runParityCommand(options, runOptions, log);
  } finally {
    lock.release();
  }
}

async function runParityCommand(
  options: Record<string, string | boolean>,
  runOptions: RunCLIOptions,
  log: (message: string) => void,
): Promise<number> {
  const rustPath = required(options.rust, "--rust");
  const goPath = required(options.go, "--go");
  const sdkPath = required(options.sdk, "--sdk");
  const model = stringOption(options.model);
  const modelReasoningEffort = parseReasoningEffort(options["reasoning-effort"]);
  const infraRetries = parseNonNegativeInteger(options["infra-retries"], "--infra-retries", 0, 10);
  const failFastInfra = options["fail-fast-infra"] === true;
  const identity = buildSuiteIdentity({ rustPath, goPath, sdkPath, model, modelReasoningEffort });
  let order = parseOrder(options.order) ?? ["rust", "go"];
  let suiteDir: string | null = null;
  let suite: SuiteSummary | null = null;
  let scenarioNames: string[];

  if (typeof options.resume === "string") {
    if (options.all || options.scenario || options.from) {
      throw new Error("--resume cannot be combined with --all, --scenario, or --from");
    }
    suiteDir = path.resolve(options.resume);
    suite = loadSuiteSummary(
      suiteDir,
      identity,
      options.order ? order : undefined,
      infraRetries,
      failFastInfra,
    );
    order = suite.order;
    scenarioNames = remainingSuiteScenarios(suite);
  } else if (options.all) {
    if (options.scenario) throw new Error("--all cannot be combined with --scenario");
    const approved = scenarios.filter((scenario) => !scenario.optIn).map((scenario) => scenario.name);
    scenarioNames = selectScenarioNames(approved, stringOption(options.from));
    const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
    const artifactsRoot = runOptions.artifactsRoot ?? path.join(sdktestsRoot, "artifacts");
    suiteDir = path.join(artifactsRoot, "suites", `${stamp}-parity`);
    suite = createSuiteSummary({
      suiteDir,
      identity,
      order,
      scenarioNames,
      infraRetries,
      failFastInfra,
    });
    log(`sdktests suite: ${suiteDir}`);
  } else {
    if (options.from) throw new Error("--from requires --all");
    scenarioNames = [required(options.scenario, "--scenario")];
  }

  let aggregateExitCode = suite ? suiteExitCode(suite) : 0;
  try {
    for (const scenario of scenarioNames) {
      let stopAfterInfrastructureFailure = false;
      if (runOptions.signal.aborted) throw new Error("live SDK suite was aborted");
      if (suite && suiteDir) {
        updateSuiteScenario(suiteDir, suite, scenario, {
          status: "running",
          startedAt: new Date().toISOString(),
          completedAt: undefined,
          artifactDir: undefined,
          comparison: undefined,
          error: undefined,
        });
      }
      try {
        for (let retry = 0; ; retry += 1) {
          const attemptStartedAt = new Date().toISOString();
          try {
            const result = await runParity({
              scenario,
              rustPath,
              goPath,
              sdkPath,
              order,
              signal: runOptions.signal,
              artifactsRoot: runOptions.artifactsRoot,
              tempRoot: runOptions.tempRoot,
              model,
              modelReasoningEffort,
            });
            const comparison = {
              status: result.comparison.status,
              classification: result.comparison.classification,
              firstMismatch: result.comparison.firstMismatch,
            };
            if (suite && suiteDir) {
              const record = suite.scenarios.find((entry) => entry.name === scenario);
              updateSuiteScenario(suiteDir, suite, scenario, {
                attempts: [
                  ...(record?.attempts ?? []),
                  {
                    number: (record?.attempts?.length ?? 0) + 1,
                    status: "completed",
                    startedAt: attemptStartedAt,
                    completedAt: new Date().toISOString(),
                    artifactDir: result.artifactDir,
                    comparison,
                  },
                ],
              });
            }
            log(`sdktests parity ${scenario}: ${result.comparison.status} (${result.comparison.classification})`);
            log(`sdktests parity artifact: ${result.artifactDir}`);
            if (result.comparison.status === "infra_failure" && retry < infraRetries) {
              const delayMs = infraRetryDelay(retry);
              log(
                `sdktests parity ${scenario}: retrying infrastructure failure ` +
                `(${retry + 1}/${infraRetries}) after ${delayMs}ms`,
              );
              await abortableDelay(delayMs, runOptions.signal);
              continue;
            }
            aggregateExitCode = Math.max(aggregateExitCode, exitCode(result.comparison.status));
            stopAfterInfrastructureFailure = result.comparison.status === "infra_failure" && failFastInfra;
            if (suite && suiteDir) {
              updateSuiteScenario(suiteDir, suite, scenario, {
                status: "completed",
                artifactDir: result.artifactDir,
                comparison,
                completedAt: new Date().toISOString(),
              });
            }
            break;
          } catch (error: any) {
            if (suite && suiteDir) {
              const record = suite.scenarios.find((entry) => entry.name === scenario);
              updateSuiteScenario(suiteDir, suite, scenario, {
                attempts: [
                  ...(record?.attempts ?? []),
                  {
                    number: (record?.attempts?.length ?? 0) + 1,
                    status: "incomplete",
                    startedAt: attemptStartedAt,
                    completedAt: new Date().toISOString(),
                    artifactDir: typeof error?.artifactDir === "string" ? error.artifactDir : undefined,
                    error: String(error?.message ?? error),
                  },
                ],
              });
            }
            throw error;
          }
        }
      } catch (error: any) {
        if (suite && suiteDir) {
          updateSuiteScenario(suiteDir, suite, scenario, {
            status: "incomplete",
            artifactDir: typeof error?.artifactDir === "string" ? error.artifactDir : undefined,
            error: String(error?.message ?? error),
            completedAt: new Date().toISOString(),
          });
        }
        throw error;
      }
      if (stopAfterInfrastructureFailure) {
        if (suite && suiteDir) {
          finishSuite(suiteDir, suite, "interrupted");
          log(`sdktests suite stopped after infrastructure failure: ${scenario}`);
          log(`sdktests suite summary: ${path.join(suiteDir, "suite-summary.json")}`);
        }
        return aggregateExitCode;
      }
    }
    if (suite && suiteDir) finishSuite(suiteDir, suite, "completed");
  } catch (error) {
    if (suite && suiteDir) finishSuite(suiteDir, suite, "interrupted");
    throw error;
  }
  if (suiteDir) log(`sdktests suite summary: ${path.join(suiteDir, "suite-summary.json")}`);
  return aggregateExitCode;
}

export async function runProcessCLI(processOptions: RunProcessCLIOptions = {}): Promise<void> {
  const abortController = new AbortController();
  let receivedSignal: "SIGINT" | "SIGTERM" | null = null;
  const signalHandler = (signal: "SIGINT" | "SIGTERM") => {
    receivedSignal ??= signal;
    abortController.abort(new Error(`received ${signal}`));
  };
  const sigintHandler = () => signalHandler("SIGINT");
  const sigtermHandler = () => signalHandler("SIGTERM");
  process.once("SIGINT", sigintHandler);
  process.once("SIGTERM", sigtermHandler);
  try {
    process.exitCode = await runCLI({
      ...processOptions,
      args: processOptions.args ?? process.argv.slice(2),
      signal: abortController.signal,
    });
  } catch (error: any) {
    console.error(error?.message ?? error);
    process.exitCode = receivedSignal ? signalExitCode(receivedSignal) : 2;
  } finally {
    process.removeListener("SIGINT", sigintHandler);
    process.removeListener("SIGTERM", sigtermHandler);
  }
}

function exitCode(status: "pass" | "behavior_mismatch" | "infra_failure"): number {
  if (status === "pass") return 0;
  return status === "behavior_mismatch" ? 1 : 2;
}

function signalExitCode(signal: "SIGINT" | "SIGTERM"): number {
  return signal === "SIGINT" ? 130 : 143;
}

function parseOptions(items: string[]): Record<string, string | boolean> {
  const parsed: Record<string, string | boolean> = {};
  const booleanOptions = new Set(["all", "recover-lock", "fail-fast-infra"]);
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    if (!item.startsWith("--")) continue;
    const key = item.slice(2);
    if (booleanOptions.has(key)) {
      parsed[key] = true;
    } else {
      const value = items[index + 1];
      if (!value || value.startsWith("--")) throw new Error(`Missing value for --${key}`);
      parsed[key] = value;
      index += 1;
    }
  }
  parsed.sdk ??= path.resolve(sdktestsRoot, "..", "..", "git", "codex", "sdk", "typescript");
  return parsed;
}

function required(value: string | boolean | undefined, name: string): string {
  if (!value || typeof value !== "string") throw new Error(`Missing required option ${name}`);
  return value;
}

function stringOption(value: string | boolean | undefined): string | undefined {
  return typeof value === "string" ? value : undefined;
}

function parseOrder(value: string | boolean | undefined): ("rust" | "go")[] | undefined {
  if (value === undefined) return undefined;
  if (value === "rust-go") return ["rust", "go"];
  if (value === "go-rust") return ["go", "rust"];
  throw new Error(`Invalid --order ${String(value)}; expected rust-go or go-rust`);
}

function parseReasoningEffort(
  value: string | boolean | undefined,
): "minimal" | "low" | "medium" | "high" | "xhigh" | undefined {
  if (value === undefined) return undefined;
  if (typeof value === "string" && ["minimal", "low", "medium", "high", "xhigh"].includes(value)) {
    return value as "minimal" | "low" | "medium" | "high" | "xhigh";
  }
  throw new Error(`Invalid --reasoning-effort ${String(value)}; expected minimal, low, medium, high, or xhigh`);
}

function parseNonNegativeInteger(
  value: string | boolean | undefined,
  name: string,
  fallback: number,
  maximum: number,
): number {
  if (value === undefined) return fallback;
  if (typeof value !== "string" || !/^\d+$/.test(value)) {
    throw new Error(`Invalid ${name} ${String(value)}; expected an integer from 0 to ${maximum}`);
  }
  const parsed = Number(value);
  if (!Number.isSafeInteger(parsed) || parsed > maximum) {
    throw new Error(`Invalid ${name} ${value}; expected an integer from 0 to ${maximum}`);
  }
  return parsed;
}

export function infraRetryDelay(retry: number): number {
  return Math.min(15_000 * 2 ** retry, 60_000);
}

async function abortableDelay(delayMs: number, signal: AbortSignal): Promise<void> {
  if (signal.aborted) throw signal.reason ?? new Error("live SDK suite was aborted");
  await new Promise<void>((resolve, reject) => {
    const timer = setTimeout(done, delayMs);
    signal.addEventListener("abort", aborted, { once: true });
    function done() {
      signal.removeEventListener("abort", aborted);
      resolve();
    }
    function aborted() {
      clearTimeout(timer);
      reject(signal.reason ?? new Error("live SDK suite was aborted"));
    }
  });
}

function validatePlatformOption(value: string | boolean | undefined): void {
  if (value === undefined) return;
  if (typeof value !== "string" || value !== currentPlatformSuite()) {
    throw new Error(`Live platform is ${currentPlatformSuite()}; cannot run --platform ${String(value)}`);
  }
}

if (import.meta.main) await runProcessCLI();
