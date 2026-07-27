import assert from "node:assert/strict";
import { existsSync, mkdirSync, mkdtempSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { HarnessAbortError, runParity } from "./runner.ts";
import { sdktestsRoot } from "./util.ts";

test("an aborted parity run records incomplete state and removes temporary work", async () => {
  const sdkPath = mkdtempSync(path.join(os.tmpdir(), "sdktests-sdk-"));
  mkdirSync(path.join(sdkPath, "dist"), { recursive: true });
  writeFileSync(path.join(sdkPath, "dist", "index.js"), "export {};\n");
  const controller = new AbortController();
  controller.abort();
  let artifactDir = "";
  try {
    await assert.rejects(
      runParity({
        scenario: "streaming-smoke",
        rustPath: process.execPath,
        goPath: process.execPath,
        sdkPath,
        signal: controller.signal,
      }),
      (error: any) => {
        artifactDir = error?.artifactDir ?? "";
        return error instanceof HarnessAbortError;
      },
    );
    assert.ok(artifactDir);
    const state = JSON.parse(readFileSync(path.join(artifactDir, "run-state.json"), "utf8"));
    assert.equal(state.status, "incomplete");
    assert.deepEqual(state.completedImplementations, []);
    assert.match(state.error, /aborted/);
    assert.equal(existsSync(path.join(sdktestsRoot, ".tmp", path.basename(artifactDir))), false);
  } finally {
    if (artifactDir) rmSync(artifactDir, { recursive: true, force: true });
    rmSync(sdkPath, { recursive: true, force: true });
  }
});
