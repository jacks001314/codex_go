import { spawn } from "node:child_process";
import { existsSync, mkdirSync, rmSync } from "node:fs";
import path from "node:path";
import readline from "node:readline";
import { copyFixture, maybeRunText, repoRoot, sdktestsRoot, sha256File, snapshotFiles, writeJson } from "./util.ts";

type Implementation = "rust" | "go";

type RawRun = {
  impl: Implementation;
  status: "ok" | "error";
  error?: { name?: string; message: string };
  stderr: string;
  records: any[];
  results: Record<string, any>;
  workspace: { before: Record<string, string>; after: Record<string, string> };
  homeFiles: Record<string, string>;
  semantic: Record<string, any>;
};

const options = parseOptions(process.argv.slice(2));
const rustPath = required(options.rust, "--rust");
const goPath = required(options.go, "--go");
const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
const artifactDir = path.join(sdktestsRoot, "artifacts", `${stamp}-raw_paginated_fork`);
const tmpDir = path.join(sdktestsRoot, ".tmp", `${stamp}-raw_paginated_fork`);
mkdirSync(path.join(artifactDir, "raw"), { recursive: true });
mkdirSync(path.join(artifactDir, "normalized"), { recursive: true });

const manifest = {
  generatedAt: new Date().toISOString(),
  driver: "raw-app-server-jsonrpc",
  scenario: "raw_paginated_fork",
  platform: { os: process.platform, arch: process.arch, node: process.version },
  goCommit: maybeRunText("git", ["rev-parse", "HEAD"], repoRoot),
  goDirty: Boolean(maybeRunText("git", ["status", "--short"], repoRoot)?.trim()),
  binaries: {
    rust: binaryInfo(rustPath),
    go: binaryInfo(goPath),
  },
  sdk: null,
  assertions: [
    "paginated thread/start is accepted",
    "thread/name/set does not materialize a fresh thread",
    "paginated thread/read(includeTurns=true) is rejected",
    "ephemeral paginated fork without excludeTurns is rejected",
    "ephemeral paginated fork with excludeTurns stays pathless and returns no turns",
    "ephemeral thread/read(includeTurns=true) is rejected",
    "ephemeral fork is absent from thread/list",
  ],
};
writeJson(path.join(artifactDir, "run-manifest.json"), manifest);

let rust: RawRun | undefined;
let go: RawRun | undefined;
try {
  rust = await runImplementation("rust", rustPath, tmpDir);
  writeRun(artifactDir, rust);
  go = await runImplementation("go", goPath, tmpDir);
  writeRun(artifactDir, go);
  const equal = JSON.stringify(rust.semantic) === JSON.stringify(go.semantic);
  const passed = rust.status === "ok" && go.status === "ok" && equal && semanticChecksPass(rust.semantic);
  const comparison = {
    status: passed ? "pass" : rust.status !== "ok" || go.status !== "ok" ? "infra_failure" : "behavior_mismatch",
    classification: passed ? "parity" : rust.status !== "ok" || go.status !== "ok" ? "infra-failure" : "go-bug",
    firstMismatch: equal ? null : firstMismatch(rust.semantic, go.semantic),
    rust: rust.semantic,
    go: go.semantic,
  };
  writeJson(path.join(artifactDir, "comparison.json"), comparison);
  console.log(`raw fork audit: ${comparison.status} (${comparison.classification})`);
  console.log(`raw fork artifact: ${artifactDir}`);
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
  console.error(`raw fork artifact: ${artifactDir}`);
  process.exitCode = 2;
} finally {
  try {
    rmSync(tmpDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
  } catch (error: any) {
    console.warn(`raw fork temporary cleanup failed: ${String(error?.message ?? error)}`);
  }
}

async function runImplementation(impl: Implementation, binary: string, root: string): Promise<RawRun> {
  const home = path.join(root, impl, "home");
  const workspace = path.join(root, impl, "workspace");
  mkdirSync(home, { recursive: true });
  copyFixture(workspace, "smoke");
  const before = snapshotFiles(workspace);
  const records: any[] = [];
  const results: Record<string, any> = {};
  let stderr = "";
  let status: RawRun["status"] = "ok";
  let capturedError: RawRun["error"] | undefined;
  const child = spawn(binary, ["--disable", "plugins", "--disable", "apps", "app-server"], {
    cwd: workspace,
    env: { ...process.env, CODEX_HOME: home, NO_COLOR: "1" },
    windowsHide: true,
    stdio: ["pipe", "pipe", "pipe"],
  });
  child.stderr.on("data", (chunk) => { stderr += String(chunk); });
  const pending = new Map<number, { resolve: (value: any) => void; reject: (error: Error) => void; timer: NodeJS.Timeout }>();
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
      clientInfo: { name: "sdktests-raw-fork", version: "1.0.0" },
      capabilities: { experimentalApi: true },
    });
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "initialized", params: {} })}\n`);
    results.start = await request("thread/start", {
      cwd: workspace,
      approvalPolicy: "never",
      sandbox: "danger-full-access",
      historyMode: "paginated",
    });
    const sourceID = results.start?.result?.thread?.id;
    if (!sourceID) throw new Error(`${impl} thread/start omitted a thread id`);
    results.name = await request("thread/name/set", { threadId: sourceID, name: "raw fork source" });
    results.read = await request("thread/read", { threadId: sourceID, includeTurns: false });
    results.paginatedReadWithTurns = await request("thread/read", { threadId: sourceID, includeTurns: true });
    results.invalidFork = await request("thread/fork", { threadId: sourceID, ephemeral: true });
    results.validFork = await request("thread/fork", { threadId: sourceID, ephemeral: true, excludeTurns: true });
    const forkID = results.validFork?.result?.thread?.id;
    if (!forkID) throw new Error(`${impl} valid thread/fork omitted a thread id`);
    results.list = await request("thread/list", {});
    results.forkRead = await request("thread/read", { threadId: forkID, includeTurns: true });
  } catch (error: any) {
    status = "error";
    capturedError = { name: error?.name, message: String(error?.message ?? error) };
  } finally {
    child.stdin.end();
    await waitForExit(child);
  }
  const semantic = summarize(results);
  return {
    impl,
    status,
    error: capturedError,
    stderr,
    records,
    results,
    workspace: { before, after: snapshotFiles(workspace) },
    homeFiles: snapshotFiles(home),
    semantic,
  };
}

function summarize(results: Record<string, any>): Record<string, any> {
  const source = results.start?.result?.thread;
  const read = results.read?.result?.thread;
  const paginatedReadError = results.paginatedReadWithTurns?.error;
  const invalid = results.invalidFork?.error;
  const fork = results.validFork?.result?.thread;
  const listed = Array.isArray(results.list?.result?.data) ? results.list.result.data : [];
  const forkReadError = results.forkRead?.error;
  return {
    start: {
      accepted: Boolean(source?.id),
      historyMode: source?.historyMode ?? null,
    },
    sourceSummary: {
      readable: Boolean(read?.id),
      historyMode: read?.historyMode ?? null,
      name: read?.name ?? null,
    },
    paginatedReadWithTurns: {
      code: paginatedReadError?.code ?? null,
      message: paginatedReadError?.message ?? null,
    },
    invalidFork: {
      code: invalid?.code ?? null,
      message: invalid?.message ?? null,
    },
    validFork: {
      accepted: Boolean(fork?.id),
      ephemeral: fork?.ephemeral ?? null,
      pathIsNull: fork?.path == null,
      historyMode: fork?.historyMode ?? null,
      turnCount: Array.isArray(fork?.turns) ? fork.turns.length : null,
    },
    ephemeralReadWithTurns: {
      code: forkReadError?.code ?? null,
      message: forkReadError?.message ?? null,
    },
    list: {
      sourceCount: listed.filter((thread: any) => thread?.id === source?.id).length,
      ephemeralCount: listed.filter((thread: any) => thread?.id === fork?.id).length,
    },
  };
}

function semanticChecksPass(value: Record<string, any>): boolean {
  return value.start?.accepted === true &&
    value.start?.historyMode === "paginated" &&
    value.sourceSummary?.readable === true &&
    value.sourceSummary?.historyMode === "paginated" &&
    value.sourceSummary?.name === "raw fork source" &&
    value.paginatedReadWithTurns?.code === -32600 &&
    value.paginatedReadWithTurns?.message === "paginated threads do not support thread/read(includeTurns=true)" &&
    value.invalidFork?.code === -32600 &&
    value.invalidFork?.message === "ephemeral paginated thread/fork requires `excludeTurns: true`" &&
    value.validFork?.accepted === true &&
    value.validFork?.ephemeral === true &&
    value.validFork?.pathIsNull === true &&
    value.validFork?.historyMode === "paginated" &&
    value.validFork?.turnCount === 0 &&
    value.ephemeralReadWithTurns?.code === -32600 &&
    value.ephemeralReadWithTurns?.message === "ephemeral threads do not support includeTurns" &&
    value.list?.sourceCount === 0 &&
    value.list?.ephemeralCount === 0;
}

function firstMismatch(rust: Record<string, any>, go: Record<string, any>, prefix = ""): any {
  const keys = [...new Set([...Object.keys(rust ?? {}), ...Object.keys(go ?? {})])].sort();
  for (const key of keys) {
    const current = prefix ? `${prefix}.${key}` : key;
    const left = rust?.[key];
    const right = go?.[key];
    if (isObject(left) && isObject(right)) {
      const nested = firstMismatch(left, right, current);
      if (nested) return nested;
    } else if (JSON.stringify(left) !== JSON.stringify(right)) {
      return { path: current, rust: left, go: right };
    }
  }
  return null;
}

function isObject(value: any): value is Record<string, any> {
  return value != null && typeof value === "object" && !Array.isArray(value);
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
    child.once("exit", finish);
  });
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
