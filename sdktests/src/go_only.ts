import { spawnSync } from "node:child_process";
import { existsSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import { pathToFileURL } from "node:url";
import { copyCodexHome, readJson, sdktestsRoot, writeJson } from "./util.ts";

const options = parseOptions(process.argv.slice(2));
const goPath = required(options.go, "--go");
const sdkPath = required(options.sdk, "--sdk");
const scenario = required(options.scenario, "--scenario");
if (scenario !== "git-commit-push") throw new Error(`Unknown Go-only scenario: ${scenario}`);

const stamp = new Date().toISOString().replaceAll(":", "").replaceAll(".", "");
const artifactDir = path.join(sdktestsRoot, "artifacts", `${stamp}-${scenario}-go-only`);
const tempRoot = path.join(sdktestsRoot, ".tmp", `${stamp}-${scenario}-go-only`);
const workspace = path.join(tempRoot, "workspace");
const remote = path.join(tempRoot, "origin.git");
const home = path.join(tempRoot, "home");
mkdirSync(workspace, { recursive: true });
mkdirSync(artifactDir, { recursive: true });

run("git", ["init", "--bare", remote], tempRoot);
run("git", ["init", "-b", "main"], workspace);
run("git", ["config", "user.name", "SDK Test"], workspace);
run("git", ["config", "user.email", "sdk-test@example.invalid"], workspace);
writeFileSync(path.join(workspace, "README.md"), "initial\n", "utf8");
run("git", ["add", "README.md"], workspace);
run("git", ["commit", "-m", "initial"], workspace);
run("git", ["remote", "add", "origin", remote], workspace);
run("git", ["push", "-u", "origin", "main"], workspace);
writeFileSync(path.join(workspace, "result.txt"), "GIT_COMMIT_PUSH_OK\n", "utf8");

copyCodexHome(path.join(os.homedir(), ".codex"), home);
const sdk = await import(pathToFileURL(path.join(sdkPath, "dist", "index.js")).href);
const client = new sdk.Codex({ codexPathOverride: goPath, env: { ...process.env, CODEX_HOME: home, NO_COLOR: "1" } });
const thread = client.startThread({ workingDirectory: workspace, sandboxMode: "danger-full-access", skipGitRepoCheck: false, approvalPolicy: "never", networkAccessEnabled: false, webSearchMode: "disabled" });
const events: any[] = [];
let error: any = null;
try {
  const turn = await thread.runStreamed("请执行 git commit and push。只提交当前工作区的 result.txt，commit message 必须是 sdk git push smoke，然后 push 到已有的 origin main。完成后简要说明结果。", {});
  for await (const event of turn.events) events.push(event);
} catch (value: any) {
  error = { name: value?.name, message: String(value?.message ?? value) };
}

const headMessage = maybe("git", ["log", "-1", "--pretty=%s"], workspace);
const status = maybe("git", ["status", "--porcelain"], workspace);
const localHead = maybe("git", ["rev-parse", "HEAD"], workspace);
const remoteHead = maybe("git", ["--git-dir", remote, "rev-parse", "refs/heads/main"], tempRoot);
const completedCommands = events.filter((event) => event.type === "item.completed" && event.item?.type === "command_execution");
const commandKeys = completedCommands.map((event) => JSON.stringify({ command: event.item.command, output: event.item.aggregated_output, exitCode: event.item.exit_code, status: event.item.status }));
const duplicateCommands = commandKeys.filter((key, index) => commandKeys.indexOf(key) !== index);
const finalMessages = events.filter((event) => event.type === "item.completed" && event.item?.type === "agent_message" && event.item?.phase === "final_answer");
const checks = {
  processCompleted: error === null,
  commitMessage: headMessage === "sdk git push smoke",
  cleanWorktree: status === "",
  pushedHead: Boolean(localHead) && localHead === remoteHead,
  resultTracked: maybe("git", ["ls-files", "--error-unmatch", "result.txt"], workspace) === "result.txt",
  noDuplicateCommands: duplicateCommands.length === 0,
  hasCommandLifecycle: completedCommands.length > 0,
  oneFinalMessage: finalMessages.length === 1,
};
const passed = Object.values(checks).every(Boolean);
writeJson(path.join(artifactDir, "go.json"), { scenario, error, events, git: { headMessage, status, localHead, remoteHead }, checks, duplicateCommands });
writeFileSync(path.join(artifactDir, "report.md"), `# Go-only git commit/push smoke\n\nStatus: ${passed ? "pass" : "fail"}\n\n${Object.entries(checks).map(([name, ok]) => `- ${ok ? "PASS" : "FAIL"} ${name}`).join("\n")}\n`, "utf8");
console.log(`sdktests go ${scenario}: ${passed ? "pass" : "fail"}`);
console.log(`sdktests go artifact: ${artifactDir}`);
if (!passed) process.exitCode = 1;
if (options.keep !== "true") rmSync(tempRoot, { recursive: true, force: true });

function run(command: string, args: string[], cwd: string) {
  const result = spawnSync(command, args, { cwd, encoding: "utf8", shell: false });
  if (result.status !== 0) throw new Error(`${command} ${args.join(" ")} failed: ${result.stderr || result.stdout}`);
}
function maybe(command: string, args: string[], cwd: string): string | null {
  const result = spawnSync(command, args, { cwd, encoding: "utf8", shell: false });
  return result.status === 0 ? String(result.stdout).trim() : null;
}
function parseOptions(args: string[]): Record<string, string> { const out: Record<string, string> = {}; for (let i = 0; i < args.length; i += 2) out[args[i].replace(/^--/, "")] = args[i + 1]; return out; }
function required(value: string | undefined, flag: string): string { if (!value) throw new Error(`Missing ${flag}`); return value; }
