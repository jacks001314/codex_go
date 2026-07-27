import { spawn } from "node:child_process";
import { existsSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import readline from "node:readline";
import { maybeRunText, repoRoot, sdktestsRoot, sha256File, snapshotFiles, writeJson } from "./util.ts";

type Implementation = "rust" | "go";

type RawRun = {
  impl: Implementation;
  requirementsInjection: Record<string, string>;
  status: "ok" | "error";
  error?: { name?: string; message: string };
  stderr: string;
  records: any[];
  results: Record<string, any>;
  homeFiles: Record<string, string>;
  semantic: Record<string, any>;
};

const options = parseOptions(process.argv.slice(2));
const rustPath = required(options.rust, "--rust");
const rustAppServerPath = required(options["rust-app-server"], "--rust-app-server");
const goPath = required(options.go, "--go");
const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
const artifactDir = path.join(sdktestsRoot, "artifacts", `${stamp}-raw_config_requirements`);
const tmpDir = path.join(sdktestsRoot, ".tmp", `${stamp}-raw_config_requirements`);
mkdirSync(path.join(artifactDir, "raw"), { recursive: true });
mkdirSync(path.join(artifactDir, "normalized"), { recursive: true });

writeJson(path.join(artifactDir, "run-manifest.json"), {
  generatedAt: new Date().toISOString(),
  driver: "raw-app-server-jsonrpc",
  scenario: "raw_config_requirements_in_app_updates",
  platform: { os: process.platform, arch: process.arch, node: process.version },
  goCommit: maybeRunText("git", ["rev-parse", "HEAD"], repoRoot),
  goDirty: Boolean(maybeRunText("git", ["status", "--short"], repoRoot)?.trim()),
  binaries: {
    rust: binaryInfo(rustPath),
    rustAppServer: binaryInfo(rustAppServerPath),
    go: binaryInfo(goPath),
  },
  sdk: null,
  requirements: { features: { in_app_updates: false } },
  requirementsInjection: {
    rust: "CODEX_APP_SERVER_MANAGED_CONFIG_PATH debug hook with sibling requirements.toml",
    go: "CODEX_HOME/requirements.toml",
  },
  assertions: [
    "configRequirements/read returns a requirements object",
    "featureRequirements contains in_app_updates",
    "the managed in_app_updates value is exactly false",
  ],
});

let rust: RawRun | undefined;
let go: RawRun | undefined;
try {
  rust = await runImplementation("rust", rustAppServerPath, tmpDir);
  writeRun(artifactDir, rust);
  go = await runImplementation("go", goPath, tmpDir);
  writeRun(artifactDir, go);
  const equal = JSON.stringify(rust.semantic) === JSON.stringify(go.semantic);
  const passed = rust.status === "ok" && go.status === "ok" && equal && semanticChecksPass(rust.semantic);
  const comparison = {
    status: passed ? "pass" : rust.status !== "ok" || go.status !== "ok" ? "infra_failure" : "behavior_mismatch",
    classification: classify(rust, go, passed),
    firstMismatch: equal ? null : firstMismatch(rust.semantic, go.semantic),
    rust: rust.semantic,
    go: go.semantic,
  };
  writeJson(path.join(artifactDir, "comparison.json"), comparison);
  console.log(`raw config requirements audit: ${comparison.status} (${comparison.classification})`);
  console.log(`raw config requirements artifact: ${artifactDir}`);
  process.exitCode = comparison.status === "pass" ? 0 : comparison.status === "behavior_mismatch" ? 1 : 2;
} catch (error: any) {
  writeJson(path.join(artifactDir, "comparison.json"), {
    status: "infra_failure",
    classification: "infra-failure",
    error: { name: error?.name, message: String(error?.message ?? error) },
    rust: rust?.semantic,
    go: go?.semantic,
  });
  console.error(error?.message ?? error);
  console.error(`raw config requirements artifact: ${artifactDir}`);
  process.exitCode = 2;
} finally {
  await removeTemporaryRoot(tmpDir);
}

async function runImplementation(impl: Implementation, binary: string, root: string): Promise<RawRun> {
  const home = path.join(root, impl, "home");
  const workspace = path.join(root, impl, "workspace");
  mkdirSync(home, { recursive: true });
  mkdirSync(workspace, { recursive: true });
  writeFileSync(path.join(home, "requirements.toml"), "[features]\nin_app_updates = false\n", "utf8");
  const requirementsInjection: Record<string, string> = impl === "rust"
    ? { kind: "debug-loader-override", managedConfigPath: path.join(home, "managed_config.toml"), requirementsPath: path.join(home, "requirements.toml") }
    : { kind: "codex-home", requirementsPath: path.join(home, "requirements.toml") };
  const records: any[] = [];
  const results: Record<string, any> = {};
  let stderr = "";
  let status: RawRun["status"] = "ok";
  let capturedError: RawRun["error"] | undefined;
  const args = impl === "rust"
    ? ["--disable-plugin-startup-tasks-for-tests"]
    : ["--disable", "plugins", "--disable", "apps", "app-server"];
  const child = spawn(binary, args, {
    cwd: workspace,
    env: {
      ...process.env,
      CODEX_HOME: home,
      NO_COLOR: "1",
      ...(impl === "rust" ? { CODEX_APP_SERVER_MANAGED_CONFIG_PATH: requirementsInjection.managedConfigPath } : {}),
    },
    windowsHide: true,
    stdio: ["pipe", "pipe", "pipe"],
  });
  child.stderr.on("data", (chunk) => { stderr += String(chunk); });
  const pending = new Map<number, {
    resolve: (value: any) => void;
    reject: (error: Error) => void;
    timer: NodeJS.Timeout;
  }>();
  readline.createInterface({ input: child.stdout }).on("line", (line) => {
    let value: any;
    try {
      value = JSON.parse(line);
    } catch {
      records.push({ parseError: line });
      return;
    }
    records.push(value);
    if (typeof value.id !== "number") return;
    const waiter = pending.get(value.id);
    if (!waiter) return;
    pending.delete(value.id);
    clearTimeout(waiter.timer);
    waiter.resolve(value);
  });
  child.once("exit", (code) => {
    for (const [id, waiter] of pending) {
      clearTimeout(waiter.timer);
      waiter.reject(new Error(`${impl} app-server exited with code ${String(code)} while request ${id} was pending`));
    }
    pending.clear();
  });
  let nextID = 1;
  const request = (method: string, params: any): Promise<any> => {
    const id = nextID++;
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`${impl} ${method} timed out`));
      }, 15_000);
      pending.set(id, { resolve, reject, timer });
    });
  };
  try {
    results.initialize = await request("initialize", {
      clientInfo: { name: "sdktests-raw-config-requirements", version: "1.0.0" },
      capabilities: { experimentalApi: true },
    });
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "initialized", params: {} })}\n`);
    results.requirements = await request("configRequirements/read", {});
  } catch (error: any) {
    status = "error";
    capturedError = { name: error?.name, message: String(error?.message ?? error) };
  } finally {
    child.stdin.end();
    await waitForExit(child);
  }
  return {
    impl,
    requirementsInjection,
    status,
    error: capturedError,
    stderr,
    records,
    results,
    homeFiles: snapshotFiles(home),
    semantic: summarize(results),
  };
}

function classify(rust: RawRun, go: RawRun, passed: boolean): string {
  if (passed) return "parity";
  if (rust.status !== "ok" || go.status !== "ok") return "infra-failure";
  const rustPasses = semanticChecksPass(rust.semantic);
  const goPasses = semanticChecksPass(go.semantic);
  if (rustPasses && !goPasses) return "go-bug";
  if (!rustPasses && goPasses) return "rust-bug-or-invalid-rust-fixture";
  return "behavior-difference";
}

function summarize(results: Record<string, any>): Record<string, any> {
  const requirements = results.requirements?.result?.requirements;
  const features = requirements?.featureRequirements;
  return {
    requirementsPresent: requirements != null,
    featureRequirementsPresent: features != null && typeof features === "object",
    inAppUpdatesPresent: features != null && Object.prototype.hasOwnProperty.call(features, "in_app_updates"),
    inAppUpdates: features?.in_app_updates ?? null,
  };
}

function semanticChecksPass(value: Record<string, any>): boolean {
  return value.requirementsPresent === true &&
    value.featureRequirementsPresent === true &&
    value.inAppUpdatesPresent === true &&
    value.inAppUpdates === false;
}

function firstMismatch(rust: Record<string, any>, go: Record<string, any>): any {
  const keys = [...new Set([...Object.keys(rust ?? {}), ...Object.keys(go ?? {})])].sort();
  for (const key of keys) {
    if (JSON.stringify(rust?.[key]) !== JSON.stringify(go?.[key])) {
      return { path: key, rust: rust?.[key], go: go?.[key] };
    }
  }
  return null;
}

function writeRun(root: string, run: RawRun): void {
  writeJson(path.join(root, "raw", `${run.impl}.json`), run);
  writeJson(path.join(root, "normalized", `${run.impl}.json`), run.semantic);
}

function binaryInfo(binary: string): Record<string, any> {
  return {
    path: binary,
    version: maybeRunText(binary, ["--version"], repoRoot),
    sha256: sha256File(binary),
  };
}

function waitForExit(child: ReturnType<typeof spawn>): Promise<void> {
  return new Promise((resolve) => {
    if (child.exitCode !== null || child.signalCode !== null) {
      resolve();
      return;
    }
    let settled = false;
    let fallbackTimer: NodeJS.Timeout | undefined;
    const finish = () => {
      if (settled) return;
      settled = true;
      clearTimeout(forceTimer);
      if (fallbackTimer) clearTimeout(fallbackTimer);
      resolve();
    };
    const forceTimer = setTimeout(() => {
      child.kill();
      fallbackTimer = setTimeout(finish, 2_000);
    }, 5_000);
    child.once("close", finish);
  });
}

async function removeTemporaryRoot(root: string): Promise<void> {
  let lastError: unknown;
  for (let attempt = 0; attempt < 40; attempt += 1) {
    try {
      rmSync(root, { recursive: true, force: true });
      return;
    } catch (error) {
      lastError = error;
      await new Promise((resolve) => setTimeout(resolve, 250));
    }
  }
  throw lastError;
}

function parseOptions(args: string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (let index = 0; index < args.length; index += 1) {
    if (!args[index].startsWith("--")) continue;
    out[args[index].slice(2)] = args[index + 1];
    index += 1;
  }
  return out;
}

function required(value: string | undefined, flag: string): string {
  if (!value) throw new Error(`Missing required option ${flag}`);
  const resolved = path.resolve(value);
  if (!existsSync(resolved)) throw new Error(`${flag} does not exist: ${resolved}`);
  return resolved;
}
