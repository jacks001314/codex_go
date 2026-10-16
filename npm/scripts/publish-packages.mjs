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
// Rust #54641 retries each publish up to three times with 10s/20s backoff and
// skips a version that is already published.
const publishAttempts = 3;
const alreadyPublishedPattern = /previously published|cannot publish over|version already exists/i;
const sleepSeconds = (seconds) => {
  // `Atomics.wait` blocks this single-threaded script without spinning the CPU.
  Atomics.wait(new Int32Array(new SharedArrayBuffer(4)), 0, 0, seconds * 1000);
};
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
  const filename = path.basename(tarball);
  for (let attempt = 1; attempt <= publishAttempts; attempt += 1) {
    console.log(`Publishing ${filename} with tag ${tag} (attempt ${attempt}/${publishAttempts})`);
    const publishArgs = ["publish", tarball, "--access", "public", "--tag", tag];
    if (otp) publishArgs.push("--otp", otp);
    // The output is captured so an already-published version can be recognized,
    // and echoed so the verbose npm log still reaches the job output.
    const result = spawnSync("npm", publishArgs, { cwd: root, encoding: "utf8", shell: process.platform === "win32" });
    const output = `${result.stdout ?? ""}${result.stderr ?? ""}`;
    process.stdout.write(output);
    if (result.status === 0) break;
    if (alreadyPublishedPattern.test(output)) {
      console.log(`Skipping already-published package version for ${filename}`);
      break;
    }
    if (attempt === publishAttempts) process.exit(result.status ?? 1);
    const delay = attempt * 10;
    console.log(`Publishing ${filename} failed; retrying in ${delay} seconds.`);
    sleepSeconds(delay);
  }
}
