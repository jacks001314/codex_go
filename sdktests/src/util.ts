import { spawnSync } from "node:child_process";
import { createHash } from "node:crypto";
import { cpSync, existsSync, mkdirSync, readdirSync, readFileSync, renameSync, rmSync, statSync, writeFileSync } from "node:fs";
import path from "node:path";

export const repoRoot = path.resolve(import.meta.dirname, "..", "..");
export const sdktestsRoot = path.resolve(import.meta.dirname, "..");

export function runText(command: string, args: string[], cwd: string): string {
  const result = spawnSync(command, args, {
    cwd,
    encoding: "utf8",
    shell: false,
    windowsHide: true,
  });
  const output = `${result.stdout ?? ""}${result.stderr ?? ""}`.trim();
  if (result.status !== 0) {
    throw new Error(`${command} ${args.join(" ")} failed: ${output}`);
  }
  return output;
}

export function maybeRunText(command: string, args: string[], cwd: string): string | null {
  const result = spawnSync(command, args, {
    cwd,
    encoding: "utf8",
    shell: false,
    windowsHide: true,
  });
  if (result.status !== 0) {
    return null;
  }
  return `${result.stdout ?? ""}${result.stderr ?? ""}`.trim();
}

export function sha256File(filePath: string): string | null {
  if (!existsSync(filePath) || statSync(filePath).isDirectory()) {
    return null;
  }
  const hash = createHash("sha256");
  hash.update(readFileSync(filePath));
  return hash.digest("hex");
}

export function readJson(filePath: string): any {
  return JSON.parse(readFileSync(filePath, "utf8"));
}

export function writeJson(filePath: string, value: unknown): void {
  mkdirSync(path.dirname(filePath), { recursive: true });
  writeFileSync(filePath, `${JSON.stringify(value, null, 2)}\n`, "utf8");
}

export function writeJsonAtomic(filePath: string, value: unknown): void {
  mkdirSync(path.dirname(filePath), { recursive: true });
  const temporary = `${filePath}.${process.pid}.tmp`;
  try {
    writeFileSync(temporary, `${JSON.stringify(value, null, 2)}\n`, "utf8");
    renameSync(temporary, filePath);
  } catch (error) {
    rmSync(temporary, { force: true });
    throw error;
  }
}

export function copyCodexHome(sourceHome: string, targetHome: string): { copied: string[] } {
  mkdirSync(targetHome, { recursive: true });
  const copied: string[] = [];
  for (const name of ["auth.json", "config.toml"]) {
    const source = path.join(sourceHome, name);
    if (existsSync(source)) {
      cpSync(source, path.join(targetHome, name), { recursive: false });
      copied.push(name);
    }
  }
  return { copied };
}

export function copyWindowsSandboxState(sourceHome: string, targetHome: string): string[] {
  if (process.platform !== "win32") {
    return [];
  }
  const copied: string[] = [];
  for (const name of ["cap_sid", ".sandbox-bin"]) {
    const source = path.join(sourceHome, name);
    if (existsSync(source)) {
      cpSync(source, path.join(targetHome, name), { recursive: true });
      copied.push(name);
    }
  }
  const marker = path.join(sourceHome, ".sandbox", "setup_marker.json");
  if (existsSync(marker)) {
    const target = path.join(targetHome, ".sandbox", "setup_marker.json");
    mkdirSync(path.dirname(target), { recursive: true });
    cpSync(marker, target, { recursive: false });
    copied.push(".sandbox/setup_marker.json");
  }
  return copied;
}

export function copyFixture(targetWorkspace: string, fixtureName = "smoke"): void {
  const fixture = path.join(sdktestsRoot, "fixtures", fixtureName);
  rmSync(targetWorkspace, { force: true, recursive: true });
  mkdirSync(path.dirname(targetWorkspace), { recursive: true });
  cpSync(fixture, targetWorkspace, { recursive: true });
}

export function snapshotFiles(root: string): Record<string, string> {
  const out: Record<string, string> = {};
  if (!existsSync(root)) {
    return out;
  }
  const visit = (dir: string) => {
    for (const entry of readdirSync(dir, { withFileTypes: true })) {
      if (entry.name === "__pycache__") continue;
      const full = path.join(dir, entry.name);
      const rel = path.relative(root, full).replaceAll("\\", "/");
      if (entry.isDirectory()) {
        visit(full);
      } else if (entry.isFile() && !entry.name.endsWith(".pyc") && !entry.name.endsWith(".pyo")) {
        out[rel] = sha256File(full) ?? "";
      }
    }
  };
  visit(root);
  return out;
}

export function selectRolloutJsonl(contents: string[], threadId?: string | null): string {
  const records = collectRolloutRecords(contents);
  const exact = threadId
    ? records.filter((record) => record.threadId === threadId)
    : records;
  const candidates = exact.length > 0
    ? exact
    : records.filter((record) => !threadId || record.jsonl.includes(threadId));
  return candidates.sort((left, right) => right.jsonl.length - left.jsonl.length)[0]?.jsonl ?? "";
}

export type RolloutRecord = {
  threadId: string;
  sessionId: string;
  threadSource: string;
  parentThreadId: string;
  agentPath: string;
  agentNickname: string;
  jsonl: string;
};

export function collectRolloutRecords(contents: string[]): RolloutRecord[] {
  return contents.map((jsonl) => {
    const meta = rolloutSessionMeta(jsonl);
    return {
      threadId: stringValue(meta?.id),
      sessionId: stringValue(meta?.session_id),
      threadSource: stringValue(meta?.thread_source),
      parentThreadId: firstString(
        meta?.parent_thread_id,
        meta?.source?.subagent?.thread_spawn?.parent_thread_id,
      ),
      agentPath: stringValue(meta?.agent_path),
      agentNickname: stringValue(meta?.agent_nickname),
      jsonl,
    };
  });
}

function rolloutSessionMeta(jsonl: string): any {
  for (const line of jsonl.split(/\r?\n/)) {
    if (!line.trim()) continue;
    try {
      const parsed = JSON.parse(line);
      if (parsed?.type === "session_meta") return parsed.payload ?? parsed.meta ?? parsed;
    } catch {
      // Preserve malformed rollout text for raw diagnostics and keep scanning.
    }
  }
  return null;
}

function firstString(...values: unknown[]): string {
  for (const value of values) {
    const text = stringValue(value);
    if (text) return text;
  }
  return "";
}

function stringValue(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

export function latestArtifactDir(): string {
  const artifacts = path.join(sdktestsRoot, "artifacts");
  const dirs = existsSync(artifacts)
    ? readdirSync(artifacts, { withFileTypes: true })
        .filter((entry) => entry.isDirectory())
        .map((entry) => path.join(artifacts, entry.name))
        .filter((directory) =>
          existsSync(path.join(directory, "raw", "rust.json")) &&
          existsSync(path.join(directory, "raw", "go.json")))
        .sort()
    : [];
  if (dirs.length === 0) {
    throw new Error("No complete Rust/Go sdktests parity artifacts found.");
  }
  return dirs.at(-1)!;
}

export function safeConfigSummary(configPath: string): Record<string, string> {
  if (!existsSync(configPath)) {
    return {};
  }
  const text = readFileSync(configPath, "utf8");
  const summary: Record<string, string> = {};
  for (const line of text.split(/\r?\n/)) {
    const match = /^(model|model_provider|model_reasoning_effort)\s*=\s*"([^"]*)"/.exec(line.trim());
    if (match) {
      summary[match[1]] = match[2];
    }
  }
  return summary;
}
