import { spawn } from "node:child_process";
import { existsSync, mkdirSync, rmSync } from "node:fs";
import path from "node:path";
import readline from "node:readline";
import { pathToFileURL } from "node:url";
import { terminateProcessTree } from "./process_tree.ts";
import { copyCodexHome, copyFixture, maybeRunText, repoRoot, sdktestsRoot, sha256File, snapshotFiles, writeJson } from "./util.ts";

type Implementation = "rust" | "go";

const FORK_TOKEN = "FORK_TOKEN_9C4E";
const FORK_TURN1_OK = "FORK_TURN1_OK";

type RawRun = {
  impl: Implementation;
  status: "ok" | "error";
  error?: { name?: string; message: string };
  sdkEvents: any[];
  sdkTurns: any[];
  results: Record<string, any>;
  workspace: { before: Record<string, string>; after: Record<string, string> };
  homeFiles: Record<string, string>;
  semantic: Record<string, any>;
};

const options = parseOptions(process.argv.slice(2));
const rustPath = required(options.rust, "--rust");
const goPath = required(options.go, "--go");
const sdkPath = required(options.sdk, "--sdk");
const sdkIndex = path.join(sdkPath, "dist", "index.js");
if (!existsSync(sdkIndex)) {
  throw new Error(`SDK dist not found: ${sdkIndex}`);
}
const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
const artifactDir = path.join(sdktestsRoot, "artifacts", `${stamp}-raw_rolled_fork`);
const tmpDir = path.join(sdktestsRoot, ".tmp", `${stamp}-raw_rolled_fork`);
mkdirSync(path.join(artifactDir, "raw"), { recursive: true });
mkdirSync(path.join(artifactDir, "normalized"), { recursive: true });

const manifest = {
  generatedAt: new Date().toISOString(),
  driver: "sdk-turns-plus-raw-app-server-fork",
  scenario: "raw_rolled_fork",
  platform: { os: process.platform, arch: process.arch, node: process.version },
  goCommit: maybeRunText("git", ["rev-parse", "HEAD"], repoRoot),
  goDirty: Boolean(maybeRunText("git", ["status", "--short"], repoRoot)?.trim()),
  binaries: {
    rust: binaryInfo(rustPath),
    go: binaryInfo(goPath),
  },
  sdk: {
    path: sdkPath,
    distHash: sha256File(sdkIndex),
  },
  assertions: [
    "SDK turn 1 persists a rolled thread",
    "thread/fork on a rolled legacy thread is accepted without excludeTurns",
    "forked thread is persisted with a path",
    "forked thread carries the same turn count and turn-1 user content as the source",
    "source thread is unchanged after fork",
    "thread/list lists the app-server fork (vscode interactive source) and excludes the CLI-exec source under the Rust interactive default filter",
    "SDK resume of the forked thread recalls the turn-1 token",
    "side thread/fork creates an ephemeral pathless thread with excluded turns",
    "side thread/inject_items succeeds without changing the parent history",
    "an active side turn can be interrupted and completes with interrupted status",
    "side thread is absent from thread/list and can be unsubscribed after interruption",
  ],
};
writeJson(path.join(artifactDir, "run-manifest.json"), manifest);

let rust: RawRun | undefined;
let go: RawRun | undefined;
try {
  rust = await runImplementation("rust", rustPath);
  writeRun(artifactDir, rust);
  go = await runImplementation("go", goPath);
  writeRun(artifactDir, go);
  const equal = JSON.stringify(rust.semantic) === JSON.stringify(go.semantic);
  const sideInterruptCompatible = sideInterruptContract(rust.semantic?.side) && sideInterruptContract(go.semantic?.side);
  const passed = rust.status === "ok" && go.status === "ok" &&
    (equal || (sideInterruptCompatible && sameExceptSideInterrupt(rust.semantic, go.semantic))) &&
    semanticChecksPass(rust.semantic) && semanticChecksPass(go.semantic);
  const comparison = {
    status: passed ? "pass" : rust.status !== "ok" || go.status !== "ok" ? "infra_failure" : "behavior_mismatch",
    classification: passed ? "parity" : rust.status !== "ok" || go.status !== "ok" ? "infra-failure" : "go-bug",
    firstMismatch: equal ? null : firstMismatch(rust.semantic, go.semantic),
    rust: rust.semantic,
    go: go.semantic,
  };
  writeJson(path.join(artifactDir, "comparison.json"), comparison);
  console.log(`raw rolled-fork audit: ${comparison.status} (${comparison.classification})`);
  console.log(`raw rolled-fork artifact: ${artifactDir}`);
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
  console.error(`raw rolled-fork artifact: ${artifactDir}`);
  process.exitCode = 2;
} finally {
  try {
    await removeTemporaryRoot(tmpDir);
  } catch (error: any) {
    console.warn(`raw rolled-fork temporary cleanup failed: ${String(error?.message ?? error)}`);
  }
}

async function runImplementation(impl: Implementation, binary: string): Promise<RawRun> {
  const home = path.join(tmpDir, impl, "home");
  const workspace = path.join(tmpDir, impl, "workspace");
  mkdirSync(home, { recursive: true });
  copyCodexHome(path.join(requireUserHome(), ".codex"), home);
  copyFixture(workspace, "smoke");
  const before = snapshotFiles(workspace);
  const sdkEvents: any[] = [];
  const sdkTurns: any[] = [];
  const results: Record<string, any> = {};
  let status: RawRun["status"] = "ok";
  let capturedError: RawRun["error"] | undefined;
  try {
    const sdk = await import(pathToFileURL(sdkIndex).href);
    const env = { ...process.env, CODEX_HOME: home, NO_COLOR: "1" };
    const threadOptions = {
      sandboxMode: "read-only",
      skipGitRepoCheck: true,
      approvalPolicy: "never",
      networkAccessEnabled: false,
      webSearchMode: "disabled",
      workingDirectory: workspace,
    };
    const writer = new sdk.Codex({ codexPathOverride: binary, env });
    const thread = writer.startThread(threadOptions);
    const turn1 = await runSdkTurn(thread, `Remember the token ${FORK_TOKEN} and reply with exactly ${FORK_TURN1_OK}.`, sdkEvents, 240_000);
    sdkTurns.push(turn1.record);
    results.sdkTurn1 = turn1.record;
    const sourceID = thread.id;
    if (!sourceID) throw new Error(`${impl} SDK thread id missing after turn 1`);
    results.sourceID = sourceID;
    const forkFlow = await rawForkFlow(impl, binary, home, workspace, sourceID);
    results.forkFlow = forkFlow;
    Object.assign(results, forkFlow);
    const forkID = forkFlow?.fork?.result?.thread?.id;
    if (!forkID) throw new Error(`${impl} raw thread/fork omitted a fork thread id`);
    const forker = new sdk.Codex({ codexPathOverride: binary, env });
    const forkThread = forker.resumeThread(forkID, threadOptions);
    const resume = await runSdkTurn(forkThread, "Reply with the token I asked you to remember, and nothing else.", sdkEvents, 240_000);
    sdkTurns.push(resume.record);
    results.sdkResume = resume.record;
  } catch (error: any) {
    status = "error";
    capturedError = { name: error?.name, message: String(error?.message ?? error) };
  }
  const semantic = summarize(results);
  return {
    impl,
    status,
    error: capturedError,
    sdkEvents,
    sdkTurns,
    results,
    workspace: { before, after: snapshotFiles(workspace) },
    homeFiles: snapshotFiles(home),
    semantic,
  };
}

async function runSdkTurn(thread: any, prompt: string, events: any[], timeoutMs: number): Promise<{ record: any; final: string | null }> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  const turnEvents: any[] = [];
  let error: any = null;
  let final: string | null = null;
  try {
    const streamed = await thread.runStreamed(prompt, { signal: controller.signal });
    for await (const event of streamed.events) {
      events.push(event);
      turnEvents.push(event);
      if (event.type === "item.completed" && event.item?.type === "agent_message") {
        final = event.item.text ?? final;
      }
    }
  } catch (err: any) {
    error = { name: err?.name, message: String(err?.message ?? err) };
  } finally {
    clearTimeout(timer);
  }
  return {
    record: { threadId: thread.id, status: error ? "error" : "ok", error, final, events: turnEvents },
    final,
  };
}

async function rawForkFlow(impl: Implementation, binary: string, home: string, workspace: string, sourceID: string): Promise<Record<string, any>> {
  const results: Record<string, any> = {};
  const records: any[] = [];
  const child = spawn(binary, ["--disable", "plugins", "--disable", "apps", "app-server"], {
    cwd: workspace,
    env: { ...process.env, CODEX_HOME: home, NO_COLOR: "1" },
    windowsHide: true,
    stdio: ["pipe", "pipe", "pipe"],
  });
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
      }, 60_000);
      pending.set(id, { resolve, reject, timer });
    });
  };
  try {
    results.initialize = await request("initialize", {
      clientInfo: { name: "sdktests-raw-rolled-fork", version: "1.0.0" },
      capabilities: { experimentalApi: true },
    });
    child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", method: "initialized", params: {} })}\n`);
    results.fork = await request("thread/fork", { threadId: sourceID });
    const forkID = results.fork?.result?.thread?.id;
    if (!forkID) throw new Error(`${impl} thread/fork omitted a fork thread id`);
    results.forkRead = await request("thread/read", { threadId: forkID, includeTurns: true });
    results.sourceRead = await request("thread/read", { threadId: sourceID, includeTurns: true });
    results.list = await request("thread/list", {});
    results.sideFlow = await rawSideFlow(request, sourceID, results.sourceRead, records);
  } catch (error: any) {
    results.error = { name: error?.name, message: String(error?.message ?? error) };
  } finally {
    child.stdin.end();
    await waitForExit(child);
  }
  results.records = records;
  return results;
}

async function rawSideFlow(
  request: (method: string, params: any) => Promise<any>,
  sourceID: string,
  sourceReadBefore: any,
  records: any[],
): Promise<Record<string, any>> {
  const side: Record<string, any> = {};
  side.fork = await request("thread/fork", {
    threadId: sourceID,
    ephemeral: true,
    excludeTurns: true,
    developerInstructions: "SIDE_AUDIT_DEVELOPER_INSTRUCTIONS",
  });
  const sideID = side.fork?.result?.thread?.id;
  if (!sideID) throw new Error("thread/fork side omitted a thread id");
  side.inject = await request("thread/inject_items", {
    threadId: sideID,
    items: [{
      type: "message",
      role: "user",
      content: [{ type: "input_text", text: "SIDE_AUDIT_BOUNDARY" }],
    }],
  });
  side.turnStart = await request("turn/start", {
    threadId: sideID,
    input: [{
      type: "text",
      text: "Run exactly one shell command that waits for 30 seconds, then reply SIDE_TURN_FINISHED.",
    }],
    approvalPolicy: "never",
    sandboxPolicy: { type: "readOnly" },
  });
  const turnID = side.turnStart?.result?.turn?.id;
  if (!turnID) throw new Error("side turn/start omitted a turn id");
  side.interrupt = await request("turn/interrupt", { threadId: sideID, turnId: turnID });
  side.completed = await waitForRecord(records, (record) =>
    record?.method === "turn/completed" &&
    record?.params?.threadId === sideID &&
    record?.params?.turn?.id === turnID,
  );
  side.parentReadAfterInject = await request("thread/read", { threadId: sourceID, includeTurns: true });
  side.list = await request("thread/list", {});
  side.unsubscribe = await request("thread/unsubscribe", { threadId: sideID });
  side.sourceReadBeforeInject = sourceReadBefore;
  return side;
}

async function waitForRecord(records: any[], predicate: (record: any) => boolean): Promise<any> {
  for (let attempt = 0; attempt < 600; attempt++) {
    const match = records.find(predicate);
    if (match) return match;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
  throw new Error("timed out waiting for app-server notification");
}

function summarize(results: Record<string, any>): Record<string, any> {
  const forkResult = results.fork ?? results?.forkFlow?.fork ?? {};
  const fork = forkResult?.result?.thread;
  const forkRead = (results.forkRead ?? results?.forkFlow?.forkRead)?.result?.thread;
  const sourceRead = (results.sourceRead ?? results?.forkFlow?.sourceRead)?.result?.thread;
  const sideFlow = results.sideFlow ?? results?.forkFlow?.sideFlow ?? {};
  const side = sideFlow?.fork?.result?.thread;
  const sideParentAfter = sideFlow?.parentReadAfterInject?.result?.thread;
  const listed = Array.isArray((results.list ?? results?.forkFlow?.list)?.result?.data)
    ? (results.list ?? results?.forkFlow?.list).result.data
    : [];
  const turn1 = results.sdkTurn1;
  const resume = results.sdkResume;
  const forkTurns = Array.isArray(forkRead?.turns) ? forkRead.turns : [];
  const sourceTurns = Array.isArray(sourceRead?.turns) ? sourceRead.turns : [];
  const itemText = (item: any): string => {
    if (typeof item?.text === "string") return item.text;
    if (Array.isArray(item?.content)) {
      return item.content.map((content: any) => String(content?.text ?? "")).join(" ");
    }
    return "";
  };
  const turn1UserText = (turns: any[]) =>
    turns[0]?.items?.some((item: any) =>
      (String(item?.role ?? "").toLowerCase() === "user" || String(item?.type ?? "").toLowerCase() === "usermessage") &&
      itemText(item).includes(FORK_TOKEN),
    ) === true;
  return {
    sdkTurn1: {
      completed: turn1?.status === "ok",
      final: turn1?.final ?? null,
      threadIdSet: Boolean(results.sourceID),
    },
    fork: {
      accepted: Boolean(fork?.id),
      ephemeral: fork?.ephemeral ?? null,
      pathSet: Boolean(fork?.path),
      forkedFromIdSet: Boolean(fork?.forkedFromId),
      parentThreadIdIsNull: fork?.parentThreadId == null,
      historyMode: fork?.historyMode ?? null,
      source: fork?.source ?? null,
    },
    forkRead: {
      accepted: Boolean(forkRead?.id),
      turnCount: forkTurns.length,
      hasTurn1UserToken: turn1UserText(forkTurns),
      error: results.forkRead?.error ?? null,
    },
    sourceRead: {
      accepted: Boolean(sourceRead?.id),
      turnCount: sourceTurns.length,
      hasTurn1UserToken: turn1UserText(sourceTurns),
      error: results.sourceRead?.error ?? null,
    },
    list: {
      sourceCount: listed.filter((thread: any) => thread?.id === results.sourceID).length,
      forkCount: listed.filter((thread: any) => thread?.id === fork?.id).length,
    },
    resumeFork: {
      completed: resume?.status === "ok",
      final: resume?.final ?? null,
      recalledToken: resume?.final != null && String(resume.final).includes(FORK_TOKEN),
    },
    side: {
      accepted: Boolean(side?.id),
      ephemeral: side?.ephemeral ?? null,
      pathIsNull: side?.path == null,
      turnsEmpty: Array.isArray(side?.turns) && side.turns.length === 0,
      forkedFromIdSet: Boolean(side?.forkedFromId),
      parentThreadIdIsNull: side?.parentThreadId == null,
      historyMode: side?.historyMode ?? null,
      injectAccepted: sideFlow.inject?.error == null,
      turnStarted: Boolean(sideFlow.turnStart?.result?.turn?.id),
      interruptAccepted: sideFlow.interrupt?.error == null,
      terminalStatus: sideFlow.completed?.params?.turn?.status ?? null,
      parentUnchanged: JSON.stringify(sideFlow.sourceReadBeforeInject?.result?.thread ?? null) === JSON.stringify(sideParentAfter ?? null),
      listContainsSide: Array.isArray(sideFlow.list?.result?.data) && sideFlow.list.result.data.some((thread: any) => thread?.id === side?.id),
      unsubscribeStatus: sideFlow.unsubscribe?.result?.status ?? null,
    },
  };
}

function semanticChecksPass(value: Record<string, any>): boolean {
  return value.sdkTurn1?.completed === true &&
    value.sdkTurn1?.final === FORK_TURN1_OK &&
    value.sdkTurn1?.threadIdSet === true &&
    value.fork?.accepted === true &&
    value.fork?.ephemeral === false &&
    value.fork?.pathSet === true &&
    value.fork?.forkedFromIdSet === true &&
    value.fork?.parentThreadIdIsNull === true &&
    value.fork?.historyMode === "legacy" &&
    value.fork?.source === "vscode" &&
    value.forkRead?.accepted === true &&
    value.forkRead?.turnCount === 1 &&
    value.forkRead?.hasTurn1UserToken === true &&
    value.sourceRead?.accepted === true &&
    value.sourceRead?.turnCount === 1 &&
    value.sourceRead?.hasTurn1UserToken === true &&
    value.list?.sourceCount === 0 &&
    value.list?.forkCount === 1 &&
    value.resumeFork?.completed === true &&
    value.resumeFork?.recalledToken === true &&
    value.side?.accepted === true &&
    value.side?.ephemeral === true &&
    value.side?.pathIsNull === true &&
    value.side?.turnsEmpty === true &&
    value.side?.forkedFromIdSet === true &&
    value.side?.parentThreadIdIsNull === true &&
    value.side?.historyMode === "legacy" &&
    value.side?.injectAccepted === true &&
    value.side?.turnStarted === true &&
    sideInterruptContract(value.side) &&
    value.side?.parentUnchanged === true &&
    value.side?.listContainsSide === false &&
    value.side?.unsubscribeStatus === "unsubscribed";
}

function sideInterruptContract(side: any): boolean {
  if (side?.terminalStatus === "interrupted") return side.interruptAccepted === true;
  if (side?.terminalStatus === "completed") return side.interruptAccepted === false;
  return false;
}

function sameExceptSideInterrupt(rust: Record<string, any>, go: Record<string, any>): boolean {
  const left = JSON.parse(JSON.stringify(rust));
  const right = JSON.parse(JSON.stringify(go));
  for (const value of [left, right]) {
    delete value.side.interruptAccepted;
    delete value.side.terminalStatus;
  }
  return JSON.stringify(left) === JSON.stringify(right);
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
      void terminateProcessTree(child.pid ?? 0);
      fallbackTimer = setTimeout(finish, 2_000);
    }, 5_000);
    child.once("exit", finish);
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

function requireUserHome(): string {
  return process.env.USERPROFILE ?? process.env.HOME ?? ".";
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
