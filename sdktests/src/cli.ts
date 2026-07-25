import path from "node:path";
import { compareArtifact } from "./compare.ts";
import { runParity } from "./runner.ts";
import { currentPlatformSuite } from "./platform/index.ts";
import { scenarios } from "./scenarios.ts";
import { latestArtifactDir, sdktestsRoot } from "./util.ts";

const args = process.argv.slice(2);
const command = args.shift() ?? "smoke";

try {
  if (command === "smoke") {
    const options = parseOptions(args);
    validatePlatformOption(options.platform);
    const result = await runParity({
      scenario: "streaming-smoke",
      rustPath: required(options.rust, "--rust"),
      goPath: required(options.go, "--go"),
      sdkPath: required(options.sdk, "--sdk"),
      order: parseOrder(options.order),
    });
    console.log(`sdktests smoke: ${result.comparison.status} (${result.comparison.classification})`);
    console.log(`sdktests smoke artifact: ${result.artifactDir}`);
    process.exitCode = exitCode(result.comparison.status);
  } else if (command === "parity") {
    const options = parseOptions(args);
    validatePlatformOption(options.platform);
    const scenarioNames = options.all
      ? scenarios.filter((scenario) => !scenario.optIn).map((scenario) => scenario.name)
      : [required(options.scenario, "--scenario")];
    let aggregateExitCode = 0;
    for (const scenario of scenarioNames) {
      const result = await runParity({
        scenario,
        rustPath: required(options.rust, "--rust"),
        goPath: required(options.go, "--go"),
        sdkPath: required(options.sdk, "--sdk"),
        order: parseOrder(options.order),
      });
      console.log(`sdktests parity ${scenario}: ${result.comparison.status} (${result.comparison.classification})`);
      console.log(`sdktests parity artifact: ${result.artifactDir}`);
      aggregateExitCode = Math.max(aggregateExitCode, exitCode(result.comparison.status));
    }
    process.exitCode = aggregateExitCode;
  } else if (command === "report") {
    const options = parseOptions(args);
    const artifact = path.resolve(options.artifact ?? latestArtifactDir());
    const result = compareArtifact(artifact);
    console.log(`sdktests report: ${result.status} (${result.classification})`);
    console.log(path.join(artifact, "report.md"));
    process.exitCode = exitCode(result.status);
  } else {
    throw new Error(`Unknown command: ${command}`);
  }
} catch (error: any) {
  console.error(error?.message ?? error);
  process.exit(2);
}

function exitCode(status: "pass" | "behavior_mismatch" | "infra_failure"): number {
  if (status === "pass") {
    return 0;
  }
  return status === "behavior_mismatch" ? 1 : 2;
}

function parseOptions(items: string[]): Record<string, string | boolean> {
  const parsed: Record<string, string | boolean> = {};
  for (let index = 0; index < items.length; index += 1) {
    const item = items[index];
    if (!item.startsWith("--")) {
      continue;
    }
    const key = item.slice(2);
    if (key === "all") {
      parsed.all = true;
    } else {
      parsed[key] = items[index + 1];
      index += 1;
    }
  }
  parsed.sdk ??= path.resolve(sdktestsRoot, "..", "..", "git", "codex", "sdk", "typescript");
  return parsed;
}

function required(value: string | boolean | undefined, name: string): string {
  if (!value || typeof value !== "string") {
    throw new Error(`Missing required option ${name}`);
  }
  return value;
}

function parseOrder(value: string | boolean | undefined): ("rust" | "go")[] | undefined {
  if (value === undefined) {
    return undefined;
  }
  if (value === "rust-go") {
    return ["rust", "go"];
  }
  if (value === "go-rust") {
    return ["go", "rust"];
  }
  throw new Error(`Invalid --order ${String(value)}; expected rust-go or go-rust`);
}

function validatePlatformOption(value: string | boolean | undefined): void {
  if (value === undefined) return;
  if (typeof value !== "string" || value !== currentPlatformSuite()) {
    throw new Error(`Live platform is ${currentPlatformSuite()}; cannot run --platform ${String(value)}`);
  }
}
