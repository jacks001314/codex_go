#!/usr/bin/env node

import { spawn } from "node:child_process";
import { existsSync, realpathSync } from "node:fs";
import { createRequire } from "node:module";
import path from "node:path";
import { fileURLToPath } from "node:url";

const require = createRequire(import.meta.url);
const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const codexPackageRoot = realpathSync(path.resolve(scriptDir, ".."));
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
	const packageManager = detectPackageManager();
	const updateCommand =
		packageManager === "bun"
			? "bun install -g @jacks001314/codex-go@latest"
			: packageManager === "pnpm"
				? "pnpm add -g @jacks001314/codex-go@latest"
				: "npm install -g @jacks001314/codex-go@latest";
  console.error(
    `Missing optional dependency ${platformPackage}. ` +
			`Reinstall with: ${updateCommand}`,
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

function isPnpmOwnedCodexInstall(nodeModulesDir) {
  if (!existsSync(path.join(nodeModulesDir, ".modules.yaml"))) return false;
  try {
    return realpathSync(path.join(nodeModulesDir, "@jacks001314", "codex-go")) === codexPackageRoot;
  } catch {
    return false;
  }
}

function detectPackageManager() {
  const entrypointDir = path.dirname(path.resolve(process.argv[1]));
  for (const startDir of new Set([codexPackageRoot, entrypointDir])) {
    const filesystemRoot = path.parse(startDir).root;
    for (let currentDir = startDir; currentDir !== filesystemRoot; currentDir = path.dirname(currentDir)) {
      if (isPnpmOwnedCodexInstall(path.join(currentDir, "node_modules"))) return "pnpm";
    }
    if (isPnpmOwnedCodexInstall(path.join(filesystemRoot, "node_modules"))) return "pnpm";
  }
  const userAgent = process.env.npm_config_user_agent || "";
  const execPath = process.env.npm_execpath || "";
  if (
    /\bbun\//.test(userAgent) ||
    execPath.includes("bun") ||
    scriptDir.includes(".bun/install/global") ||
    scriptDir.includes(".bun\\install\\global")
  ) {
    return "bun";
  }
  return userAgent ? "npm" : null;
}

const packageManager = detectPackageManager();
const packageManagerEnvVar =
  packageManager === "bun"
    ? "CODEX_MANAGED_BY_BUN"
    : packageManager === "pnpm"
      ? "CODEX_MANAGED_BY_PNPM"
      : "CODEX_MANAGED_BY_NPM";
const env = { ...process.env, CODEX_MANAGED_PACKAGE_ROOT: codexPackageRoot };
delete env.CODEX_MANAGED_BY_NPM;
delete env.CODEX_MANAGED_BY_BUN;
delete env.CODEX_MANAGED_BY_PNPM;
env[packageManagerEnvVar] = "1";

const child = spawn(executable, process.argv.slice(2), {
  stdio: "inherit",
  env,
});

child.on("error", (error) => {
  console.error(`Failed to start Codex: ${error.message}`);
  process.exit(1);
});

for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  process.on(signal, () => {
    if (!child.killed) child.kill(signal);
  });
}

child.on("exit", (code, signal) => {
  if (signal) process.kill(process.pid, signal);
  else process.exit(code ?? 1);
});
