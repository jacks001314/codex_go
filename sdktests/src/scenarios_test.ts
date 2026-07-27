import assert from "node:assert/strict";
import test from "node:test";
import { getScenario } from "./scenarios.ts";

test("long-context structured output uses a strict service-compatible schema", () => {
  const scenario = getScenario("resume-long-context-tools");
  const schema: any = scenario.turns.at(-1)?.outputSchema;
  assert.equal(schema?.type, "object");
  assert.equal(schema?.properties?.token?.type, "string");
  assert.equal(schema?.properties?.state?.type, "string");
  assert.deepEqual(schema?.required, ["token", "state"]);
  assert.equal(schema?.additionalProperties, false);
});

test("multifile refactor compares deterministic paths and contracts the generated implementation", () => {
  const expected = getScenario("real-coding-multifile-refactor").expected;
  assert.deepEqual(expected.compareWorkspacePaths, [
    "legacy_math.py",
    "math_utils.py",
    "obsolete.txt",
    "test_service.py",
  ]);
  assert.deepEqual(expected.workspaceChanges, [
    { path: "legacy_math.py", change: "removed" },
    { path: "math_utils.py", change: "added" },
    { path: "obsolete.txt", change: "removed" },
    { path: "service.py", change: "modified" },
  ]);
});

test("Windows screen capture scenario preserves the requested prompt and audits image inspection", () => {
  const scenario = getScenario("windows-screen-capture-description");
  assert.equal(scenario.optIn, true);
  assert.deepEqual(scenario.platforms, ["windows"]);
  assert.equal(scenario.turns[0]?.prompt, "截屏，然后告诉我你看到了什么");
  assert.deepEqual(scenario.expected.requiredRolloutItemTypes, ["image_view"]);
  assert.equal(scenario.expected.commandOutputComparison, "informational");
  assert.equal(scenario.expected.requireSingleFinalAgentMessagePerTurn, undefined);
  assert.deepEqual(scenario.expected.workspaceChanges, [{ path: "screenshot.png", change: "added" }]);
  assert.deepEqual(scenario.expected.compareWorkspacePaths, []);
});
