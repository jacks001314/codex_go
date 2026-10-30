import { existsSync, readFileSync, realpathSync } from "node:fs";
import path from "node:path";

// CODEX_PACKAGE_NAME is the package this shim is published as. Keeping it in
// this module lets the shim and its tests use the same ownership path.
export const CODEX_PACKAGE_NAME = "@jacks001314/codex-go";

function sameRealPath(left, right) {
  try {
    return realpathSync(left) === realpathSync(right);
  } catch {
    return false;
  }
}

export function isPnpmOwnedCodexInstall(nodeModulesDir, codexPackageRoot, packageName = CODEX_PACKAGE_NAME) {
  if (!existsSync(path.join(nodeModulesDir, ".modules.yaml"))) return false;
  return sameRealPath(path.join(nodeModulesDir, ...packageName.split("/")), codexPackageRoot);
}

// Vite+ records the active global installation in
// packages/<package-name>.json. Older installs append a #-prefixed ID to the
// package directory; newer installs put the ID in a subdirectory.
export function isVitePlusOwnedCodexInstall(packagesDir, codexPackageRoot, packageName = CODEX_PACKAGE_NAME) {
  if (path.basename(packagesDir) !== "packages") return false;
  try {
    const metadata = JSON.parse(
      readFileSync(path.join(packagesDir, `${packageName}.json`), "utf8"),
    );
    if (metadata.name !== packageName) return false;
    const installId = typeof metadata.installId === "string" ? metadata.installId : "";
    const packageDir = path.join(packagesDir, ...packageName.split("/"));
    const installDir = installId.startsWith("#")
      ? `${packageDir}${installId}`
      : path.join(packageDir, installId);
    for (const nodeModulesDir of [
      path.join(installDir, "lib", "node_modules"),
      path.join(installDir, "node_modules"),
    ]) {
      if (sameRealPath(path.join(nodeModulesDir, ...packageName.split("/")), codexPackageRoot)) {
        return true;
      }
    }
  } catch {
    // Missing or unreadable ownership metadata must not prevent Codex starting.
  }
  return false;
}

// detectPackageManager uses owning-directory metadata from the canonical
// package root and lexical entrypoint. Vite+ is checked before pnpm because
// both can expose the same package through ancestor node_modules layouts.
export function detectPackageManager({
  codexPackageRoot,
  entrypointDir,
  packageName = CODEX_PACKAGE_NAME,
  scriptDir,
  env = process.env,
}) {
  for (const startDir of new Set([codexPackageRoot, entrypointDir])) {
    const filesystemRoot = path.parse(startDir).root;
    for (
      let currentDir = startDir;
      currentDir !== filesystemRoot;
      currentDir = path.dirname(currentDir)
    ) {
      if (isVitePlusOwnedCodexInstall(currentDir, codexPackageRoot, packageName)) {
        return "vite-plus";
      }
      if (isPnpmOwnedCodexInstall(path.join(currentDir, "node_modules"), codexPackageRoot, packageName)) {
        return "pnpm";
      }
    }
    if (isPnpmOwnedCodexInstall(path.join(filesystemRoot, "node_modules"), codexPackageRoot, packageName)) {
      return "pnpm";
    }
  }
  const userAgent = env.npm_config_user_agent || "";
  const execPath = env.npm_execpath || "";
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

export function updateCommandFor(packageManager, packageName = CODEX_PACKAGE_NAME) {
  switch (packageManager) {
    case "bun":
      return `bun install -g ${packageName}@latest`;
    case "pnpm":
      return `pnpm add -g ${packageName}@latest`;
    case "vite-plus":
      return `vp install -g ${packageName}@latest`;
    default:
      return `npm install -g ${packageName}@latest`;
  }
}

export function managedByEnvVarFor(packageManager) {
  switch (packageManager) {
    case "bun":
      return "CODEX_MANAGED_BY_BUN";
    case "pnpm":
      return "CODEX_MANAGED_BY_PNPM";
    case "vite-plus":
      return "CODEX_MANAGED_BY_VITE_PLUS";
    default:
      return "CODEX_MANAGED_BY_NPM";
  }
}
