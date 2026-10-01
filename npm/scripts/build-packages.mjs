#!/usr/bin/env node

import { spawnSync } from "node:child_process";
import { chmodSync, cpSync, existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";

const scriptDir = path.dirname(fileURLToPath(import.meta.url));
const root = path.resolve(scriptDir, "../..");
const args = process.argv.slice(2);
const valueOf = (name) => {
  const index = args.indexOf(name);
  return index >= 0 ? args[index + 1] : undefined;
};
let version = (valueOf("--version") || "").replace(/^v/, "");
if (!version) {
  const versionFile = path.resolve(root, "VERSION");
  if (existsSync(versionFile)) {
    version = readFileSync(versionFile, "utf8").trim().replace(/^v/, "");
  }
}
if (!/^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$/.test(version)) {
  throw new Error("Usage: node npm/scripts/build-packages.mjs --version 1.2.3 [--output-dir dist/npm]");
}

const outputDir = path.resolve(root, valueOf("--output-dir") || `dist/v${version}/npm`);
const stageRoot = path.join(outputDir, "stage");
rmSync(stageRoot, { recursive: true, force: true });
mkdirSync(stageRoot, { recursive: true });

const targets = [
  { goos: "linux", goarch: "amd64", npmOS: "linux", npmCPU: "x64", targetTriple: "x86_64-unknown-linux-musl" },
  { goos: "linux", goarch: "arm64", npmOS: "linux", npmCPU: "arm64", targetTriple: "aarch64-unknown-linux-musl" },
  { goos: "darwin", goarch: "amd64", npmOS: "darwin", npmCPU: "x64", targetTriple: "x86_64-apple-darwin" },
  { goos: "darwin", goarch: "arm64", npmOS: "darwin", npmCPU: "arm64", targetTriple: "aarch64-apple-darwin" },
  { goos: "windows", goarch: "amd64", npmOS: "win32", npmCPU: "x64", targetTriple: "x86_64-pc-windows-msvc" },
  { goos: "windows", goarch: "arm64", npmOS: "win32", npmCPU: "arm64", targetTriple: "aarch64-pc-windows-msvc" },
];

function run(command, commandArgs, options = {}) {
  const useShell = process.platform === "win32" && command === "npm";
  const result = spawnSync(command, commandArgs, { cwd: root, stdio: "inherit", shell: useShell, ...options });
  if (result.error) throw result.error;
  if (result.status !== 0) throw new Error(`${command} failed with exit code ${result.status}`);
}

for (const target of targets) {
  const suffix = `${target.npmOS}-${target.npmCPU}`;
  const packageDir = path.join(stageRoot, `codex-go-${suffix}`);
  const vendorDir = path.join(packageDir, "vendor");
  const targetDir = path.join(vendorDir, target.targetTriple);
  const binDir = path.join(targetDir, "bin");
  mkdirSync(binDir, { recursive: true });
  const executableName = target.goos === "windows" ? "codex.exe" : "codex";
  const executable = path.join(binDir, executableName);
  const env = { ...process.env, GOOS: target.goos, GOARCH: target.goarch, CGO_ENABLED: "0" };
  // Linux packages pin the digest of the bundled bubblewrap they carry, so the
  // sandbox refuses to run a replaced launcher (Rust build.rs stamps
  // CODEX_BWRAP_SHA256 the same way).
  let bwrapDigestFlag = "";
  if (target.goos === "linux") {
    const digestPath = path.join(root, "third_party", "bwrap", "build", `${target.goos}-${target.goarch}`, "bundled-bwrap.sha256");
    if (existsSync(digestPath)) {
      const digest = readFileSync(digestPath, "utf8").trim();
      if (digest) bwrapDigestFlag = ` -X codex_go/sandbox/linuxsandbox.bundledBwrapSHA256=${digest}`;
    }
  }
  const ldflags = `-s -w -X codex_go/doctor.buildVersion=${version} -X codex_go/appserver.buildVersion=${version} -X codex_go/mcp.buildVersion=${version}${bwrapDigestFlag}`;
  run("go", ["build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", executable, "./cmd/codex"], { env });
  // Best-effort exec bit: required for Unix targets when built on a host that
  // does not preserve mode bits. No-op on Windows.
  try { chmodSync(executable, 0o755); } catch {}
  // The managed app-server launches the code-mode host from its own package, so
  // every packaged target ships it beside the entrypoint.
  const codeModeHostName = target.goos === "windows" ? "codex-code-mode-host.exe" : "codex-code-mode-host";
  run("go", ["build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", path.join(binDir, codeModeHostName), "./cmd/codex-code-mode-host"], { env });
  try { chmodSync(path.join(binDir, codeModeHostName), 0o755); } catch {}
  if (target.goos === "windows") {
    const resourcesDir = path.join(targetDir, "codex-resources");
    mkdirSync(resourcesDir, { recursive: true });
    for (const helper of ["codex-command-runner", "codex-windows-sandbox-setup"]) {
      run("go", ["build", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", path.join(resourcesDir, `${helper}.exe`), `./cmd/${helper}`], { env });
    }
  }
  writeFileSync(path.join(targetDir, "codex-package.json"), `${JSON.stringify({
    layoutVersion: 1,
    version,
    target: target.targetTriple,
    variant: "codex",
    entrypoint: `bin/${executableName}`,
    resourcesDir: "codex-resources",
    pathDir: "codex-path",
  }, null, 2)}\n`);
  writeFileSync(path.join(packageDir, "package.json"), `${JSON.stringify({
    name: `@jacks001314/codex-go-${suffix}`,
    version,
    description: `Native binary for @jacks001314/codex-go (${suffix})`,
    license: "Apache-2.0",
    os: [target.npmOS],
    cpu: [target.npmCPU],
    files: ["vendor"],
    repository: { type: "git", url: "git+https://github.com/jacks001314/codex_go.git" },
  }, null, 2)}\n`);
  for (const file of ["README.md", "LICENSE", "NOTICE"]) {
    try { cpSync(path.join(root, file), path.join(packageDir, file)); } catch {}
  }
  // Every package carries the pinned search backend its own tools resolve from
  // `codex-path` (Rust package_layout.path_dir); a package without it is
  // rejected when a daemon installs from it.
  const ripgrepName = target.goos === "windows" ? "rg.exe" : "rg";
  const preparedRipgrep = path.join(root, "third_party", "ripgrep", "build", `${target.goos}-${target.goarch}`, "bin", ripgrepName);
  if (existsSync(preparedRipgrep)) {
    mkdirSync(path.join(targetDir, "codex-path"), { recursive: true });
    cpSync(preparedRipgrep, path.join(targetDir, "codex-path", ripgrepName));
    try { chmodSync(path.join(targetDir, "codex-path", ripgrepName), 0o755); } catch {}
  } else {
    console.warn(`ripgrep for ${target.goos}/${target.goarch} is not prepared; run third_party/ripgrep/prepare_ripgrep.py --platform ${target.goos}-${target.goarch}`);
  }
  // Linux packages carry the sandbox launcher the CLI runs when no system bwrap
  // is on PATH (Rust codex-resources/bwrap); the package is rejected as a daemon
  // package without it.
  if (target.goos === "linux") {
    const preparedBwrap = path.join(root, "third_party", "bwrap", "build", `${target.goos}-${target.goarch}`, "bin", "bwrap");
    if (existsSync(preparedBwrap)) {
      mkdirSync(path.join(targetDir, "codex-resources"), { recursive: true });
      cpSync(preparedBwrap, path.join(targetDir, "codex-resources", "bwrap"));
      try { chmodSync(path.join(targetDir, "codex-resources", "bwrap"), 0o755); } catch {}
    } else {
      console.warn(`bubblewrap for ${target.goos}/${target.goarch} is not prepared; run third_party/bwrap/prepare_bwrap.py --platform ${target.goos}-${target.goarch}`);
    }
  }
  run("npm", ["pack", packageDir, "--pack-destination", outputDir]);
}

const mainDir = path.join(stageRoot, "codex-go");
cpSync(path.join(root, "npm/codex"), mainDir, { recursive: true });
const mainPackagePath = path.join(mainDir, "package.json");
const mainPackage = JSON.parse(readFileSync(mainPackagePath, "utf8"));
mainPackage.version = version;
mainPackage.optionalDependencies = Object.fromEntries(
  targets.map((target) => [`@jacks001314/codex-go-${target.npmOS}-${target.npmCPU}`, version]),
);
writeFileSync(mainPackagePath, `${JSON.stringify(mainPackage, null, 2)}\n`);
for (const file of ["README.md", "LICENSE", "NOTICE"]) {
  try { cpSync(path.join(root, file), path.join(mainDir, file)); } catch {}
}
run("npm", ["pack", mainDir, "--pack-destination", outputDir]);
console.log(`npm packages written to ${outputDir}`);
