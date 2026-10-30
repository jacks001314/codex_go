import assert from "node:assert/strict";
import { mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import path from "node:path";
import test from "node:test";

import {
  CODEX_PACKAGE_NAME,
  detectPackageManager,
  isVitePlusOwnedCodexInstall,
  managedByEnvVarFor,
  updateCommandFor,
} from "./package-manager.js";

function makeTempDir(t) {
  const dir = mkdtempSync(path.join(tmpdir(), "codex-go-package-manager-"));
  t.after(() => rmSync(dir, { force: true, recursive: true }));
  return dir;
}

function packageRootInside(nodeModulesDir) {
  return path.join(nodeModulesDir, ...CODEX_PACKAGE_NAME.split("/"));
}

function writeVitePlusMetadata(packagesDir, installId, name = CODEX_PACKAGE_NAME) {
  mkdirSync(path.join(packagesDir, ...CODEX_PACKAGE_NAME.split("/").slice(0, -1)), {
    recursive: true,
  });
  writeFileSync(
    path.join(packagesDir, `${CODEX_PACKAGE_NAME}.json`),
    JSON.stringify({ name, installId }),
  );
}

function installVitePlusPackage(packagesDir, installId, nodeModulesName = "lib/node_modules") {
  const packageDir = path.join(packagesDir, ...CODEX_PACKAGE_NAME.split("/"));
  const installDir = installId.startsWith("#")
    ? `${packageDir}${installId}`
    : path.join(packageDir, installId);
  const packageRoot = packageRootInside(path.join(installDir, ...nodeModulesName.split("/")));
  mkdirSync(packageRoot, { recursive: true });
  return packageRoot;
}

function detectFor(codexPackageRoot) {
  return detectPackageManager({
    codexPackageRoot,
    entrypointDir: codexPackageRoot,
    scriptDir: codexPackageRoot,
    env: {},
  });
}

test("detects Vite+ installs that append the install id with #", (t) => {
  const packagesDir = path.join(makeTempDir(t), "packages");
  writeVitePlusMetadata(packagesDir, "#v1");
  const codexPackageRoot = installVitePlusPackage(packagesDir, "#v1");

  assert.equal(isVitePlusOwnedCodexInstall(packagesDir, codexPackageRoot), true);
  assert.equal(detectFor(codexPackageRoot), "vite-plus");
});

test("detects Vite+ installs that nest the install id as a subdirectory", (t) => {
  const packagesDir = path.join(makeTempDir(t), "packages");
  writeVitePlusMetadata(packagesDir, "v2");
  const codexPackageRoot = installVitePlusPackage(packagesDir, "v2", "node_modules");

  assert.equal(isVitePlusOwnedCodexInstall(packagesDir, codexPackageRoot), true);
  assert.equal(detectFor(codexPackageRoot), "vite-plus");
});

test("ignores Vite+ metadata for a different package", (t) => {
  const packagesDir = path.join(makeTempDir(t), "packages");
  writeVitePlusMetadata(packagesDir, "#v1", "@openai/codex");
  const codexPackageRoot = installVitePlusPackage(packagesDir, "#v1");

  assert.equal(isVitePlusOwnedCodexInstall(packagesDir, codexPackageRoot), false);
  assert.equal(detectFor(codexPackageRoot), null);
});

test("ignores Vite+ metadata outside a packages directory", (t) => {
  const storeDir = path.join(makeTempDir(t), "store");
  const codexPackageRoot = installVitePlusPackage(storeDir, "#v1");

  assert.equal(isVitePlusOwnedCodexInstall(storeDir, codexPackageRoot), false);
});

test("detects pnpm-owned installs from .modules.yaml", (t) => {
  const nodeModulesDir = path.join(makeTempDir(t), "node_modules");
  mkdirSync(nodeModulesDir, { recursive: true });
  writeFileSync(path.join(nodeModulesDir, ".modules.yaml"), "hoistPattern:\n");
  const codexPackageRoot = packageRootInside(nodeModulesDir);
  mkdirSync(codexPackageRoot, { recursive: true });

  assert.equal(detectFor(codexPackageRoot), "pnpm");
});

test("falls back to npm and bun from the environment", (t) => {
  const codexPackageRoot = path.join(makeTempDir(t), "codex-go");
  const env = { npm_config_user_agent: "npm/10.0.0 node/v20.0.0" };
  assert.equal(
    detectPackageManager({ codexPackageRoot, entrypointDir: codexPackageRoot, scriptDir: "", env }),
    "npm",
  );
  assert.equal(
    detectPackageManager({
      codexPackageRoot,
      entrypointDir: codexPackageRoot,
      scriptDir: "",
      env: { npm_config_user_agent: "bun/1.1.0 npm/? node/v20.0.0" },
    }),
    "bun",
  );
  assert.equal(
    detectPackageManager({ codexPackageRoot, entrypointDir: codexPackageRoot, scriptDir: "", env: {} }),
    null,
  );
});

test("maps package managers to update commands and environment markers", () => {
  assert.equal(updateCommandFor("vite-plus"), `vp install -g ${CODEX_PACKAGE_NAME}@latest`);
  assert.equal(updateCommandFor("pnpm"), `pnpm add -g ${CODEX_PACKAGE_NAME}@latest`);
  assert.equal(updateCommandFor("bun"), `bun install -g ${CODEX_PACKAGE_NAME}@latest`);
  assert.equal(updateCommandFor(null), `npm install -g ${CODEX_PACKAGE_NAME}@latest`);

  assert.equal(managedByEnvVarFor("vite-plus"), "CODEX_MANAGED_BY_VITE_PLUS");
  assert.equal(managedByEnvVarFor("pnpm"), "CODEX_MANAGED_BY_PNPM");
  assert.equal(managedByEnvVarFor("bun"), "CODEX_MANAGED_BY_BUN");
  assert.equal(managedByEnvVarFor(null), "CODEX_MANAGED_BY_NPM");
});
