import { spawn } from "node:child_process";
import { existsSync, mkdirSync, readdirSync, readFileSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { collectRolloutRecords, copyCodexHome, copyFixture, maybeRunText, repoRoot, sdktestsRoot, sha256File, writeJson } from "./util.ts";

type Implementation = "rust" | "go";

const ALPHA_TOKEN = "CROSS_TOKEN_ALPHA";
const BETA_TOKEN = "CROSS_TOKEN_BETA";
const RUST_WRITER_OK = "RUST_WRITER_OK";
const GO_WRITER_OK = "GO_WRITER_OK";

const options = parseOptions(process.argv.slice(2));
const rustPath = required(options.rust, "--rust");
const goPath = required(options.go, "--go");
const sdkPath = required(options.sdk, "--sdk");
const sdkIndex = path.join(sdkPath, "dist", "index.js");
if (!existsSync(sdkIndex)) {
  throw new Error(`SDK dist not found: ${sdkIndex}`);
}
const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
const artifactDir = path.join(sdktestsRoot, "artifacts", `${stamp}-raw_session_cross_compat`);
const tmpDir = path.join(sdktestsRoot, ".tmp", `${stamp}-raw_session_cross_compat`);
mkdirSync(path.join(artifactDir, "raw"), { recursive: true });
mkdirSync(path.join(artifactDir, "normalized"), { recursive: true });

const manifest = {
  generatedAt: new Date().toISOString(),
  driver: "sdk-shared-codex-home-cross-implementation-resume",
  scenario: "raw_session_cross_compat",
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
    "Rust CLI writes a session that the Go CLI resumes in the same CODEX_HOME",
    "Go CLI repairs a Rust rollout-only session selected by exact name",
    "Go CLI repairs a Rust rollout-only session selected by resume --last",
    "Go CLI resumes the Rust-written thread and recalls the turn-1 token",
    "Go CLI writes a session that the Rust CLI resumes in the same CODEX_HOME",
    "Rust CLI resumes the Go-written thread and recalls the turn-1 token",
    "both rollout files remain parseable by the shared harness after each phase",
  ],
};
writeJson(path.join(artifactDir, "run-manifest.json"), manifest);

const home = path.join(tmpDir, "shared-home");
const workspace = path.join(tmpDir, "workspace");
mkdirSync(home, { recursive: true });
copyFixture(workspace, "smoke");
copyCodexHome(path.join(requireUserHome(), ".codex"), home);

const phases: Record<string, any> = {};
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

  phases.rustWriter = await runPhase(sdk, rustPath, env, threadOptions, {
    label: "rustWriter",
    start: true,
    prompt: `Remember the token ${ALPHA_TOKEN} and reply with exactly ${RUST_WRITER_OK}.`,
    expected: RUST_WRITER_OK,
  });
  const rustThreadID = phases.rustWriter.threadId;
  if (!rustThreadID) throw new Error("rust writer thread id missing");
  phases.homeAfterRustWriter = snapshotHome(home, artifactDir, "after-rust-writer");

  writeFileSync(path.join(home, "session_index.jsonl"), `${JSON.stringify({
    id: rustThreadID,
    thread_name: "Cross Compat Rust Session",
    updated_at: new Date().toISOString(),
  })}\n`, { flag: "a" });
  phases.goNamedReader = await runSelectorPhase(goPath, env, workspace, {
    label: "goNamedReader",
    diagnosticFile: path.join(artifactDir, "raw", "responses-debug-go-named-reader.jsonl"),
    selector: ["Cross Compat Rust Session"],
    prompt: "Reply with the token I asked you to remember, and nothing else.",
    expected: ALPHA_TOKEN,
  });
  rmSync(path.join(home, "sessions", `${rustThreadID}.json`), { force: true });
  phases.goLastReader = await runSelectorPhase(goPath, env, workspace, {
    label: "goLastReader",
    diagnosticFile: path.join(artifactDir, "raw", "responses-debug-go-last-reader.jsonl"),
    selector: ["--last"],
    prompt: "Reply with the token I asked you to remember, and nothing else.",
    expected: ALPHA_TOKEN,
  });
  phases.homeAfterGoSelectors = snapshotHome(home, artifactDir, "after-go-selectors");

  phases.goReader = await runPhase(sdk, goPath, env, threadOptions, {
    label: "goReader",
    diagnosticFile: path.join(artifactDir, "raw", "responses-debug-go-id-reader.jsonl"),
    resume: rustThreadID,
    prompt: "Reply with the token I asked you to remember, and nothing else.",
    expected: ALPHA_TOKEN,
  });
  phases.homeAfterGoReader = snapshotHome(home, artifactDir, "after-go-reader");

  phases.goWriter = await runPhase(sdk, goPath, env, threadOptions, {
    label: "goWriter",
    diagnosticFile: path.join(artifactDir, "raw", "responses-debug-go-writer.jsonl"),
    start: true,
    prompt: `Remember the token ${BETA_TOKEN} and reply with exactly ${GO_WRITER_OK}.`,
    expected: GO_WRITER_OK,
  });
  const goThreadID = phases.goWriter.threadId;
  if (!goThreadID) throw new Error("go writer thread id missing");
  phases.homeAfterGoWriter = snapshotHome(home, artifactDir, "after-go-writer");
  await waitForRolloutsSettled(home);

  phases.rustReader = await runPhase(sdk, rustPath, env, threadOptions, {
    label: "rustReader",
    resume: goThreadID,
    prompt: "Reply with the token I asked you to remember, and nothing else.",
    expected: BETA_TOKEN,
  });
  phases.homeAfterRustReader = snapshotHome(home, artifactDir, "after-rust-reader");

  const semantic = summarize(phases);
  const passed = semanticChecksPass(semantic);
  const infrastructureFailure = !passed && Object.values(phases).some((phase: any) =>
    phase && typeof phase === "object" && isInfrastructureError(phase.error),
  );
  const comparison = {
    status: passed ? "pass" : infrastructureFailure ? "infra_failure" : "behavior_mismatch",
    classification: passed ? "parity" : infrastructureFailure ? "infra-failure" : "go-bug",
    semantic,
  };
  writeJson(path.join(artifactDir, "comparison.json"), comparison);
  writeJson(path.join(artifactDir, "raw", "phases.json"), phases);
  writeJson(path.join(artifactDir, "normalized", "semantic.json"), semantic);
  console.log(`raw session cross-compat: ${comparison.status} (${comparison.classification})`);
  console.log(`raw session cross-compat artifact: ${artifactDir}`);
  process.exitCode = passed ? 0 : infrastructureFailure ? 2 : 1;
} catch (error: any) {
  writeJson(path.join(artifactDir, "comparison.json"), {
    status: "infra_failure",
    classification: "infra-failure",
    error: { name: error?.name, message: String(error?.message ?? error) },
  });
  console.error(error?.message ?? error);
  console.error(`raw session cross-compat artifact: ${artifactDir}`);
  process.exitCode = 2;
} finally {
  try {
    await removeTemporaryRoot(tmpDir);
  } catch (error: any) {
    console.warn(`raw session cross-compat temporary cleanup failed: ${String(error?.message ?? error)}`);
  }
}

function isInfrastructureError(error: any): boolean {
  const message = String(error?.message ?? "");
  return /upstream request failed|server[_ -]?overloaded|rate[_ -]?limit|status\s+(?:402|408|409|429|5\d\d)/i.test(message);
}

async function runSelectorPhase(
  binary: string,
  env: Record<string, string>,
  workspace: string,
  phase: { label: string; diagnosticFile?: string; selector: string[]; prompt: string; expected: string },
): Promise<Record<string, any>> {
  const args = [
    "exec", "--experimental-json", "--skip-git-repo-check", "--sandbox", "read-only",
    "--cd", workspace, "--config", 'approval_policy="never"', "--config", 'web_search="disabled"',
    "resume", ...phase.selector,
  ];
  const phaseEnv = diagnosticEnv(env, phase.diagnosticFile);
  const child = spawn(binary, args, { cwd: workspace, env: phaseEnv, windowsHide: true, stdio: ["pipe", "pipe", "pipe"] });
  let stdout = "";
  let stderr = "";
  child.stdout.on("data", (chunk) => { stdout += String(chunk); });
  child.stderr.on("data", (chunk) => { stderr += String(chunk); });
  child.stdin.end(phase.prompt);
  const exit = await new Promise<{ code: number | null; signal: NodeJS.Signals | null }>((resolve) => {
    const timer = setTimeout(() => {
      child.kill();
      resolve({ code: null, signal: "SIGTERM" });
    }, 240_000);
    child.once("exit", (code, signal) => {
      clearTimeout(timer);
      resolve({ code, signal });
    });
  });
  const events = stdout.split(/\r?\n/).filter((line) => line.trim()).flatMap((line) => {
    try { return [JSON.parse(line)]; } catch { return []; }
  });
  const final = events.filter((event) => event?.type === "item.completed" && event?.item?.type === "agent_message")
    .map((event) => event.item.text).at(-1) ?? null;
  const threadId = events.find((event) => event?.type === "thread.started")?.thread_id ?? null;
  const error = exit.code === 0 && !exit.signal ? null : { message: `exit=${exit.code} signal=${exit.signal}: ${stderr}` };
  return {
    phase: phase.label,
    binary,
    status: error ? "error" : "ok",
    error,
    final,
    expected: phase.expected,
    ok: error == null && String(final ?? "").trim() === phase.expected,
    threadId,
    eventTypeCounts: countEventTypes(events),
    events,
    stderr,
    diagnosticFile: phase.diagnosticFile ?? null,
  };
}

async function runPhase(
  sdk: any,
  binary: string,
  env: Record<string, string>,
  threadOptions: Record<string, unknown>,
  phase: { label: string; diagnosticFile?: string; start?: boolean; resume?: string; prompt: string; expected: string },
): Promise<Record<string, any>> {
  const client = new sdk.Codex({ codexPathOverride: binary, env: diagnosticEnv(env, phase.diagnosticFile) });
  const thread = phase.resume
    ? client.resumeThread(phase.resume, threadOptions)
    : client.startThread(threadOptions);
  const events: any[] = [];
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), 240_000);
  let error: any = null;
  let final: string | null = null;
  try {
    const streamed = await thread.runStreamed(phase.prompt, { signal: controller.signal });
    for await (const event of streamed.events) {
      events.push(event);
      if (event.type === "item.completed" && event.item?.type === "agent_message") {
        final = event.item.text ?? final;
      }
    }
  } catch (err: any) {
    error = { name: err?.name, message: String(err?.message ?? err) };
  } finally {
    clearTimeout(timer);
  }
  const ok = error == null && final != null && String(final).trim() === phase.expected;
  return {
    phase: phase.label,
    binary: binary,
    status: error ? "error" : "ok",
    error,
    final,
    expected: phase.expected,
    ok,
    threadId: thread.id,
    eventTypeCounts: countEventTypes(events),
    events,
    diagnosticFile: phase.diagnosticFile ?? null,
  };
}

function diagnosticEnv(env: Record<string, string>, diagnosticFile?: string): Record<string, string> {
  if (!diagnosticFile) return env;
  return {
    ...env,
    CODEX_GO_RESPONSES_DEBUG: "1",
    CODEX_GO_RESPONSES_DEBUG_FILE: diagnosticFile,
  };
}

function countEventTypes(events: any[]): Record<string, number> {
  const counts: Record<string, number> = {};
  for (const event of events) {
    counts[String(event.type)] = (counts[String(event.type)] ?? 0) + 1;
  }
  return counts;
}

function snapshotHome(home: string, artifactDir: string, label: string): Record<string, any> {
  const files = listSessionFiles(home);
  const contents = files.map((relative) => readFileSync(path.join(home, relative.replaceAll("/", path.sep)), "utf8"));
  const rawDir = path.join(artifactDir, "raw", label);
  mkdirSync(rawDir, { recursive: true });
  for (const relative of files) {
    const source = path.join(home, relative.replaceAll("/", path.sep));
    const destination = path.join(rawDir, relative.replaceAll("/", path.sep));
    mkdirSync(path.dirname(destination), { recursive: true });
    writeFileSync(destination, readFileSync(source));
  }
  return {
    sessionFiles: files.sort(),
    rolloutRecords: collectRolloutRecords(contents).map((record) => ({
      threadId: record.threadId,
      sessionId: record.sessionId,
      threadSource: record.threadSource,
      parentThreadId: record.parentThreadId,
      agentPath: record.agentPath,
    })),
  };
}

async function waitForRolloutsSettled(home: string): Promise<void> {
  let previous = "";
  for (let attempt = 0; attempt < 30; attempt++) {
    const files = listSessionFiles(home);
    const signature = files.map((file) => `${file}:${statSync(path.join(home, file)).size}`).join("|");
    const allNonEmpty = files.length > 0 && files.every((file) => statSync(path.join(home, file)).size > 0);
    if (allNonEmpty && signature === previous) return;
    previous = signature;
    await new Promise((resolve) => setTimeout(resolve, 100));
  }
}

function listSessionFiles(home: string): string[] {
  const sessionsRoot = path.join(home, "sessions");
  const files: string[] = [];
  if (!existsSync(sessionsRoot)) return files;
  const visit = (directory: string) => {
    for (const name of readdirSync(directory)) {
      const candidate = path.join(directory, name);
      if (statSync(candidate).isDirectory()) visit(candidate);
      else if (name.endsWith(".jsonl")) files.push(path.relative(home, candidate).replaceAll("\\", "/"));
    }
  };
  visit(sessionsRoot);
  return files.sort();
}

function summarize(phases: Record<string, any>): Record<string, any> {
  const pick = (name: string) => {
    const phase = phases[name];
    return {
      status: phase?.status ?? "missing",
      final: phase?.final ?? null,
      ok: Boolean(phase?.ok),
      threadIdSet: Boolean(phase?.threadId),
      error: phase?.error ?? null,
    };
  };
  return {
    rustWriter: pick("rustWriter"),
    goNamedReader: pick("goNamedReader"),
    goLastReader: pick("goLastReader"),
    goReader: pick("goReader"),
    goWriter: pick("goWriter"),
    rustReader: pick("rustReader"),
    crossCompat: {
      rustToGo: phases.goReader?.ok === true,
      rustToGoByName: phases.goNamedReader?.ok === true,
      rustToGoByLast: phases.goLastReader?.ok === true,
      goToRust: phases.rustReader?.ok === true,
    },
    sessionInventory: {
      afterRustWriter: phases.homeAfterRustWriter,
      afterGoSelectors: phases.homeAfterGoSelectors,
      afterGoReader: phases.homeAfterGoReader,
      afterGoWriter: phases.homeAfterGoWriter,
      afterRustReader: phases.homeAfterRustReader,
    },
  };
}

function semanticChecksPass(value: Record<string, any>): boolean {
  return value.rustWriter?.ok === true &&
    value.goNamedReader?.ok === true &&
    value.goLastReader?.ok === true &&
    value.goReader?.ok === true &&
    value.goWriter?.ok === true &&
    value.rustReader?.ok === true &&
    value.crossCompat?.rustToGo === true &&
    value.crossCompat?.rustToGoByName === true &&
    value.crossCompat?.rustToGoByLast === true &&
    value.crossCompat?.goToRust === true;
}

function binaryInfo(binary: string): Record<string, any> {
  return {
    path: binary,
    version: maybeRunText(binary, ["--version"], repoRoot),
    sha256: sha256File(binary),
  };
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
