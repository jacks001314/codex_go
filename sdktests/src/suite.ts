import { existsSync, readFileSync } from "node:fs";
import path from "node:path";
import { currentPlatformSuite } from "./platform/index.ts";
import { sha256File, writeJsonAtomic } from "./util.ts";

export type SuiteIdentity = {
  platform: string;
  rustPath: string;
  rustSha256: string | null;
  goPath: string;
  goSha256: string | null;
  sdkPath: string;
  sdkDistSha256: string | null;
};

export type SuiteScenarioRecord = {
  name: string;
  status: "pending" | "running" | "completed" | "incomplete";
  artifactDir?: string;
  comparison?: {
    status: "pass" | "behavior_mismatch" | "infra_failure";
    classification: string;
    firstMismatch: string | null;
  };
  error?: string;
  startedAt?: string;
  completedAt?: string;
};

export type SuiteSummary = {
  version: 1;
  id: string;
  createdAt: string;
  updatedAt: string;
  status: "running" | "completed" | "interrupted";
  identity: SuiteIdentity;
  order: ("rust" | "go")[];
  scenarioNames: string[];
  scenarios: SuiteScenarioRecord[];
};

export function buildSuiteIdentity(args: { rustPath: string; goPath: string; sdkPath: string }): SuiteIdentity {
  const identity: SuiteIdentity = {
    platform: currentPlatformSuite(),
    rustPath: path.resolve(args.rustPath),
    rustSha256: sha256File(args.rustPath),
    goPath: path.resolve(args.goPath),
    goSha256: sha256File(args.goPath),
    sdkPath: path.resolve(args.sdkPath),
    sdkDistSha256: sha256File(path.join(args.sdkPath, "dist", "index.js")),
  };
  for (const [label, filePath, hash] of [
    ["Rust binary", identity.rustPath, identity.rustSha256],
    ["Go binary", identity.goPath, identity.goSha256],
    ["SDK dist", path.join(identity.sdkPath, "dist", "index.js"), identity.sdkDistSha256],
  ] as const) {
    if (!hash) throw new Error(`${label} not found or not a file: ${filePath}`);
  }
  return identity;
}

export function createSuiteSummary(options: {
  suiteDir: string;
  identity: SuiteIdentity;
  order: ("rust" | "go")[];
  scenarioNames: string[];
}): SuiteSummary {
  const now = new Date().toISOString();
  const summary: SuiteSummary = {
    version: 1,
    id: path.basename(options.suiteDir),
    createdAt: now,
    updatedAt: now,
    status: "running",
    identity: options.identity,
    order: options.order,
    scenarioNames: [...options.scenarioNames],
    scenarios: options.scenarioNames.map((name) => ({ name, status: "pending" })),
  };
  writeSuiteSummary(options.suiteDir, summary);
  return summary;
}

export function loadSuiteSummary(
  suiteDir: string,
  identity: SuiteIdentity,
  requestedOrder?: ("rust" | "go")[],
): SuiteSummary {
  const summaryPath = suiteSummaryPath(suiteDir);
  if (!existsSync(summaryPath)) throw new Error(`suite summary not found: ${summaryPath}`);
  const summary = JSON.parse(readFileSync(summaryPath, "utf8")) as SuiteSummary;
  if (summary.version !== 1 || !Array.isArray(summary.scenarios)) {
    throw new Error(`unsupported suite summary: ${summaryPath}`);
  }
  assertSuiteIdentity(summary.identity, identity);
  if (requestedOrder && JSON.stringify(requestedOrder) !== JSON.stringify(summary.order)) {
    throw new Error(`cannot resume suite with a different --order; expected ${summary.order.join("-")}`);
  }
  for (const scenario of summary.scenarios) {
    if (scenario.status === "running") {
      scenario.status = "incomplete";
      scenario.error = "previous runner terminated before recording completion";
    }
  }
  summary.status = "running";
  writeSuiteSummary(suiteDir, summary);
  return summary;
}

export function selectScenarioNames(names: string[], from?: string): string[] {
  if (!from) return [...names];
  const index = names.indexOf(from);
  if (index < 0) throw new Error(`unknown --from scenario ${from}`);
  return names.slice(index);
}

export function remainingSuiteScenarios(summary: SuiteSummary): string[] {
  return summary.scenarios.filter((scenario) => scenario.status !== "completed").map((scenario) => scenario.name);
}

export function suiteExitCode(summary: SuiteSummary): number {
  let result = 0;
  for (const scenario of summary.scenarios) {
    if (scenario.status !== "completed" || !scenario.comparison) continue;
    if (scenario.comparison.status === "infra_failure") return 2;
    if (scenario.comparison.status === "behavior_mismatch") result = 1;
  }
  return result;
}

export function updateSuiteScenario(
  suiteDir: string,
  summary: SuiteSummary,
  name: string,
  update: Partial<SuiteScenarioRecord>,
): void {
  const scenario = summary.scenarios.find((entry) => entry.name === name);
  if (!scenario) throw new Error(`scenario ${name} is not part of suite ${summary.id}`);
  Object.assign(scenario, update);
  summary.updatedAt = new Date().toISOString();
  writeSuiteSummary(suiteDir, summary);
}

export function finishSuite(suiteDir: string, summary: SuiteSummary, status: "completed" | "interrupted"): void {
  summary.status = status;
  summary.updatedAt = new Date().toISOString();
  writeSuiteSummary(suiteDir, summary);
}

export function writeSuiteSummary(suiteDir: string, summary: SuiteSummary): void {
  writeJsonAtomic(suiteSummaryPath(suiteDir), summary);
}

export function suiteSummaryPath(suiteDir: string): string {
  return path.join(path.resolve(suiteDir), "suite-summary.json");
}

function assertSuiteIdentity(expected: SuiteIdentity, actual: SuiteIdentity): void {
  for (const key of ["platform", "rustSha256", "goSha256", "sdkDistSha256"] as const) {
    if (expected?.[key] !== actual[key]) {
      throw new Error(`cannot resume suite: ${key} changed from ${String(expected?.[key])} to ${String(actual[key])}`);
    }
  }
}
