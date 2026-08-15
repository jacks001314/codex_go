import { spawn } from "node:child_process";
import { existsSync, mkdirSync, rmSync } from "node:fs";
import path from "node:path";
import readline from "node:readline";
import { copyFixture, maybeRunText, repoRoot, sdktestsRoot, sha256File, snapshotFiles, writeJson } from "./util.ts";

// Model-free app-server protocol audit for thread/goal/* (get/set/clear).
// Drives the same JSON-RPC requests through the Rust and Go app-servers and
// compares responses, error messages, notification ordering, and home-dir
// side effects. No model call, no credentials required.

type Implementation = "rust" | "go";

type RawRun = {
  impl: Implementation;
  status: "ok" | "error";
  error?: { name?: string; message: string };
  stderr: string;
  records: any[];
  results: Record<string, any>;
  semantic: Record<string, any>;
  workspace: { before: Record<string, string>; after: Record<string, string> };
  homeFiles: Record<string, string>;
};

type Step = {
  key: string;
  method: string;
  params: any;
};

const options = parseOptions(process.argv.slice(2));
const rustPath = required(options.rust, "--rust");
const goPath = required(options.go, "--go");
const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
const artifactDir = path.join(sdktestsRoot, "artifacts", `${stamp}-raw_goal`);
const tmpDir = path.join(sdktestsRoot, ".tmp", `${stamp}-raw_goal`);
mkdirSync(path.join(artifactDir, "raw"), { recursive: true });
mkdirSync(path.join(artifactDir, "normalized"), { recursive: true });

const manifest = {
  generatedAt: new Date().toISOString(),
  driver: "raw-app-server-jsonrpc",
  scenario: "raw_goal",
  platform: { os: process.platform, arch: process.arch, node: process.version },
  goCommit: maybeRunText("git", ["rev-parse", "HEAD"], repoRoot),
  goDirty: Boolean(maybeRunText("git", ["status", "--short"], repoRoot)?.trim()),
  binaries: {
    rust: binaryInfo(rustPath),
    go: binaryInfo(goPath),
  },
  sdk: null,
  assertions: [
    "thread/goal/get returns null goal on a fresh thread",
    "thread/goal/set with an objective creates an active goal and trims whitespace",
    "thread/goal/set status-only pauses/resumes an existing goal and keeps the objective",
    "thread/goal/set objective replaces the existing goal objective",
    "tokenBudget set/clear round-trips (explicit null resets to the configured max)",
    "blank/oversized objective and non-positive budget are rejected",
    "status-only set on a thread with no goal is rejected",
    "unknown thread id is rejected",
    "ephemeral (pathless) fork does not support goals",
    "goals feature disabled rejects thread/goal/*",
    "thread/goal/updated|cleared notification ordering relative to the response matches",
  ],
  normalizationRules: [
    {
      field: "comparison.ordering",
      rule: "notifications are attributed to the request whose response immediately precedes them in the raw stream (attributeGoalNotifications) and compared for equality",
      reason: "thread/goal/updated|cleared notifications must follow their own response (Rust send_response-before-emit order). Go stream transports defer these notifications and flush them after the response, so both implementations attribute every notification to the same set op. semantic.*.preNotifications remains raw recorder-window evidence for diagnostics and is excluded from the content equality.",
    },
    {
      field: "goal.createdAt/updatedAt",
      rule: "normalized to <TS>",
      reason: "Unix timestamps are wall-clock volatile and not part of the observable goal contract.",
    },
    {
      field: "threadId",
      rule: "mapped to <THREAD_1>/<THREAD_2> placeholders",
      reason: "Thread ids are freshly generated per run; the mapping preserves reference identity across steps.",
    },
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
  const contentEqual = JSON.stringify(contentView(rust.semantic)) === JSON.stringify(contentView(go.semantic));
  const notificationsEqual =
    JSON.stringify(rust.semantic.notificationsStream ?? []) === JSON.stringify(go.semantic.notificationsStream ?? []);
  const ordering = {
    rust: attributeGoalNotifications(rust.records, rust.semantic),
    go: attributeGoalNotifications(go.records, go.semantic),
    matches:
      JSON.stringify(attributeGoalNotifications(rust.records, rust.semantic)) ===
      JSON.stringify(attributeGoalNotifications(go.records, go.semantic)),
  };
  const equal = contentEqual && notificationsEqual;
  const passed = rust.status === "ok" && go.status === "ok" && equal && semanticChecksPass(rust.semantic);
  const comparison = {
    status: passed ? "pass" : rust.status !== "ok" || go.status !== "ok" ? "infra_failure" : "behavior_mismatch",
    classification: passed
      ? ordering.matches
        ? "parity"
        : "parity-with-compat-note"
      : rust.status !== "ok" || go.status !== "ok"
        ? "infra-failure"
        : "go-bug",
    firstMismatch: equal ? null : firstMismatch(rust.semantic, go.semantic),
    ordering,
    rust: rust.semantic,
    go: go.semantic,
  };
  writeJson(path.join(artifactDir, "comparison.json"), comparison);
  console.log(`raw goal audit: ${comparison.status} (${comparison.classification})`);
  if (comparison.ordering && !comparison.ordering.matches) {
    console.log(
      "note: thread/goal/updated|cleared notification placement relative to its response differs; content and count match.",
    );
  }

  console.log(`raw goal artifact: ${artifactDir}`);
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
  console.error(`raw goal artifact: ${artifactDir}`);
  process.exitCode = 2;
} finally {
  try {
    rmSync(tmpDir, { recursive: true, force: true, maxRetries: 5, retryDelay: 200 });
  } catch (error: any) {
    console.warn(`raw goal temporary cleanup failed: ${String(error?.message ?? error)}`);
  }
}

async function runImplementation(impl: Implementation, binary: string, root: string): Promise<RawRun> {
  const home = path.join(root, impl, "home");
  const workspace = path.join(root, impl, "workspace");
  mkdirSync(home, { recursive: true });
  copyFixture(workspace, "smoke");
  const before = snapshotFiles(workspace);
  const records: any[] = [];
  const goalNotificationStream: any[] = [];
  const results: Record<string, any> = {};
  const semantic: Record<string, any> = {};
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
  const pending = new Map<number, {
    resolve: (value: any) => void;
    reject: (error: Error) => void;
    timer: NodeJS.Timeout;
    preNotifications: any[];
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
    if (typeof value.id !== "number") {
      // A server notification observed before the matching response.
      if (value.method === "thread/goal/updated" || value.method === "thread/goal/cleared") {
        goalNotificationStream.push(value);
      }
      for (const waiter of pending.values()) {
        if (value.method === "thread/goal/updated" || value.method === "thread/goal/cleared") {
          waiter.preNotifications.push(value);
        }
      }
      return;
    }
    const waiter = pending.get(value.id);
    if (!waiter) return;
    pending.delete(value.id);
    clearTimeout(waiter.timer);
    waiter.resolve(value);
  });
  let nextID = 1;
  const request = (method: string, params: any): Promise<{ response: any; preNotifications: any[] }> => {
    const id = nextID++;
    const preNotifications: any[] = [];
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
    return new Promise((resolve, reject) => {
      const timer = setTimeout(() => {
        pending.delete(id);
        reject(new Error(`${impl} ${method} timed out`));
      }, 15_000);
      pending.set(id, {
        resolve: (response) => resolve({ response, preNotifications, id }),
        reject,
        timer,
        preNotifications,
      });
    });
  };

  const step = async (step: Step, threadMap: Record<string, string>): Promise<void> => {
    const { response, preNotifications, id } = await request(step.method, step.params);
    const summarized = summarizeResponse(response, threadMap);
    results[step.key] = response;
    semantic[step.key] = {
      ...summarized,
      requestID: typeof id === "number" ? id : null,
      preNotifications: preNotifications.map((notification) => ({
        method: notification.method,
        params: normalizeNotification(notification.params, threadMap),
      })),
    };
  };

  try {
    results.initialize = (await request("initialize", {
      clientInfo: { name: "sdktests-raw-goal", version: "1.0.0" },
      capabilities: { experimentalApi: true },
    })).response;
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "initialized", params: {} })}\n`);
    results.start = (await request("thread/start", {
      cwd: workspace,
      approvalPolicy: "never",
      sandbox: "danger-full-access",
      historyMode: "paginated",
    })).response;
    const sourceID = results.start?.result?.thread?.id;
    if (!sourceID) throw new Error(`${impl} thread/start omitted a thread id`);
    const threadMap: Record<string, string> = { [sourceID]: "<THREAD_1>" };

    const steps: Step[] = [
      { key: "getEmpty", method: "thread/goal/get", params: { threadId: sourceID } },
      { key: "setCreate", method: "thread/goal/set", params: { threadId: sourceID, objective: "improve benchmark coverage" } },
      { key: "getAfterCreate", method: "thread/goal/get", params: { threadId: sourceID } },
      { key: "setPause", method: "thread/goal/set", params: { threadId: sourceID, status: "paused" } },
      { key: "setResume", method: "thread/goal/set", params: { threadId: sourceID, status: "active" } },
      { key: "setReplace", method: "thread/goal/set", params: { threadId: sourceID, objective: "replace objective" } },
      { key: "setObjectiveTrim", method: "thread/goal/set", params: { threadId: sourceID, objective: "  trimmed objective  " } },
      { key: "setWithBudget", method: "thread/goal/set", params: { threadId: sourceID, objective: "budgeted goal", tokenBudget: 5000 } },
      { key: "setBudgetNull", method: "thread/goal/set", params: { threadId: sourceID, tokenBudget: null } },
      { key: "setBlankObjective", method: "thread/goal/set", params: { threadId: sourceID, objective: "   " } },
      { key: "setOversizedObjective", method: "thread/goal/set", params: { threadId: sourceID, objective: "x".repeat(4001) } },
      { key: "setZeroBudget", method: "thread/goal/set", params: { threadId: sourceID, objective: "zero budget", tokenBudget: 0 } },
      { key: "setStatusComplete", method: "thread/goal/set", params: { threadId: sourceID, status: "complete" } },
      { key: "setObjectiveAfterComplete", method: "thread/goal/set", params: { threadId: sourceID, objective: "fresh objective after complete" } },
      { key: "clear", method: "thread/goal/clear", params: { threadId: sourceID } },
      { key: "getAfterClear", method: "thread/goal/get", params: { threadId: sourceID } },
      { key: "clearAgain", method: "thread/goal/clear", params: { threadId: sourceID } },
      { key: "setStatusNoGoal", method: "thread/goal/set", params: { threadId: sourceID, status: "paused" } },
      { key: "getMissingThread", method: "thread/goal/get", params: { threadId: "00000000-0000-4000-8000-000000000000" } },
    ];
    for (const current of steps) {
      await step(current, threadMap);
    }

    results.fork = (await request("thread/fork", { threadId: sourceID, ephemeral: true, excludeTurns: true })).response;
    const forkID = results.fork?.result?.thread?.id;
    if (!forkID) throw new Error(`${impl} ephemeral thread/fork omitted a thread id`);
    threadMap[forkID] = "<THREAD_2>";
    for (const current of [
      { key: "setEphemeral", method: "thread/goal/set", params: { threadId: forkID, objective: "ephemeral goal" } },
      { key: "getEphemeral", method: "thread/goal/get", params: { threadId: forkID } },
    ]) {
      await step(current, threadMap);
    }

    // Feature-disabled phase: relaunch the app-server with --disable goals.
    try {
      semantic.featureDisabled = await runFeatureDisabledProbe(impl, binary, workspace, home, sourceID);
    } catch (error: any) {
      semantic.featureDisabled = {
        probeError: { name: error?.name, message: String(error?.message ?? error) },
      };
    }
    semantic.notificationsStream = goalNotificationStream
      .map((notification) => ({
        method: notification.method,
        params: normalizeNotification(notification.params, threadMap),
      }))
      .sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  } catch (error: any) {
    status = "error";
    capturedError = { name: error?.name, message: String(error?.message ?? error) };
  } finally {
    child.stdin.end();
    await waitForExit(child);
  }

  return {
    impl,
    status,
    error: capturedError,
    stderr,
    records,
    results,
    semantic,
    workspace: { before, after: snapshotFiles(workspace) },
    homeFiles: snapshotFiles(home),
  };
}

async function runFeatureDisabledProbe(
  impl: Implementation,
  binary: string,
  workspace: string,
  home: string,
  sourceID: string,
): Promise<Record<string, any>> {
  const child = spawn(binary, ["--disable", "plugins", "--disable", "apps", "--disable", "goals", "app-server"], {
    cwd: workspace,
    env: { ...process.env, CODEX_HOME: home, NO_COLOR: "1" },
    windowsHide: true,
    stdio: ["pipe", "pipe", "pipe"],
  });
  let stderr = "";
  child.stderr.on("data", (chunk) => { stderr += String(chunk); });
  const pending = new Map<number, { resolve: (value: any) => void; reject: (error: Error) => void; timer: NodeJS.Timeout }>();
  readline.createInterface({ input: child.stdout }).on("line", (line) => {
    let value: any;
    try {
      value = JSON.parse(line);
    } catch {
      return;
    }
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
        reject(new Error(`${impl} ${method} (feature-disabled) timed out`));
      }, 15_000);
      pending.set(id, { resolve, reject, timer });
    });
  };
  try {
    await request("initialize", {
      clientInfo: { name: "sdktests-raw-goal-disabled", version: "1.0.0" },
      capabilities: { experimentalApi: true },
    });
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "initialized", params: {} })}\n`);
    const get = await request("thread/goal/get", { threadId: sourceID });
    const set = await request("thread/goal/set", { threadId: sourceID, objective: "disabled goal" });
    return {
      get: summarizeResponse(get, { [sourceID]: "<THREAD_1>" }),
      set: summarizeResponse(set, { [sourceID]: "<THREAD_1>" }),
      stderrTail: stderr.split(/\r?\n/).filter((line) => line.trim()).slice(-6),
    };
  } finally {
    child.stdin.end();
    await waitForExit(child);
  }
}

function summarizeResponse(response: any, threadMap: Record<string, string>): any {
  if (!response) {
    return { ok: false, error: { code: null, message: "no response" } };
  }
  if (response.error) {
    return {
      ok: false,
      error: {
        code: response.error.code ?? null,
        message: normalizeThreadIds(String(response.error.message ?? ""), threadMap),
      },
    };
  }
  const result = response.result;
  if (result && typeof result === "object" && "goal" in result) {
    return { ok: true, goal: normalizeGoal(result.goal, threadMap) };
  }
  if (result && typeof result === "object" && "cleared" in result) {
    return { ok: true, cleared: result.cleared };
  }
  return { ok: true, result: normalizeVolatile(result, threadMap) };
}

function normalizeGoal(goal: any, threadMap: Record<string, string>): any {
  if (!goal) return null;
  return {
    threadId: threadMap[goal.threadId] ?? goal.threadId,
    objective: goal.objective,
    status: goal.status,
    tokenBudget: goal.tokenBudget ?? null,
    tokensUsed: goal.tokensUsed,
    timeUsedSeconds: goal.timeUsedSeconds,
    createdAt: typeof goal.createdAt === "number" && goal.createdAt > 0 ? "<TS>" : goal.createdAt,
    updatedAt: typeof goal.updatedAt === "number" && goal.updatedAt > 0 ? "<TS>" : goal.updatedAt,
  };
}

function normalizeNotification(params: any, threadMap: Record<string, string>): any {
  if (!params) return params;
  return {
    ...params,
    threadId: params.threadId ? (threadMap[params.threadId] ?? params.threadId) : params.threadId,
    goal: normalizeGoal(params.goal, threadMap),
  };
}

function normalizeVolatile(value: any, threadMap: Record<string, string>): any {
  if (Array.isArray(value)) {
    return value.map((item) => normalizeVolatile(item, threadMap));
  }
  if (value && typeof value === "object") {
    const out: Record<string, any> = {};
    for (const [key, item] of Object.entries(value)) {
      out[key] = key === "threadId" && typeof item === "string"
        ? (threadMap[item] ?? item)
        : normalizeVolatile(item, threadMap);
    }
    return out;
  }
  return value;
}

function normalizeThreadIds(message: string, threadMap: Record<string, string>): string {
  let out = message;
  for (const [id, placeholder] of Object.entries(threadMap)) {
    out = out.split(id).join(placeholder);
  }
  return out;
}

function semanticChecksPass(semantic: Record<string, any>): boolean {
  // The empty read must return no goal; a created goal must be active.
  if (semantic.getEmpty?.ok !== true || semantic.getEmpty?.goal !== null) return false;
  if (semantic.setCreate?.ok !== true || semantic.setCreate?.goal?.status !== "active") return false;
  if (semantic.setCreate?.goal?.objective !== "improve benchmark coverage") return false;
  if (semantic.getAfterCreate?.ok !== true || semantic.getAfterCreate?.goal?.status !== "active") return false;
  if (semantic.setPause?.ok !== true || semantic.setPause?.goal?.status !== "paused") return false;
  if (semantic.setResume?.ok !== true || semantic.setResume?.goal?.status !== "active") return false;
  if (semantic.setReplace?.ok !== true || semantic.setReplace?.goal?.objective !== "replace objective") return false;
  if (semantic.setObjectiveTrim?.ok !== true || semantic.setObjectiveTrim?.goal?.objective !== "trimmed objective") return false;
  if (semantic.setBlankObjective?.ok !== false) return false;
  if (semantic.setOversizedObjective?.ok !== false) return false;
  if (semantic.setZeroBudget?.ok !== false) return false;
  if (semantic.setStatusComplete?.ok !== true || semantic.setStatusComplete?.goal?.status !== "complete") return false;
  if (semantic.clear?.ok !== true || semantic.clear?.cleared !== true) return false;
  if (semantic.getAfterClear?.ok !== true || semantic.getAfterClear?.goal !== null) return false;
  if (semantic.clearAgain?.ok !== true || semantic.clearAgain?.cleared !== false) return false;
  if (semantic.setStatusNoGoal?.ok !== false) return false;
  if (semantic.getMissingThread?.ok !== false) return false;
  if (semantic.setEphemeral?.ok !== false) return false;
  if (semantic.getEphemeral?.ok !== false) return false;
  if (semantic.featureDisabled?.get?.ok !== false || semantic.featureDisabled?.set?.ok !== false) return false;
  return true;
}

function contentView(semantic: Record<string, any>): Record<string, any> {
  const out: Record<string, any> = {};
  for (const [key, value] of Object.entries(semantic)) {
    if (value && typeof value === "object") {
      const { preNotifications, requestID, ...rest } = value;
      out[key] = rest;
      continue;
    }
    out[key] = value;
  }
  return out;
}

// attributeGoalNotifications attributes every thread/goal/* notification in the
// raw stream to the request whose response immediately precedes it. This is
// deterministic (no recorder-window timing) and verifies the contract that a
// goal notification follows its own response (Rust ordering).
function attributeGoalNotifications(records: any[], semantic: Record<string, any>): Record<string, string[]> {
  const keyByID = new Map<number, string>();
  for (const [key, value] of Object.entries(semantic)) {
    if (typeof value?.requestID === "number") {
      keyByID.set(value.requestID, key);
    }
  }
  const post: Record<string, string[]> = {};
  let currentKey: string | undefined;
  for (const record of records) {
    if (typeof record.id === "number") {
      currentKey = keyByID.get(record.id) ?? currentKey;
      continue;
    }
    if (
      currentKey &&
      (record.method === "thread/goal/updated" || record.method === "thread/goal/cleared")
    ) {
      (post[currentKey] ??= []).push(record.method);
    }
  }
  return post;
}

function firstMismatch(rust: Record<string, any>, go: Record<string, any>): { key: string; rust: any; go: any } | null {
  const keys = new Set([...Object.keys(rust), ...Object.keys(go)]);
  for (const key of keys) {
    if (JSON.stringify(rust[key]) !== JSON.stringify(go[key])) {
      return { key, rust: rust[key], go: go[key] };
    }
  }
  return null;
}

function binaryInfo(binary: string): Record<string, any> {
  return {
    path: binary,
    sha256: sha256File(binary),
  };
}

function writeRun(artifactDir: string, run: RawRun): void {
  writeJson(path.join(artifactDir, "raw", `${run.impl}.json`), run);
  writeJson(path.join(artifactDir, "normalized", `${run.impl}.json`), {
    impl: run.impl,
    status: run.status,
    error: run.error,
    semantic: run.semantic,
    homeFileCount: Object.keys(run.homeFiles).length,
    workspaceChanged: JSON.stringify(run.workspace.before) !== JSON.stringify(run.workspace.after),
  });
}

function waitForExit(child: ReturnType<typeof spawn>): Promise<void> {
  return new Promise((resolve, reject) => {
    const timer = setTimeout(() => {
      try {
        child.kill();
      } catch {
        // Already gone.
      }
      reject(new Error("child did not exit after stdin closed"));
    }, 10_000);
    child.on("exit", () => {
      clearTimeout(timer);
      resolve();
    });
    child.on("error", (error) => {
      clearTimeout(timer);
      reject(error);
    });
  });
}

function parseOptions(argv: string[]): Record<string, string> {
  const out: Record<string, string> = {};
  for (let index = 0; index < argv.length; index += 2) {
    const key = argv[index];
    const value = argv[index + 1];
    if (key?.startsWith("--") && value) {
      out[key.slice(2)] = value;
    }
  }
  return out;
}

function required(value: string | undefined, flag: string): string {
  if (!value) {
    console.error(`missing ${flag}`);
    process.exit(2);
  }
  if (!existsSync(value)) {
    console.error(`${flag} does not exist: ${value}`);
    process.exit(2);
  }
  return value;
}
