import assert from "node:assert/strict";
import { mkdtempSync, mkdirSync, readFileSync, rmSync, writeFileSync } from "node:fs";
import os from "node:os";
import path from "node:path";
import test from "node:test";
import { compareArtifact } from "./compare.ts";

function event(type: string, item?: Record<string, unknown>) {
  return item ? { type, item } : { type };
}

function recording(events: any[]) {
  return {
    status: "ok",
    events,
    turns: [{ index: 0, events }],
    workspace: { before: {}, after: {} },
  };
}

function writeArtifact(
  root: string,
  rust: any,
  go: any,
  baseline?: Record<string, unknown>,
  expectedOverrides?: Record<string, unknown>,
) {
  mkdirSync(path.join(root, "raw"), { recursive: true });
  writeFileSync(path.join(root, "raw", "rust.json"), JSON.stringify(rust));
  writeFileSync(path.join(root, "raw", "go.json"), JSON.stringify(go));
  writeFileSync(path.join(root, "run-manifest.json"), JSON.stringify({
    baseline,
    scenario: { expected: {
      expectedTurns: 1,
      requireUsage: false,
      workspaceMutation: "none",
      eventSequenceComparison: "semantic-tools",
      agentMessageComparison: "final-per-turn",
      requireStartedCompletedPairs: ["command_execution"],
      requireSingleFinalAgentMessagePerTurn: true,
      forbidEmptyCommandExecutions: true,
      ...expectedOverrides,
    } },
  }));
}

function validEvents() {
  return [
    event("thread.started"),
    event("turn.started"),
    event("item.started", { id: "cmd-1", type: "command_execution", command: "Write-Output OK", status: "in_progress" }),
    event("item.completed", { id: "cmd-1", type: "command_execution", command: "Write-Output OK", status: "completed", exit_code: 0, aggregated_output: "OK" }),
    event("item.completed", { id: "msg-1", type: "agent_message", text: "DONE" }),
    event("turn.completed"),
  ];
}

test("compareArtifact accepts paired command and one final message", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const valid = recording(validEvents());
    writeArtifact(root, valid, valid);
    const result = compareArtifact(root);
    assert.equal(result.status, "pass");
    assert.equal(result.firstMismatch, null);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact enforces exact completed item type counts", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const rust = recording(validEvents());
    const goEvents = validEvents();
    goEvents.splice(4, 0, event("item.completed", { id: "cmd-2", type: "command_execution", command: "Write-Output OK", status: "completed", exit_code: 0, aggregated_output: "OK" }));
    writeArtifact(root, rust, recording(goEvents), undefined, {
      exactCompletedItemTypeCounts: { command_execution: 1, agent_message: 1 },
    });
    const result = compareArtifact(root);
    assert.equal(result.status, "behavior_mismatch");
    assert.equal(result.checks.find((check) => check.name === "rust: exact completed item type counts")?.ok, true);
    assert.equal(result.checks.find((check) => check.name === "go: exact completed item type counts")?.ok, false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact enforces linked subagent rollouts and the final semantic result", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const factorial = "93326215443944152681699238856266700490715968264381621468592963895217599993229915608941463976156518286253697920827223758251185210916864000000000000000000000000";
    const events = [
      event("thread.started"),
      event("turn.started"),
      event("item.completed", { id: "wait-1", type: "collab_tool_call", tool: "wait", status: "completed" }),
      event("item.completed", { id: "msg-1", type: "agent_message", text: `100! = ${factorial}` }),
      event("turn.completed"),
    ];
    const withSubagents = (count: number) => ({
      ...recording(events),
      threadId: "thread-root",
      rolloutRecords: Array.from({ length: count }, (_, index) => ({
        threadId: `thread-child-${index + 1}`,
        threadSource: "subagent",
        parentThreadId: "thread-root",
        jsonl: index === 0 ? "LIFECYCLE_FIRST\nLIFECYCLE_SECOND" : "",
      })),
    });
    const valid = withSubagents(2);
    writeArtifact(root, valid, valid, undefined, {
      requireStartedCompletedPairs: [],
      minCompletedCollabSpawnCalls: 2,
      requiredCompletedCollabTools: ["collaboration.spawn_agent", "collaboration.wait_agent"],
      minSubagentRollouts: 2,
      subagentRolloutPatterns: ["LIFECYCLE_FIRST", "LIFECYCLE_SECOND"],
      finalAgentMessagePatterns: [factorial],
    });
    assert.equal(compareArtifact(root).status, "pass");

    const missingSubagent = withSubagents(1);
    writeArtifact(root, valid, missingSubagent, undefined, {
      requireStartedCompletedPairs: [],
      minSubagentRollouts: 2,
      subagentRolloutPatterns: ["LIFECYCLE_FIRST", "LIFECYCLE_SECOND"],
      finalAgentMessagePatterns: [factorial],
    });
    const result = compareArtifact(root);
    assert.equal(result.status, "behavior_mismatch");
    assert.equal(result.checks.find((check) => check.name === "go: subagent rollout contract")?.ok, false);

    const duplicatedSubagent = withSubagents(1);
    duplicatedSubagent.rolloutRecords.push({ ...duplicatedSubagent.rolloutRecords[0] });
    writeArtifact(root, valid, duplicatedSubagent, undefined, {
      requireStartedCompletedPairs: [],
      minSubagentRollouts: 2,
      subagentRolloutPatterns: ["LIFECYCLE_FIRST", "LIFECYCLE_SECOND"],
      finalAgentMessagePatterns: [factorial],
    });
    const duplicateResult = compareArtifact(root);
    assert.equal(duplicateResult.status, "behavior_mismatch");
    assert.match(
      duplicateResult.checks.find((check) => check.name === "go: subagent rollout contract")?.detail ?? "",
      /unique linked subagent threads=1\/2/,
    );

    const missingPattern = withSubagents(2);
    missingPattern.rolloutRecords[0].jsonl = "LIFECYCLE_FIRST";
    writeArtifact(root, valid, missingPattern, undefined, {
      requireStartedCompletedPairs: [],
      minSubagentRollouts: 2,
      subagentRolloutPatterns: ["LIFECYCLE_FIRST", "LIFECYCLE_SECOND"],
      finalAgentMessagePatterns: [factorial],
    });
    const patternResult = compareArtifact(root);
    assert.equal(patternResult.status, "behavior_mismatch");
    assert.equal(patternResult.checks.find((check) => check.name === "go: subagent rollout patterns")?.ok, false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact recognizes Rust and Go image-view rollout records", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const rust = {
      ...recording(validEvents()),
      rolloutJsonl: `${JSON.stringify({
        type: "response_item",
        payload: {
          type: "custom_tool_call",
          name: "exec",
          input: 'const result = await tools.view_image({ path: "screenshot.png" }); image(result.image_url);',
        },
      })}\n`,
    };
    const go = {
      ...recording(validEvents()),
      rolloutJsonl: `${JSON.stringify({
        type: "item",
        item: { type: "function_call", name: "view_image", arguments: '{"path":"screenshot.png"}' },
      })}\n`,
    };
    writeArtifact(root, rust, go, undefined, { requiredRolloutItemTypes: ["image_view"] });
    const result = compareArtifact(root);
    assert.equal(result.status, "pass");
    assert.equal(result.checks.find((check) => check.name === "rust: required rollout item types")?.ok, true);
    assert.equal(result.checks.find((check) => check.name === "go: required rollout item types")?.ok, true);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact reports the first missing started event", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const rust = recording(validEvents());
    const goEvents = validEvents().filter((entry) => entry.type !== "item.started");
    writeArtifact(root, rust, recording(goEvents));
    const result = compareArtifact(root);
    assert.equal(result.status, "behavior_mismatch");
    assert.match(result.firstMismatch ?? "", /go: started\/completed pairs/);
    assert.match(result.firstMismatch ?? "", /started=0,completed=1/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact permits a resumed turn without the paired tool type", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const firstTurn = validEvents();
    const secondTurn = [
      event("thread.started"),
      event("turn.started"),
      event("item.completed", { id: "msg-2", type: "agent_message", text: "DONE" }),
      event("turn.completed"),
    ];
    const resumed = {
      status: "ok",
      events: [...firstTurn, ...secondTurn],
      turns: [{ index: 0, events: firstTurn }, { index: 1, events: secondTurn }],
      workspace: { before: {}, after: {} },
    };
    writeArtifact(root, resumed, resumed, undefined, { expectedTurns: 2 });
    const result = compareArtifact(root);
    assert.equal(result.status, "pass");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact rejects duplicate finals and empty commands", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const rust = recording(validEvents());
    const goEvents = validEvents();
    goEvents[2] = event("item.started", { id: "cmd-1", type: "command_execution", command: "", status: "in_progress" });
    goEvents[3] = event("item.completed", { id: "cmd-1", type: "command_execution", command: "", status: "completed", exit_code: 0, aggregated_output: "" });
    goEvents.splice(5, 0, event("item.completed", { id: "msg-2", type: "agent_message", text: "DONE AGAIN" }));
    writeArtifact(root, rust, recording(goEvents));
    const result = compareArtifact(root);
    assert.equal(result.status, "behavior_mismatch");
    assert.equal(result.checks.find((check) => check.name === "go: single final agent message")?.ok, false);
    assert.equal(result.checks.find((check) => check.name === "go: non-empty command executions")?.ok, false);
    assert.match(readFileSync(path.join(root, "report.md"), "utf8"), /FAIL go: single final agent message/);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact reports parity record drift", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const valid = recording(validEvents());
    writeArtifact(root, valid, valid, {
      parityRecordDrift: true,
      rustUpstreamCommit: "new-commit",
      parityRustUpstreamHead: "frozen-commit",
    });
    const result = compareArtifact(root);
    assert.equal(result.status, "pass");
    assert.equal(result.classification, "baseline-drift");
    const report = readFileSync(path.join(root, "report.md"), "utf8");
    assert.match(report, /Baseline drift: local SDK\/Rust checkout new-commit differs from parity\.json frozen-commit\./);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact classifies symmetric contract failures as SDK assumptions", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const events = validEvents().filter((entry) => entry.type !== "item.started");
    const result = (() => {
      writeArtifact(root, recording(events), recording(events));
      return compareArtifact(root);
    })();
    assert.equal(result.status, "behavior_mismatch");
    assert.equal(result.classification, "sdk-assumption");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact keeps symmetric failures as SDK assumptions during baseline drift", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const events = validEvents().filter((entry) => entry.type !== "item.started");
    writeArtifact(root, recording(events), recording(events), { parityRecordDrift: true });
    const result = compareArtifact(root);
    assert.equal(result.status, "behavior_mismatch");
    assert.equal(result.classification, "sdk-assumption");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact preserves an explicit platform-difference diagnosis", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const rust = recording(validEvents());
    const goEvents = validEvents().filter((entry) => entry.type !== "item.started");
    writeArtifact(root, rust, recording(goEvents), undefined, { mismatchClassification: "platform-difference" });
    const result = compareArtifact(root);
    assert.equal(result.status, "behavior_mismatch");
    assert.equal(result.classification, "platform-difference");
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});
