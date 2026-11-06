#!/usr/bin/env node

import { spawn } from "node:child_process";
import { createHash } from "node:crypto";
import {
  accessSync,
  chmodSync,
  constants as fsConstants,
  cpSync,
  existsSync,
  mkdirSync,
  realpathSync,
  renameSync,
  rmSync,
  statSync,
} from "node:fs";
import { createRequire } from "node:module";
import { constants } from "node:os";
import os from "node:os";
import path from "node:path";
import { fileURLToPath } from "node:url";

import {
  detectPackageManager,
  managedByEnvVarFor,
  updateCommandFor,
} from "./package-manager.js";

const require = createRequire(import.meta.url);
const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const codexPackageRoot = realpathSync(path.resolve(scriptDir, ".."));
const entrypointDir = path.dirname(path.resolve(process.argv[1]));
const packageManager = detectPackageManager({ codexPackageRoot, entrypointDir, scriptDir });
const platformPackages = {
  "linux-x64": "@jacks001314/codex-go-linux-x64",
  "linux-arm64": "@jacks001314/codex-go-linux-arm64",
  "darwin-x64": "@jacks001314/codex-go-darwin-x64",
  "darwin-arm64": "@jacks001314/codex-go-darwin-arm64",
  "win32-x64": "@jacks001314/codex-go-win32-x64",
  "win32-arm64": "@jacks001314/codex-go-win32-arm64",
};
const targetTriples = {
  "linux-x64": "x86_64-unknown-linux-musl",
  "linux-arm64": "aarch64-unknown-linux-musl",
  "darwin-x64": "x86_64-apple-darwin",
  "darwin-arm64": "aarch64-apple-darwin",
  "win32-x64": "x86_64-pc-windows-msvc",
  "win32-arm64": "aarch64-pc-windows-msvc",
};

const target = `${process.platform}-${process.arch}`;
const platformPackage = platformPackages[target];
const targetTriple = targetTriples[target];
if (!platformPackage || !targetTriple) {
  console.error(`Unsupported platform: ${process.platform} (${process.arch})`);
  process.exit(1);
}

let packageJsonPath;
try {
  packageJsonPath = require.resolve(`${platformPackage}/package.json`);
} catch {
  console.error(
    `Missing optional dependency ${platformPackage}. ` +
    `Reinstall with: ${updateCommandFor(packageManager)}`,
  );
  process.exit(1);
}

const executable = path.join(
  path.dirname(packageJsonPath),
  "vendor",
  targetTriple,
  "bin",
  process.platform === "win32" ? "codex.exe" : "codex",
);
if (!existsSync(executable)) {
  console.error(`Codex executable is missing from ${platformPackage}: ${executable}`);
  process.exit(1);
}

const executableMode = 0o755;
const executableNameFor = (platform) => (platform === "win32" ? "codex.exe" : "codex");
const packageRoot = path.dirname(packageJsonPath);

function platformExecutables(root, triple, platform) {
  const base = path.join(root, "vendor", triple);
  return [
    path.join(base, "bin", executableNameFor(platform)),
    path.join(base, "bin", platform === "win32" ? "codex-code-mode-host.exe" : "codex-code-mode-host"),
    path.join(base, "bin", platform === "win32" ? "codex-exec-server.exe" : "codex-exec-server"),
    path.join(base, "codex-path", platform === "win32" ? "rg.exe" : "rg"),
    path.join(base, "codex-resources", "bwrap"),
  ];
}

// chmodExecutable reports whether file is executable after applying the mode.
// Windows ignores Unix mode bits, so it is always considered ready there.
function chmodExecutable(file) {
  if (process.platform === "win32") return true;
  try {
    chmodSync(file, executableMode);
  } catch {
    return false;
  }
  try {
    accessSync(file, fsConstants.X_OK);
    return true;
  } catch {
    return false;
  }
}

// privatePackageCopy mirrors the platform package into the user cache when the
// installed copy cannot be made executable: a root-owned global install, a
// read-only prefix, or a store that dropped the mode bits. The whole vendored
// target is copied so the bundled code-mode host, rg, bwrap, and
// codex-package.json still resolve next to the running binary.
function privatePackageCopy(root, triple, binary) {
  const cacheRoot = process.env.XDG_CACHE_HOME || path.join(os.homedir(), ".cache");
  const source = path.join(root, "vendor", triple);
  let stamp;
  try {
    const info = statSync(binary);
    stamp = `${realpathSync(source)}|${info.size}|${info.mtimeMs}`;
  } catch {
    return null;
  }
  const key = createHash("sha256").update(stamp).digest("hex").slice(0, 16);
  const destination = path.join(cacheRoot, "codex-go", key);
  const copiedRoot = path.join(destination, "vendor", triple);
  const copiedBinary = path.join(copiedRoot, "bin", executableNameFor(process.platform));
  if (existsSync(copiedBinary)) return copiedBinary;

  const staging = `${destination}.tmp-${process.pid}`;
  try {
    rmSync(staging, { recursive: true, force: true });
    mkdirSync(staging, { recursive: true });
    cpSync(source, path.join(staging, "vendor", triple), {
      recursive: true,
      preserveTimestamps: true,
    });
    for (const file of platformExecutables(staging, triple, process.platform)) {
      if (existsSync(file)) chmodExecutable(file);
    }
    try {
      renameSync(staging, destination);
    } catch {
      rmSync(staging, { recursive: true, force: true });
    }
  } catch {
    rmSync(staging, { recursive: true, force: true });
    return null;
  }
  return existsSync(copiedBinary) ? copiedBinary : null;
}

// The native binary must be executable. Platform tarballs packed on a host that
// does not preserve Unix mode bits (Windows) ship it as 0644, and a root-owned
// global install cannot be chmodded by the user. Repair in place when possible
// and otherwise run an executable private copy with the same package layout.
let launchExecutable = executable;
let executableReady = true;
if (process.platform !== "win32") {
  executableReady = chmodExecutable(launchExecutable);
  if (!executableReady) {
    const repaired = privatePackageCopy(packageRoot, targetTriple, executable);
    if (repaired) {
      launchExecutable = repaired;
      executableReady = true;
    }
  }
}

const packageManagerEnvVar = managedByEnvVarFor(packageManager);
const env = { ...process.env, CODEX_MANAGED_PACKAGE_ROOT: codexPackageRoot };
delete env.CODEX_MANAGED_BY_NPM;
delete env.CODEX_MANAGED_BY_BUN;
delete env.CODEX_MANAGED_BY_PNPM;
delete env.CODEX_MANAGED_BY_VITE_PLUS;
env[packageManagerEnvVar] = "1";

// Replace Node so the native binary receives signals and exits directly.
// Node exposes execve on Windows too, but calling it there is unsupported.
if (executableReady && process.platform !== "win32" && typeof process.execve === "function") {
  // Node ignores SIGXFSZ, and ignored signals survive execve. Installing a
  // handler lets execve restore the default disposition in the native binary.
  process.on("SIGXFSZ", () => {});
  try {
    process.execve(launchExecutable, [launchExecutable, ...process.argv.slice(2)], env);
  } catch (error) {
    // execve does not return on success. A failure (for example a noexec mount)
    // falls through to spawn so the user sees a normal error instead of a
    // native abort.
    console.error(`Failed to exec Codex: ${error?.message ?? error}`);
  }
}

// Older Node versions and Windows need a child process. Spawn asynchronously
// so Node can forward signals while the native binary is executing.
const child = spawn(launchExecutable, process.argv.slice(2), {
  stdio: "inherit",
  env,
});

child.on("error", (error) => {
  console.error(`Failed to start Codex: ${error.message}`);
  if (error?.code === "EACCES") {
    console.error(`The Codex binary is not executable: ${launchExecutable}`);
    console.error(
      `Reinstall the package as the user who runs it, or fix the mode with: chmod +x ${launchExecutable}`,
    );
  }
  process.exit(1);
});

for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(signal, () => {
    if (!child.killed) child.kill(signal);
  });
}

child.on("exit", (code, signal) => {
  if (signal) {
    // Rust #57352: preserve a failure exit code even if Node ignores the
    // re-emitted signal.
    process.exitCode = 128 + constants.signals[signal];
    process.removeAllListeners(signal);
    process.kill(process.pid, signal);
  } else {
    process.exit(code ?? 1);
  }
});
