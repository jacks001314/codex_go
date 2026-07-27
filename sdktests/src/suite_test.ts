import assert from "node:assert/strict";
import { mkdtempSync, readFileSync, rmSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import {
  createSuiteSummary,
  finishSuite,
  loadSuiteSummary,
  remainingSuiteScenarios,
  selectScenarioNames,
  suiteExitCode,
  suiteSummaryPath,
  updateSuiteScenario,
  type SuiteIdentity,
} from "./suite.ts";

const identity: SuiteIdentity = {
  platform: "windows",
  rustPath: "rust.exe",
  rustSha256: "rust-hash",
  goPath: "go.exe",
  goSha256: "go-hash",
  sdkPath: "sdk",
  sdkDistSha256: "sdk-hash",
};

test("suite resume skips completed scenarios and converts running to incomplete", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-suite-"));
  try {
    const summary = createSuiteSummary({
      suiteDir: root,
      identity,
      order: ["rust", "go"],
      scenarioNames: ["one", "two", "three"],
    });
    updateSuiteScenario(root, summary, "one", { status: "completed", artifactDir: "artifact-one" });
    updateSuiteScenario(root, summary, "two", { status: "running", startedAt: new Date().toISOString() });

    const resumed = loadSuiteSummary(root, identity);
    assert.deepEqual(remainingSuiteScenarios(resumed), ["two", "three"]);
    assert.equal(resumed.scenarios[1].status, "incomplete");
    assert.match(resumed.scenarios[1].error ?? "", /previous runner terminated/);

    finishSuite(root, resumed, "completed");
    const saved = JSON.parse(readFileSync(suiteSummaryPath(root), "utf8"));
    assert.equal(saved.status, "completed");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("suite resume rejects changed binary or SDK hashes", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-suite-"));
  try {
    createSuiteSummary({ suiteDir: root, identity, order: ["rust", "go"], scenarioNames: ["one"] });
    assert.throws(
      () => loadSuiteSummary(root, { ...identity, goSha256: "different" }),
      /goSha256 changed from go-hash to different/,
    );
    assert.throws(
      () => loadSuiteSummary(root, { ...identity, sdkDistSha256: "different" }),
      /sdkDistSha256 changed from sdk-hash to different/,
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("suite resume rejects changed explicit model settings", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-suite-"));
  try {
    const pinned = { ...identity, model: "gpt-5.4", modelReasoningEffort: "high" };
    createSuiteSummary({ suiteDir: root, identity: pinned, order: ["rust", "go"], scenarioNames: ["one"] });
    assert.throws(
      () => loadSuiteSummary(root, { ...pinned, model: "gpt-5.5" }),
      /model changed from gpt-5.4 to gpt-5.5/,
    );
    assert.throws(
      () => loadSuiteSummary(root, { ...pinned, modelReasoningEffort: "medium" }),
      /modelReasoningEffort changed from high to medium/,
    );
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("suite resume rejects a changed order without modifying the summary", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-suite-"));
  try {
    createSuiteSummary({ suiteDir: root, identity, order: ["rust", "go"], scenarioNames: ["one"] });
    const before = readFileSync(suiteSummaryPath(root), "utf8");
    assert.throws(
      () => loadSuiteSummary(root, identity, ["go", "rust"]),
      /different --order; expected rust-go/,
    );
    assert.equal(readFileSync(suiteSummaryPath(root), "utf8"), before);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("scenario selection starts at the requested approved scenario", () => {
  assert.deepEqual(selectScenarioNames(["one", "two", "three"], "two"), ["two", "three"]);
  assert.throws(() => selectScenarioNames(["one"], "missing"), /unknown --from scenario missing/);
});

test("suite exit code preserves completed mismatches across resume", () => {
  const now = new Date().toISOString();
  const summary = {
    version: 1 as const,
    id: "suite",
    createdAt: now,
    updatedAt: now,
    status: "running" as const,
    identity,
    order: ["rust", "go"] as ("rust" | "go")[],
    scenarioNames: ["one", "two"],
    scenarios: [
      {
        name: "one",
        status: "completed" as const,
        comparison: { status: "behavior_mismatch" as const, classification: "event_diff", firstMismatch: "x" },
      },
      { name: "two", status: "pending" as const },
    ],
  };
  assert.equal(suiteExitCode(summary), 1);
  summary.scenarios[0].comparison!.status = "infra_failure";
  assert.equal(suiteExitCode(summary), 2);
});
