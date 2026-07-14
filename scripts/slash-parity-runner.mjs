#!/usr/bin/env node
import { existsSync, mkdirSync, mkdtempSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { spawnSync } from "node:child_process";
import path from "node:path";
import process from "node:process";

let pty;
try {
  pty = await import("node-pty");
} catch {
  pty = null;
}

const root = process.cwd();
const localExe = path.join(root, "code.exe");
const systemExe =
  process.env.CODEX_SYSTEM_EXE ||
  path.join(
    process.env.APPDATA || "",
    "npm",
    "node_modules",
    "@openai",
    "codex",
    "node_modules",
    "@openai",
    "codex-win32-x64",
    "vendor",
    "x86_64-pc-windows-msvc",
    "bin",
    "codex.exe",
  );

const commands = (process.env.CODEX_SLASH_COMMANDS || "/help,/status,/mcp,/exit")
  .split(",")
  .map((value) => value.trim())
  .filter(Boolean);

for (const exe of [localExe, systemExe]) {
  if (!existsSync(exe)) {
    console.error(`Missing executable: ${exe}`);
    process.exit(2);
  }
}

const reportDir = path.join(root, ".tmp-slash-parity");
mkdirSync(reportDir, { recursive: true });

const cases = [
  { name: "system", exe: systemExe },
  { name: "local", exe: localExe },
];

const results = [];
if (pty) {
  for (const testCase of cases) {
    results.push(await runCase(testCase));
  }
} else {
  const fallback = runGoConPTYHarness();
  const report = {
    commands,
    mode: "go-conpty-fallback",
    summary: fallback,
  };
  const reportPath = path.join(reportDir, `slash-parity-${Date.now()}.json`);
  writeFileSync(reportPath, JSON.stringify(report, null, 2));
  console.log(JSON.stringify(fallback, null, 2));
  console.log(`Report: ${reportPath}`);
  process.exit(fallback.ok ? 0 : 1);
}

const summary = compareOutputs(results[0], results[1]);
const report = { commands, results, summary };
const reportPath = path.join(reportDir, `slash-parity-${Date.now()}.json`);
writeFileSync(reportPath, JSON.stringify(report, null, 2));

console.log(JSON.stringify(summary, null, 2));
console.log(`Report: ${reportPath}`);
process.exit(summary.ok ? 0 : 1);

async function runCase(testCase) {
  const home = mkdtempSync(path.join(tmpdir(), `codex-${testCase.name}-`));
  const env = {
    ...process.env,
    CODEX_HOME: home,
    OPENAI_API_KEY: process.env.OPENAI_API_KEY || "sk-test",
    TERM: "xterm-256color",
  };
  const outputChunks = [];
  const proc = pty.spawn(testCase.exe, ["--no-alt-screen"], {
    name: "xterm-256color",
    cols: 120,
    rows: 36,
    cwd: root,
    env,
  });

  proc.onData((data) => outputChunks.push(data));
  await waitForOutput(outputChunks, "OpenAI Codex", 10000).catch(() => {});

  for (const command of commands) {
    proc.write(command + "\r");
    await delay(500);
  }

  await waitForExit(proc, 10000);
  const raw = outputChunks.join("");
  const normalized = normalizeTerminalOutput(raw);
  return {
    name: testCase.name,
    exe: testCase.exe,
    exitCode: proc.exitCode,
    raw,
    normalized,
    markers: markerPresence(normalized),
  };
}

function compareOutputs(system, local) {
  const missingInLocal = [];
  for (const [marker, present] of Object.entries(system.markers)) {
    if (present && !local.markers[marker]) {
      missingInLocal.push(marker);
    }
  }
  return {
    ok: missingInLocal.length === 0 && local.exitCode === 0 && system.exitCode === 0,
    systemExitCode: system.exitCode,
    localExitCode: local.exitCode,
    missingInLocal,
  };
}

function markerPresence(text) {
  const lower = text.toLowerCase();
  const markers = [
    "openai codex",
    "/help",
    "/status",
    "/mcp",
    "model",
    "approval",
    "sandbox",
    "mcp",
    "codex tui commands",
  ];
  return Object.fromEntries(markers.map((marker) => [marker, lower.includes(marker)]));
}

function normalizeTerminalOutput(value) {
  return value
    .replace(/\x1b\[[0-?]*[ -/]*[@-~]/g, "")
    .replace(/\x1b\][^\x07]*(?:\x07|\x1b\\)/g, "")
    .replace(/\r/g, "\n")
    .replace(/[ \t]+\n/g, "\n")
    .replace(/\n{3,}/g, "\n\n")
    .trim();
}

function waitForOutput(chunks, needle, timeoutMs) {
  const deadline = Date.now() + timeoutMs;
  return new Promise((resolve, reject) => {
    const tick = () => {
      if (chunks.join("").includes(needle)) {
        resolve();
        return;
      }
      if (Date.now() > deadline) {
        reject(new Error(`timed out waiting for ${needle}`));
        return;
      }
      setTimeout(tick, 50);
    };
    tick();
  });
}

function waitForExit(proc, timeoutMs) {
  return new Promise((resolve) => {
    let done = false;
    const finish = () => {
      if (done) return;
      done = true;
      resolve();
    };
    proc.onExit(finish);
    setTimeout(() => {
      if (!done) {
        proc.kill();
      }
      finish();
    }, timeoutMs);
  });
}

function delay(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}

function runGoConPTYHarness() {
  const env = {
    ...process.env,
    CODEX_GO_SLASH_PARITY: "1",
  };
  const result = spawnSync(
    "go",
    ["test", "./internal/tui/tea", "-run", "TestSystemCodexSlashParityWithConPTY", "-count=1", "-v"],
    {
      cwd: root,
      env,
      encoding: "utf8",
      windowsHide: true,
    },
  );
  const output = `${result.stdout || ""}${result.stderr || ""}`;
  const skipped = /--- SKIP: TestSystemCodexSlashParityWithConPTY/.test(output);
  return {
    ok: result.status === 0,
    mode: "go-conpty-fallback",
    skipped,
    exitCode: result.status,
    reason: skipped ? "ConPTY harness skipped on this host; see output." : "",
    output,
  };
}
