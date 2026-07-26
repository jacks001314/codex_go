import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync } from "node:fs";
import path from "node:path";
import readline from "node:readline";

const binary = path.resolve(process.argv[2] ?? "");
const artifactDir = path.resolve(process.argv[3] ?? "");
const cwd = path.resolve(process.argv[4] ?? process.cwd());
if (!process.argv[2] || !process.argv[3]) {
  throw new Error("usage: appserver_code_mode_live.ts CODEX ARTIFACT_DIR [CWD]");
}

mkdirSync(artifactDir, { recursive: true });
const child = spawn(binary, ["--enable", "code_mode", "--enable", "unified_exec", "app-server"], {
  cwd,
  env: { ...process.env, NO_COLOR: "1" },
  windowsHide: true,
  stdio: ["pipe", "pipe", "pipe"],
});
const records: any[] = [];
let stderr = "";
child.stderr.on("data", (chunk) => { stderr += String(chunk); });
const pending = new Map<number, { resolve: (value: any) => void; reject: (error: Error) => void }>();
let terminalResolve: ((value: any) => void) | undefined;
const terminal = new Promise<any>((resolve) => { terminalResolve = resolve; });

readline.createInterface({ input: child.stdout }).on("line", (line) => {
  let value: any;
  try { value = JSON.parse(line); } catch { records.push({ parseError: line }); return; }
  records.push(value);
  if (typeof value.id === "number" && pending.has(value.id)) {
    const waiter = pending.get(value.id)!;
    pending.delete(value.id);
    if (value.error) waiter.reject(new Error(JSON.stringify(value.error)));
    else waiter.resolve(value.result);
  }
  if (value.method === "turn/completed" || value.method === "turn/failed") terminalResolve?.(value);
});

let nextID = 1;
function request(method: string, params: any): Promise<any> {
  const id = nextID++;
  child.stdin.write(`${JSON.stringify({ jsonrpc: "2.0", id, method, params })}\n`);
  return new Promise((resolve, reject) => pending.set(id, { resolve, reject }));
}

const timeout = setTimeout(() => terminalResolve?.({ method: "harness/timeout" }), 180_000);
let result: any;
try {
  await request("initialize", {
    clientInfo: { name: "sdktests-appserver-live", version: "1.0.0" },
    capabilities: { experimentalApi: true },
  });
  const started = await request("thread/start", {
    cwd,
    approvalPolicy: "never",
    sandbox: "danger-full-access",
    experimentalRawEvents: true,
    config: { features: { code_mode: true, unified_exec: true } },
  });
  const threadID = started?.thread?.id;
  if (!threadID) throw new Error(`thread/start omitted thread id: ${JSON.stringify(started)}`);
  const prompt = "Use the exec tool exactly once with this exact JavaScript: const r = await tools.exec_command({\"cmd\":\"Write-Output WEATHER_COMMAND_PROCESS_OK\"}); text(r.output); Then reply exactly WEATHER_TOOL_DONE. Do not use any other tool or send commentary.";
  const turnStarted = await request("turn/start", {
    threadId: threadID,
    input: [{ type: "text", text: prompt }],
    cwd,
    approvalPolicy: "never",
    sandboxPolicy: "danger-full-access",
    developerInstructions: "You must follow the user's exact tool instruction. Call the visible exec tool once, and inside it await tools.exec_command exactly once before the final answer.",
    config: { features: { code_mode: true, unified_exec: true } },
  });
  const turnID = turnStarted?.turn?.id;
  const terminalEvent = await terminal;
  const commandStarted = records.filter((entry) =>
    entry.method === "item/started" && entry.params?.turnId === turnID &&
    entry.params?.item?.type === "commandExecution" && String(entry.params.item.command).includes("WEATHER_COMMAND_PROCESS_OK"));
  const commandCompleted = records.filter((entry) =>
    entry.method === "item/completed" && entry.params?.turnId === turnID &&
    entry.params?.item?.type === "commandExecution" && String(entry.params.item.command).includes("WEATHER_COMMAND_PROCESS_OK"));
  const finalCompleted = records.filter((entry) =>
    entry.method === "item/completed" && entry.params?.turnId === turnID &&
    entry.params?.item?.type === "agentMessage" && String(entry.params.item.text).trim() === "WEATHER_TOOL_DONE");
  const finalIDs = new Set(finalCompleted.map((entry) => entry.params.item.id));
  const finalDeltas = records.filter((entry) =>
    entry.method === "item/agentMessage/delta" && entry.params?.turnId === turnID && finalIDs.has(entry.params?.itemId));
  const finalDeltaText = finalDeltas.map((entry) => String(entry.params?.delta ?? "")).join("");
  const indices = {
    commandStarted: records.indexOf(commandStarted[0]),
    commandCompleted: records.indexOf(commandCompleted[0]),
    finalCompleted: records.indexOf(finalCompleted[0]),
    turnCompleted: records.indexOf(terminalEvent),
  };
  const checks = {
    turnCompleted: terminalEvent.method === "turn/completed",
    oneCommandStarted: commandStarted.length === 1,
    oneCommandCompleted: commandCompleted.length === 1,
    commandLifecycleUsesSameItemID: commandStarted.length === 1 && commandCompleted.length === 1 &&
      commandStarted[0].params.item.id === commandCompleted[0].params.item.id,
    commandOutputMatches: commandCompleted.length === 1 &&
      String(commandCompleted[0].params.item.aggregatedOutput).includes("WEATHER_COMMAND_PROCESS_OK"),
    oneFinalCompleted: finalCompleted.length === 1,
    finalDeltaUsesCompletedItemID: finalDeltas.length > 0 && finalDeltaText === "WEATHER_TOOL_DONE",
    lifecycleOrder: indices.commandStarted >= 0 && indices.commandStarted < indices.commandCompleted &&
      indices.commandCompleted < indices.finalCompleted && indices.finalCompleted < indices.turnCompleted,
  };
  result = { threadID, turnID, terminalEvent, checks, indices, commandStarted, commandCompleted, finalCompleted, finalDeltas, finalDeltaText, records, stderr };
  if (!Object.values(checks).every(Boolean)) process.exitCode = 1;
} catch (error: any) {
  result = { error: { name: error?.name, message: String(error?.message ?? error) }, records, stderr };
  process.exitCode = 1;
} finally {
  clearTimeout(timeout);
  writeFileSync(path.join(artifactDir, "appserver-code-mode-live.json"), `${JSON.stringify(result, null, 2)}\n`, "utf8");
  child.stdin.end();
  const exitTimer = setTimeout(() => child.kill(), 5_000);
  await new Promise<void>((resolve) => child.once("exit", () => resolve()));
  clearTimeout(exitTimer);
}

console.log(JSON.stringify({ artifactDir, checks: result?.checks, error: result?.error }));
