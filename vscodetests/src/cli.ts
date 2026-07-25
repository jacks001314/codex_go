import { createHash } from "node:crypto";
import { spawn } from "node:child_process";
import { copyFileSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";

type Message = Record<string, unknown>;
type Recording = {
  implementation: "rust" | "go";
  binary: string;
  status: "ok" | "error" | "timeout";
  error?: string;
  stderr: string;
  messages: Message[];
  responses: Record<string, Message>;
};

const root = path.resolve(import.meta.dirname, "..");
const args = parseArgs(process.argv.slice(3));
const command = process.argv[2] ?? "smoke";

try {
  if (command === "smoke") {
    const rust = required(args.rust, "--rust");
    const go = required(args.go, "--go");
    const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
    const artifact = path.join(root, "artifacts", `${stamp}-plugin-protocol-smoke`);
    mkdirSync(artifact, { recursive: true });
    const [rustRecording, goRecording] = await Promise.all([
      run("rust", rust, path.join(root, ".tmp", `${stamp}-rust`)),
      run("go", go, path.join(root, ".tmp", `${stamp}-go`)),
    ]);
    write(artifact, "raw-rust.json", rustRecording);
    write(artifact, "raw-go.json", goRecording);
    const comparison = compare(rustRecording, goRecording);
    write(artifact, "comparison.json", comparison);
    writeFileSync(path.join(artifact, "report.md"), report(artifact, comparison), "utf8");
    console.log(`vscodetests smoke: ${comparison.status}`);
    console.log(`vscodetests artifact: ${artifact}`);
    process.exitCode = comparison.status === "pass" ? 0 : 1;
  } else if (command === "live-turn") {
    const rust = required(args.rust, "--rust");
    const go = required(args.go, "--go");
    const auth = path.resolve(args.auth ?? path.join(process.env.HOME ?? "", ".codex", "auth.json"));
    if (!existsSync(auth)) throw new Error(`Auth file not found: ${auth}`);
    const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
    const artifact = path.join(root, "artifacts", `${stamp}-plugin-live-turn`);
    mkdirSync(artifact, { recursive: true });
    const rustRecording = await runLiveTurn("rust", rust, path.join(root, ".tmp", `${stamp}-live-rust`), auth);
    const goRecording = rustRecording.status === "ok"
      ? await runLiveTurn("go", go, path.join(root, ".tmp", `${stamp}-live-go`), auth)
      : { implementation: "go", binary: go, status: "error", error: "skipped because Rust baseline did not complete", stderr: "", messages: [], responses: {} } as Recording;
    write(artifact, "raw-rust.json", rustRecording); write(artifact, "raw-go.json", goRecording);
    const comparison = compareLiveTurn(rustRecording, goRecording);
    write(artifact, "comparison.json", comparison);
    writeFileSync(path.join(artifact, "report.md"), reportLiveTurn(artifact, comparison), "utf8");
    console.log(`vscodetests live-turn: ${comparison.status}`);
    console.log(`vscodetests artifact: ${artifact}`);
    process.exitCode = comparison.status === "pass" ? 0 : 1;
  } else if (command === "live-interrupt") {
    const rust = required(args.rust, "--rust"), go = required(args.go, "--go");
    const auth = path.resolve(args.auth ?? path.join(process.env.HOME ?? "", ".codex", "auth.json"));
    if (!existsSync(auth)) throw new Error(`Auth file not found: ${auth}`);
    const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", ""), artifact = path.join(root, "artifacts", `${stamp}-plugin-live-interrupt`);
    mkdirSync(artifact, { recursive: true });
    const rustRecording = await runLiveInterrupt("rust", rust, path.join(root, ".tmp", `${stamp}-interrupt-rust`), auth);
    const goRecording = await runLiveInterrupt("go", go, path.join(root, ".tmp", `${stamp}-interrupt-go`), auth);
    write(artifact, "raw-rust.json", rustRecording); write(artifact, "raw-go.json", goRecording);
    const comparison = compareLiveInterrupt(rustRecording, goRecording); write(artifact, "comparison.json", comparison);
    writeFileSync(path.join(artifact, "report.md"), reportLiveTurn(artifact, comparison), "utf8");
    console.log(`vscodetests live-interrupt: ${comparison.status}`); console.log(`vscodetests artifact: ${artifact}`);
    process.exitCode = comparison.status === "pass" ? 0 : 1;
  } else if (command === "command-exec") {
    const rust = required(args.rust, "--rust"), go = required(args.go, "--go");
    const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", ""), artifact = path.join(root, "artifacts", `${stamp}-plugin-command-exec`);
    mkdirSync(artifact, { recursive: true });
    const rustRecording = await runCommandExec("rust", rust, path.join(root, ".tmp", `${stamp}-exec-rust`));
    const goRecording = await runCommandExec("go", go, path.join(root, ".tmp", `${stamp}-exec-go`));
    write(artifact, "raw-rust.json", rustRecording); write(artifact, "raw-go.json", goRecording);
    const comparison = compareCommandExec(rustRecording, goRecording); write(artifact, "comparison.json", comparison);
    writeFileSync(path.join(artifact, "report.md"), reportLiveTurn(artifact, comparison), "utf8");
    console.log(`vscodetests command-exec: ${comparison.status}`); console.log(`vscodetests artifact: ${artifact}`);
    process.exitCode = comparison.status === "pass" ? 0 : 1;
  } else if (command === "report") {
    const artifact = path.resolve(required(args.artifact, "--artifact"));
    const comparison = JSON.parse(readFileSync(path.join(artifact, "comparison.json"), "utf8"));
    console.log(`vscodetests report: ${comparison.status}`);
    console.log(path.join(artifact, "report.md"));
    process.exitCode = comparison.status === "pass" ? 0 : 1;
  } else throw new Error(`Unknown command: ${command}`);
} catch (error: any) {
  console.error(error?.message ?? error);
  process.exitCode = 2;
}

async function runCommandExec(implementation: "rust" | "go", binary: string, home: string): Promise<Recording> {
  rmSync(home, { recursive: true, force: true }); mkdirSync(home, { recursive: true });
  const child = spawn(binary, ["app-server", "--stdio"], { env: { ...process.env, CODEX_HOME: home, NO_COLOR: "1" }, stdio: ["pipe", "pipe", "pipe"] });
  const messages: Message[] = [], responses: Record<string, Message> = {}; let stderr = "", buffer = "";
  child.stderr.on("data", (chunk) => { stderr += String(chunk); }); child.stdout.on("data", (chunk) => { buffer += String(chunk); for (;;) { const end = buffer.indexOf("\n"); if (end < 0) break; const line = buffer.slice(0, end).trim(); buffer = buffer.slice(end + 1); if (!line) continue; try { const message = JSON.parse(line); messages.push(message); if (message.id !== undefined) responses[String(message.id)] = message; } catch { messages.push({ parseError: true, line }); } } });
  const send = (id: number, method: string, params: unknown) => child.stdin.write(`${JSON.stringify({ id, method, params })}\n`);
  try {
    send(0, "initialize", { clientInfo: { name: "codex_vscode", version: "vscodetests" }, capabilities: { experimentalApi: true } }); await waitFor(() => responses["0"], 5000); child.stdin.write(`${JSON.stringify({ method: "initialized", params: {} })}\n`);
    send(1, "command/exec", { command: ["sh", "-c", "printf VSCODE_EXEC_OUT; printf VSCODE_EXEC_ERR >&2"], cwd: root });
    send(2, "command/exec", { command: ["sh", "-c", "printf VSCODE_FAIL_OUT; printf VSCODE_FAIL_ERR >&2; exit 7"], cwd: root });
    send(3, "command/exec", { command: ["sh", "-c", "printf VSCODE_STREAM_OUT; printf VSCODE_STREAM_ERR >&2; exit 9"], cwd: root, processId: "vscode-stream", streamStdoutStderr: true });
    await waitFor(() => responses["1"] && responses["2"] && responses["3"], 20000); child.stdin.end(); await new Promise((resolve) => { const timer = setTimeout(() => { child.kill("SIGKILL"); resolve(null); }, 3000); child.once("exit", () => { clearTimeout(timer); resolve(null); }); });
    return { implementation, binary, status: "ok", stderr, messages, responses };
  } catch (error: any) { child.kill("SIGKILL"); await new Promise((resolve) => child.once("exit", resolve)); return { implementation, binary, status: "timeout", error: error?.message ?? String(error), stderr, messages, responses }; }
}

function compareCommandExec(rust: Recording, go: Recording) {
  const checks: string[] = [];
  for (const recording of [rust, go]) {
    if (recording.status !== "ok") { checks.push(`${recording.implementation} status ${recording.status}: ${recording.error}`); continue; }
    const success = recording.responses["1"] as any, failure = recording.responses["2"] as any;
    if (success?.error || success?.result?.exitCode !== 0 || success?.result?.stdout !== "VSCODE_EXEC_OUT" || success?.result?.stderr !== "VSCODE_EXEC_ERR") checks.push(`${recording.implementation} command/exec success response mismatch`);
    if (failure?.error || failure?.result?.exitCode !== 7 || failure?.result?.stdout !== "VSCODE_FAIL_OUT" || failure?.result?.stderr !== "VSCODE_FAIL_ERR") checks.push(`${recording.implementation} command/exec nonzero response mismatch`);
    const streamed = recording.responses["3"] as any;
    if (streamed?.error || streamed?.result?.exitCode !== 9 || streamed?.result?.stdout !== "" || streamed?.result?.stderr !== "") checks.push(`${recording.implementation} streaming command response mismatch`);
    const deltas = recording.messages.filter((m: any) => m.method === "command/exec/outputDelta" && m.params?.processId === "vscode-stream") as any[];
    const decoded = (stream: string) => deltas.filter((m) => m.params?.stream === stream).map((m) => Buffer.from(m.params?.deltaBase64 ?? "", "base64").toString("utf8")).join("");
    if (!decoded("stdout").includes("VSCODE_STREAM_OUT") || !decoded("stderr").includes("VSCODE_STREAM_ERR")) checks.push(`${recording.implementation} streaming outputDelta content mismatch`);
    const responseIndex = recording.messages.indexOf(streamed), lastDeltaIndex = Math.max(...deltas.map((m) => recording.messages.indexOf(m)));
    if (deltas.length === 0 || responseIndex <= lastDeltaIndex) checks.push(`${recording.implementation} command/exec response did not follow output deltas`);
  }
  return { status: checks.length ? "mismatch" : "pass", checks };
}

async function runLiveInterrupt(implementation: "rust" | "go", binary: string, home: string, auth: string): Promise<Recording> {
  rmSync(home, { recursive: true, force: true }); mkdirSync(home, { recursive: true }); copyFileSync(auth, path.join(home, "auth.json"));
  const child = spawn(binary, ["app-server", "--stdio"], { env: { ...process.env, CODEX_HOME: home, NO_COLOR: "1" }, stdio: ["pipe", "pipe", "pipe"] });
  const messages: Message[] = [], responses: Record<string, Message> = {}; let stderr = "", buffer = "";
  child.stderr.on("data", (chunk) => { stderr += String(chunk); }); child.stdout.on("data", (chunk) => { buffer += String(chunk); for (;;) { const end = buffer.indexOf("\n"); if (end < 0) break; const line = buffer.slice(0, end).trim(); buffer = buffer.slice(end + 1); if (!line) continue; try { const message = JSON.parse(line); messages.push(message); if (message.id !== undefined) responses[String(message.id)] = message; } catch { messages.push({ parseError: true, line }); } } });
  const send = (id: number, method: string, params: unknown) => child.stdin.write(`${JSON.stringify({ id, method, params })}\n`);
  try {
    send(0, "initialize", { clientInfo: { name: "codex_vscode", version: "vscodetests" }, capabilities: { experimentalApi: true } }); await waitFor(() => responses["0"], 5000); child.stdin.write(`${JSON.stringify({ method: "initialized", params: {} })}\n`);
    send(1, "thread/start", { cwd: root, sandbox: "read-only", approvalPolicy: "never" }); await waitFor(() => responses["1"], 10000); const threadID = (responses["1"] as any)?.result?.thread?.id;
    send(2, "turn/start", { threadId: threadID, input: [{ type: "text", text: "Wait until interrupted.", text_elements: [] }], cwd: root, approvalPolicy: "never", sandboxPolicy: { type: "readOnly", networkAccess: false } }); await waitFor(() => responses["2"], 10000);
    const turnID = (responses["2"] as any)?.result?.turn?.id; await waitFor(() => messages.some((m: any) => m.method === "turn/started" && m.params?.turn?.id === turnID), 20000);
    send(3, "turn/interrupt", { threadId: threadID, turnId: turnID }); await waitFor(() => responses["3"], 10000); await waitFor(() => messages.some((m: any) => m.method === "turn/completed" && m.params?.turn?.id === turnID), 20000);
    child.stdin.end(); await new Promise((resolve) => { const timer = setTimeout(() => { child.kill("SIGKILL"); resolve(null); }, 3000); child.once("exit", () => { clearTimeout(timer); resolve(null); }); });
    return { implementation, binary, status: "ok", stderr, messages, responses };
  } catch (error: any) { child.kill("SIGKILL"); await new Promise((resolve) => child.once("exit", resolve)); return { implementation, binary, status: "timeout", error: error?.message ?? String(error), stderr, messages, responses }; }
}

function compareLiveInterrupt(rust: Recording, go: Recording) {
  const checks: string[] = [];
  for (const recording of [rust, go]) {
    if (recording.status !== "ok") { checks.push(`${recording.implementation} status ${recording.status}: ${recording.error}`); continue; }
    for (const id of ["0", "1", "2", "3"]) if (!(recording.responses[id] as any) || (recording.responses[id] as any).error) checks.push(`${recording.implementation} response ${id} missing or errored`);
    const started = recording.messages.find((m: any) => m.method === "turn/started") as any, completed = recording.messages.find((m: any) => m.method === "turn/completed") as any;
    if (!started || !completed || started.params?.turn?.id !== completed.params?.turn?.id || started.params?.threadId !== completed.params?.threadId) checks.push(`${recording.implementation} interrupt lifecycle IDs are not linked`);
    if (completed?.params?.turn?.status !== "interrupted") checks.push(`${recording.implementation} terminal status is ${JSON.stringify(completed?.params?.turn?.status)}`);
    const settings = recording.messages.find((m: any) => m.method === "thread/settings/updated") as any;
    if (settings && (settings.params?.threadSettings?.approvalPolicy !== "never" || settings.params?.threadSettings?.sandboxPolicy?.type !== "readOnly")) checks.push(`${recording.implementation} emitted stale permission settings during turn/start`);
    const responseIndex = recording.messages.indexOf(recording.responses["3"]), completedIndex = recording.messages.indexOf(completed as Message);
    if (responseIndex < 0 || completedIndex < 0 || responseIndex > completedIndex) checks.push(`${recording.implementation} interrupt response arrived after turn/completed`);
  }
  return { status: checks.length ? "mismatch" : "pass", checks };
}

async function runLiveTurn(implementation: "rust" | "go", binary: string, home: string, auth: string): Promise<Recording> {
  rmSync(home, { recursive: true, force: true }); mkdirSync(home, { recursive: true }); copyFileSync(auth, path.join(home, "auth.json"));
  const child = spawn(binary, ["app-server", "--stdio"], { env: { ...process.env, CODEX_HOME: home, NO_COLOR: "1" }, stdio: ["pipe", "pipe", "pipe"] });
  const messages: Message[] = [], responses: Record<string, Message> = {}; let stderr = "", buffer = "";
  child.stderr.on("data", (chunk) => { stderr += String(chunk); });
  child.stdout.on("data", (chunk) => { buffer += String(chunk); for (;;) { const end = buffer.indexOf("\n"); if (end < 0) break; const line = buffer.slice(0, end).trim(); buffer = buffer.slice(end + 1); if (!line) continue; try { const message = JSON.parse(line); messages.push(message); if (message.id !== undefined) responses[String(message.id)] = message; } catch { messages.push({ parseError: true, line }); } } });
  const send = (id: number, method: string, params: unknown) => child.stdin.write(`${JSON.stringify({ id, method, params })}\n`);
  try {
    send(0, "initialize", { clientInfo: { name: "codex_vscode", title: "Codex VS Code Extension", version: "vscodetests" }, capabilities: { experimentalApi: true } });
    await waitFor(() => responses["0"], 5000); child.stdin.write(`${JSON.stringify({ method: "initialized", params: {} })}\n`);
    send(1, "thread/start", { cwd: root, sandbox: "read-only", approvalPolicy: "never" }); await waitFor(() => responses["1"], 10000);
    const threadID = (responses["1"] as any)?.result?.thread?.id; if (!threadID) throw new Error(`${implementation} live thread/start omitted thread id`);
    send(2, "turn/start", { threadId: threadID, input: [{ type: "text", text: "Reply with exactly: VSCODE_SMOKE_OK", text_elements: [] }], cwd: root, approvalPolicy: "never", sandboxPolicy: { type: "readOnly", networkAccess: false } });
    await waitFor(() => responses["2"], 10000); await waitFor(() => messages.some((m: any) => m.method === "turn/completed"), 120000);
    child.stdin.end(); const exited = await new Promise<boolean>((resolve) => { const timer = setTimeout(() => { child.kill("SIGKILL"); resolve(false); }, 3000); child.once("exit", () => { clearTimeout(timer); resolve(true); }); });
    return { implementation, binary, status: exited ? "ok" : "error", error: exited ? undefined : "process did not exit", stderr, messages, responses };
  } catch (error: any) {
    child.kill("SIGKILL");
    await new Promise((resolve) => child.once("exit", resolve));
    return { implementation, binary, status: "timeout", error: error?.message ?? String(error), stderr, messages, responses };
  }
}

function compareLiveTurn(rust: Recording, go: Recording) {
  const checks: string[] = [];
  for (const recording of [rust, go]) {
    if (recording.status !== "ok") { checks.push(`${recording.implementation} status ${recording.status}: ${recording.error ?? "unknown error"}`); continue; }
    for (const id of ["0", "1", "2"]) if (!(recording.responses[id] as any) || (recording.responses[id] as any).error) checks.push(`${recording.implementation} response ${id} missing or errored`);
    const started = recording.messages.find((m: any) => m.method === "turn/started") as any;
    const completed = recording.messages.find((m: any) => m.method === "turn/completed") as any;
    const userStarted = recording.messages.find((m: any) => m.method === "item/started" && m.params?.item?.type === "userMessage") as any;
    const agentCompleted = recording.messages.find((m: any) => m.method === "item/completed" && m.params?.item?.type === "agentMessage") as any;
    if (!started || !completed) checks.push(`${recording.implementation} omitted turn terminal lifecycle`);
    if (started?.params?.threadId !== completed?.params?.threadId || started?.params?.turn?.id !== completed?.params?.turn?.id) checks.push(`${recording.implementation} turn IDs are not linked`);
    if (completed?.params?.turn?.status !== "completed") checks.push(`${recording.implementation} turn status is ${JSON.stringify(completed?.params?.turn?.status)}`);
    if (!userStarted) checks.push(`${recording.implementation} omitted userMessage item/started`);
    if (!agentCompleted) checks.push(`${recording.implementation} omitted agentMessage item/completed`);
    const text = JSON.stringify(agentCompleted?.params?.item?.content ?? agentCompleted?.params?.item ?? "");
    if (!text.includes("VSCODE_SMOKE_OK")) checks.push(`${recording.implementation} agent result missed smoke marker`);
  }
  return { status: checks.length ? "mismatch" : "pass", checks, rustMethods: rust.messages.map((m: any) => m.method ?? "response"), goMethods: go.messages.map((m: any) => m.method ?? "response") };
}

function reportLiveTurn(artifact: string, comparison: any) { return `# VS Code Live Turn Smoke\n\nStatus: ${comparison.status}\n\nArtifact: ${artifact}\n\n## Checks\n\n${comparison.checks.length ? comparison.checks.map((x: string) => `- FAIL ${x}`).join("\n") : "- PASS turn response, user/agent items, linked lifecycle, completed status and semantic marker"}\n`; }

async function run(implementation: "rust" | "go", binary: string, home: string): Promise<Recording> {
  rmSync(home, { recursive: true, force: true });
  mkdirSync(home, { recursive: true });
  const child = spawn(binary, ["app-server", "--stdio"], {
    env: { ...process.env, CODEX_HOME: home, NO_COLOR: "1" }, stdio: ["pipe", "pipe", "pipe"],
  });
  const messages: Message[] = [], responses: Record<string, Message> = {};
  let stderr = "", buffer = "";
  child.stderr.on("data", (chunk) => { stderr += String(chunk); });
  child.stdout.on("data", (chunk) => {
    buffer += String(chunk);
    for (;;) {
      const end = buffer.indexOf("\n"); if (end < 0) break;
      const line = buffer.slice(0, end).trim(); buffer = buffer.slice(end + 1);
      if (!line) continue;
      try { const message = JSON.parse(line); messages.push(message); if (message.id !== undefined) responses[String(message.id)] = message; }
      catch { messages.push({ parseError: true, line }); }
    }
  });
  const send = (id: number, method: string, params: unknown) => child.stdin.write(`${JSON.stringify({ id, method, params })}\n`);
  send(0, "initialize", { clientInfo: { name: "codex_vscode", title: "Codex VS Code Extension", version: "vscodetests" }, capabilities: { experimentalApi: true } });
  await waitFor(() => responses["0"], 5000);
  child.stdin.write(`${JSON.stringify({ method: "initialized", params: {} })}\n`);
  send(1, "config/read", {}); send(2, "model/list", {});
  send(3, "thread/start", { cwd: root, sandbox: "read-only", approvalPolicy: "never" });
  await waitFor(() => responses["1"] && responses["2"] && responses["3"], 10000);
  const threadID = (responses["3"] as any)?.result?.thread?.id;
  if (!threadID) throw new Error(`${implementation} thread/start omitted thread id`);
  send(4, "thread/read", { threadId: threadID, includeTurns: true });
  send(5, "thread/list", { limit: 10 });
  send(6, "thread/resume", { threadId: threadID, cwd: root, sandbox: "read-only", approvalPolicy: "never" });
  await waitFor(() => responses["4"] && responses["5"] && responses["6"], 10000);
  child.stdin.end();
  const exited = await new Promise<boolean>((resolve) => { const timer = setTimeout(() => { child.kill("SIGKILL"); resolve(false); }, 3000); child.once("exit", () => { clearTimeout(timer); resolve(true); }); });
  return { implementation, binary, status: exited && ["0", "1", "2", "3", "4", "5", "6"].every((id) => responses[id]) ? "ok" : "error", error: exited ? undefined : "process did not exit", stderr, messages, responses };
}

function compare(rust: Recording, go: Recording) {
  const checks: string[] = [];
  for (const recording of [rust, go]) for (const id of ["0", "1", "2", "3", "4", "5", "6"]) if (!recording.responses[id]) checks.push(`${recording.implementation} response ${id} missing`);
  for (const recording of [rust, go]) for (const id of ["0", "1", "2", "3", "5"]) if ((recording.responses[id] as any)?.error) checks.push(`${recording.implementation} response ${id} errored unexpectedly`);
  for (const recording of [rust, go]) {
    const started = recording.messages.find((m) => m.method === "thread/started") as any;
    const start = recording.responses["3"] as any;
    const resultThread = start?.result?.thread;
    const notifiedThread = started?.params?.thread;
    if (!started) checks.push(`${recording.implementation} missing thread/started notification`);
    if (!resultThread?.id || notifiedThread?.id !== resultThread.id) checks.push(`${recording.implementation} thread/start ID is not linked to thread/started`);
    if (resultThread?.sessionId !== resultThread?.id) checks.push(`${recording.implementation} thread sessionId differs from id`);
    if (start?.result?.approvalPolicy !== "never") checks.push(`${recording.implementation} did not preserve approvalPolicy=never`);
    if (start?.result?.sandbox?.type !== "readOnly" || start?.result?.sandbox?.networkAccess !== false) checks.push(`${recording.implementation} returned an incompatible read-only sandbox shape`);
    if (resultThread?.source !== "vscode") checks.push(`${recording.implementation} thread source is ${JSON.stringify(resultThread?.source)}, expected vscode`);
    if (typeof resultThread?.cliVersion !== "string" || resultThread.cliVersion.length === 0) checks.push(`${recording.implementation} thread cliVersion is empty`);
    const order = recording.messages.indexOf(started as Message);
    const responseOrder = recording.messages.indexOf(start as Message);
    if (order < 0 || responseOrder < 0 || order === responseOrder) checks.push(`${recording.implementation} thread lifecycle order is unavailable`);
    const readResponse = recording.responses["4"] as any;
    const listedThreads = (recording.responses["5"] as any)?.result?.data;
    const resumeResponse = recording.responses["6"] as any;
    if (readResponse?.error?.code !== -32600 || !String(readResponse?.error?.message ?? "").includes("not materialized")) checks.push(`${recording.implementation} thread/read did not reject an unmaterialized thread like Rust`);
    if (!Array.isArray(listedThreads) || listedThreads.some((thread: any) => thread?.id === resultThread?.id)) checks.push(`${recording.implementation} thread/list exposed an unmaterialized thread`);
    if (resumeResponse?.error?.code !== -32600 || !String(resumeResponse?.error?.message ?? "").includes("no rollout found")) checks.push(`${recording.implementation} thread/resume did not reject an unmaterialized thread like Rust`);
  }
  return { status: checks.length === 0 ? "pass" : "mismatch", checks, rustMessageMethods: rust.messages.map((m) => String(m.method ?? "response")), goMessageMethods: go.messages.map((m) => String(m.method ?? "response")) };
}

function report(artifact: string, comparison: any) { return `# VS Code Protocol Smoke\n\nStatus: ${comparison.status}\n\nArtifact: ${artifact}\n\n## Checks\n\n${comparison.checks.length ? comparison.checks.map((x: string) => `- FAIL ${x}`).join("\n") : "- PASS initialize, config/model discovery, thread start/read/list/resume, lifecycle linkage and permission shapes"}\n`; }
function write(directory: string, name: string, value: unknown) { writeFileSync(path.join(directory, name), `${JSON.stringify(value, null, 2)}\n`, "utf8"); }
function required(value: string | undefined, flag: string) { if (!value) throw new Error(`Missing ${flag}`); return value; }
function parseArgs(values: string[]) { const out: Record<string, string> = {}; for (let i = 0; i < values.length; i += 2) if (values[i]?.startsWith("--")) out[values[i].slice(2)] = values[i + 1]; return out; }
async function waitFor(predicate: () => unknown, timeout: number) { const end = Date.now() + timeout; while (!predicate()) { if (Date.now() > end) throw new Error("protocol response timeout"); await new Promise((resolve) => setTimeout(resolve, 20)); } }
