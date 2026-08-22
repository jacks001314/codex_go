#!/usr/bin/env node

import { readdirSync } from "node:fs";
import path from "node:path";
import { spawnSync } from "node:child_process";
import { fileURLToPath } from "node:url";

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "../..");
const args = process.argv.slice(2);
const valueOf = (name) => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
};
const directory = valueOf("--directory");
if (!directory) {
  throw new Error("Usage: node npm/scripts/publish-packages.mjs --directory dist/v1.2.3/npm [--tag latest]");
}
const packageDir = path.resolve(root, directory);
const tag = valueOf("--tag") || "latest";
const otp = valueOf("--otp");
const tarballs = readdirSync(packageDir)
  .filter((file) => file.endsWith(".tgz"))
  .map((file) => path.join(packageDir, file));
const main = tarballs.filter((file) => /jacks001314-codex-go-\d/.test(path.basename(file)));
const platforms = tarballs.filter((file) => !main.includes(file));
if (main.length !== 1) {
  throw new Error(`Expected exactly one main package in ${packageDir}; found ${main.length}.`);
}
if (platforms.length === 0) {
  throw new Error(`Expected at least one platform package in ${packageDir}; found none.`);
}

for (const tarball of [...platforms.sort(), main[0]]) {
  console.log(`Publishing ${path.basename(tarball)} with tag ${tag}`);
  const publishArgs = ["publish", tarball, "--access", "public", "--tag", tag];
  if (otp) publishArgs.push("--otp", otp);
  const result = spawnSync("npm", publishArgs, { cwd: root, stdio: "inherit", shell: process.platform === "win32" });
  if (result.status !== 0) process.exit(result.status ?? 1);
}
