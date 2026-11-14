import assert from "node:assert/strict";
import { readFileSync } from "node:fs";
import { EventEmitter } from "node:events";
import vm from "node:vm";
import test from "node:test";

// Run the actual spawn fallback with process boundaries replaced. This also
// exercises repeated POSIX signals on Windows without killing the test runner.
function fallback() {
 const source = readFileSync(new URL("./codex.js", import.meta.url), "utf8");
 const child = new EventEmitter();
 child.killed = false;
 const signals = [];
 child.kill = (signal) => { child.killed = true; signals.push(signal); };
 const proc = new EventEmitter();
 proc.argv = ["node", "codex.js"];
 proc.exit = (code) => { proc.exitCode = code; };
 vm.runInNewContext(source.slice(source.indexOf("const child = spawn(")), {
  spawn: () => child, process: proc, launchExecutable: "fixture", env: {}, console,
  constants: { signals: { SIGTERM: 15 } },
 });
 return { child, proc, signals };
}

test("spawn fallback forwards repeated signals and preserves numeric exit", () => {
 const { child, proc, signals } = fallback();
 for (const signal of ["SIGINT", "SIGTERM", "SIGHUP"]) {
  proc.emit(signal);
  proc.emit(signal);
 }
 assert.deepEqual(signals, ["SIGINT","SIGINT","SIGTERM","SIGTERM","SIGHUP","SIGHUP"]);
 child.emit("exit", 42, null);
 assert.equal(proc.exitCode, 42);
});

test("signal forwarding tolerates a child exiting concurrently", () => {
 const { child, proc } = fallback();
 child.kill = () => { throw new Error("child already exited"); };
 assert.doesNotThrow(() => proc.emit("SIGTERM"));
 child.emit("exit", 0, null);
 assert.equal(proc.exitCode, 0);
});
