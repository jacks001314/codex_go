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
  await main(abortController.signal);
} catch (error: any) {
  console.error(error?.message ?? error);
  process.exitCode = receivedSignal ? signalExitCode(receivedSignal) : 2;
} finally {
  process.removeListener("SIGINT", sigintHandler);
  process.removeListener("SIGTERM", sigtermHandler);
}

async function main(signal: AbortSignal): Promise<void> {
  const args = process.argv.slice(2);
  const command = args.shift() ?? "smoke";
  const options = parseOptions(args);
  if (command === "report") {
    const artifact = path.resolve(stringOption(options.artifact) ?? latestArtifactDir());
    const result = compareArtifact(artifact);
    console.log(`sdktests report: ${result.status} (${result.classification})`);
    console.log(path.join(artifact, "report.md"));
    process.exitCode = exitCode(result.status);
    return;
  }
  if (command !== "smoke" && command !== "parity") {
    throw new Error(`Unknown command: ${command}`);
  }

  validatePlatformOption(options.platform);
  const lock = await acquireLiveRunLock({ recover: options["recover-lock"] === true });
  try {
    if (command === "smoke") {
      const result = await runParity({
        scenario: "streaming-smoke",
        rustPath: required(options.rust, "--rust"),
        goPath: required(options.go, "--go"),
        sdkPath: required(options.sdk, "--sdk"),
        order: parseOrder(options.order),
        signal,
      });
      console.log(`sdktests smoke: ${result.comparison.status} (${result.comparison.classification})`);
      console.log(`sdktests smoke artifact: ${result.artifactDir}`);
      process.exitCode = exitCode(result.comparison.status);
      return;
    }
    await runParityCommand(options, signal);
  } finally {
    lock.release();
  }
}

async function runParityCommand(options: Record<string, string | boolean>, signal: AbortSignal): Promise<void> {
  const rustPath = required(options.rust, "--rust");
  const goPath = required(options.go, "--go");
  const sdkPath = required(options.sdk, "--sdk");
  const identity = buildSuiteIdentity({ rustPath, goPath, sdkPath });
  let order = parseOrder(options.order) ?? ["rust", "go"];
  let suiteDir: string | null = null;
  let suite: SuiteSummary | null = null;
  let scenarioNames: string[];

  if (typeof options.resume === "string") {
    if (options.all || options.scenario || options.from) {
      throw new Error("--resume cannot be combined with --all, --scenario, or --from");
    }
    suiteDir = path.resolve(options.resume);
    suite = loadSuiteSummary(suiteDir, identity, options.order ? order : undefined);
    order = suite.order;
    scenarioNames = remainingSuiteScenarios(suite);
  } else if (options.all) {
    if (options.scenario) throw new Error("--all cannot be combined with --scenario");
    const approved = scenarios.filter((scenario) => !scenario.optIn).map((scenario) => scenario.name);
    scenarioNames = selectScenarioNames(approved, stringOption(options.from));
    const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
    suiteDir = path.join(sdktestsRoot, "artifacts", "suites", `${stamp}-parity`);
    suite = createSuiteSummary({ suiteDir, identity, order, scenarioNames });
    console.log(`sdktests suite: ${suiteDir}`);
  } else {
    if (options.from) throw new Error("--from requires --all");
    scenarioNames = [required(options.scenario, "--scenario")];
  }

  let aggregateExitCode = suite ? suiteExitCode(suite) : 0;
  try {
    for (const scenario of scenarioNames) {
      if (signal.aborted) throw new Error("live SDK suite was aborted");
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
        const result = await runParity({ scenario, rustPath, goPath, sdkPath, order, signal });
        console.log(`sdktests parity ${scenario}: ${result.comparison.status} (${result.comparison.classification})`);
        console.log(`sdktests parity artifact: ${result.artifactDir}`);
        aggregateExitCode = Math.max(aggregateExitCode, exitCode(result.comparison.status));
        if (suite && suiteDir) {
          updateSuiteScenario(suiteDir, suite, scenario, {
            status: "completed",
            artifactDir: result.artifactDir,
            comparison: {
              status: result.comparison.status,
              classification: result.comparison.classification,
              firstMismatch: result.comparison.firstMismatch,
            },
            completedAt: new Date().toISOString(),
          });
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
    }
    if (suite && suiteDir) finishSuite(suiteDir, suite, "completed");
  } catch (error) {
    if (suite && suiteDir) finishSuite(suiteDir, suite, "interrupted");
    throw error;
  }
  if (suiteDir) console.log(`sdktests suite summary: ${path.join(suiteDir, "suite-summary.json")}`);
  process.exitCode = aggregateExitCode;
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
  const booleanOptions = new Set(["all", "recover-lock"]);
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

function validatePlatformOption(value: string | boolean | undefined): void {
  if (value === undefined) return;
  if (typeof value !== "string" || value !== currentPlatformSuite()) {
    throw new Error(`Live platform is ${currentPlatformSuite()}; cannot run --platform ${String(value)}`);
  }
}
