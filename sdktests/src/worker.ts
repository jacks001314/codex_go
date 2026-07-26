import { existsSync, readFileSync, readdirSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const [inputPath, outputPath] = process.argv.slice(2);
if (!inputPath || !outputPath) throw new Error("worker requires input and output paths");
const input = JSON.parse(readFileSync(inputPath, "utf8"));
const sdk = await import(pathToFileURL(input.sdkIndex).href);
const started = Date.now();
const events: any[] = [];
const turns: any[] = [];
let status = "ok";
let error: any = null;
let currentThreadId: string | null = null;

function checkpoint(extra: Record<string, unknown> = {}) {
  writeFileSync(outputPath, `${JSON.stringify({
    impl: input.impl, status, error, durationMs: Date.now() - started,
    threadId: currentThreadId, threadIds: turns.map((turn) => turn.threadId),
    turns, authAvailable: input.authFilesCopied.includes("auth.json"),
    sandboxStateCopied: input.sandboxStateCopied, events, ...extra,
  }, null, 2)}\n`, "utf8");
}

function rolloutJsonl(): string {
  const root = path.join(input.envHome, "sessions");
  if (!existsSync(root)) return "";
  const files: string[] = [];
  const visit = (directory: string) => {
    for (const name of readdirSync(directory)) {
      const candidate = path.join(directory, name);
      if (statSync(candidate).isDirectory()) visit(candidate);
      else if (name.endsWith(".jsonl")) files.push(candidate);
    }
  };
  visit(root);
  return files.map((file) => readFileSync(file, "utf8")).find((text) => !currentThreadId || text.includes(currentThreadId)) ?? "";
}

try {
  const client = new sdk.Codex({
    codexPathOverride: input.codexPath,
    env: {
      ...process.env,
      CODEX_HOME: input.envHome,
      NO_COLOR: "1",
      CODEX_GO_RESPONSES_DEBUG: "1",
      CODEX_GO_RESPONSES_DEBUG_FILE: path.join(input.envHome, "responses-debug.jsonl"),
    },
    config: input.scenario.codexConfig,
  });
  const baseOptions = {
    ...input.scenario.threadOptions,
    workingDirectory: input.workingDirectory,
    additionalDirectories: input.additionalDirectories,
  };
  let thread = client.startThread(baseOptions);
  checkpoint({ workerPid: process.pid });
  async function runTurn(index: number, activeThread: any) {
    const turnSpec = input.scenario.turns[index];
    const turnOptions = { ...baseOptions, ...(turnSpec.threadOptions ?? {}) };
    if (turnSpec.workingDirectoryMode === "missing") {
      turnOptions.workingDirectory = input.missingWorkingDirectory;
    } else if (turnSpec.workingDirectoryMode === "fixture") {
      turnOptions.workingDirectory = input.workingDirectory;
    }
    if (turnSpec.additionalDirectoryMode === "none") turnOptions.additionalDirectories = undefined;
    if (turnSpec.additionalDirectoryMode === "fixture") turnOptions.additionalDirectories = input.additionalDirectories;
    if (index > 0 && turnSpec.resume) {
      if (!activeThread.id) throw new Error("thread ID is unavailable before resume");
      activeThread = client.resumeThread(activeThread.id, turnOptions);
    }
    const controller = new AbortController();
    if (input.scenario.abortBeforeRun) controller.abort();
    const timer = setTimeout(() => controller.abort(), turnSpec.timeoutMs ?? input.scenario.timeoutMs);
    const turnEvents: any[] = [];
    let turnError: any = null;
    try {
      const prompt = turnSpec.prompt.replaceAll("{{WORKSPACE}}", input.workspace.replaceAll("\\", "/"));
      const turnInput = turnSpec.includeLocalImage
        ? [{ type: "text", text: prompt }, { type: "local_image", path: path.join(input.workspace, "image.png") }]
        : prompt;
      const turn = await activeThread.runStreamed(turnInput, { signal: controller.signal, outputSchema: turnSpec.outputSchema });
      for await (const event of turn.events) {
        events.push(event); turnEvents.push(event); currentThreadId = activeThread.id;
        checkpoint({ workerPid: process.pid, activeTurn: index });
        if (turnSpec.abortAfterEventType && event.type === turnSpec.abortAfterEventType) controller.abort();
        // A terminal SDK event is the observable end of the turn. Stop
        // consuming immediately so the SDK generator's finally block closes
        // the CLI process even if a descendant keeps stdout handles alive.
        if (event.type === "turn.completed" || event.type === "turn.failed") break;
      }
    } catch (err: any) {
      status = turnSpec.continueAfterError ? status : "error";
      turnError = { name: err?.name, message: String(err?.message ?? err) };
      if (!turnSpec.continueAfterError) error = turnError;
    } finally { clearTimeout(timer); }
    currentThreadId = activeThread.id;
    const turnRecord = { index, resumed: Boolean(turnSpec.resume), threadId: activeThread.id,
      status: turnError ? "error" : "ok", error: turnError, events: turnEvents };
    turns.push(turnRecord);
    checkpoint({ workerPid: process.pid });
    return { activeThread, turnError, turnRecord };
  }
  const first = await runTurn(0, thread);
  thread = first.activeThread;
  if (!(first.turnError && !input.scenario.turns[0].continueAfterError)) {
    if (input.scenario.concurrentResumeAfterFirstTurn) {
      await Promise.all(input.scenario.turns.slice(1).map((_: any, offset: number) => runTurn(offset + 1, thread)));
      turns.sort((a, b) => a.index - b.index);
    } else {
      for (let index = 1; index < input.scenario.turns.length; index += 1) {
        const result = await runTurn(index, thread);
        thread = result.activeThread;
        if (result.turnError && !input.scenario.turns[index].continueAfterError) break;
      }
    }
  }
} catch (err: any) {
  status = "error";
  error = { name: err?.name, message: String(err?.message ?? err) };
} finally {
  checkpoint({
    workerPid: process.pid,
    workerCompleted: true,
    responsesDebug: existsSync(path.join(input.envHome, "responses-debug.jsonl"))
      ? readFileSync(path.join(input.envHome, "responses-debug.jsonl"), "utf8")
      : "",
    rolloutJsonl: rolloutJsonl(),
  });
}
