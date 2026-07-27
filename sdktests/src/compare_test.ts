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

function writeArtifact(root: string, rust: any, go: any) {
  mkdirSync(path.join(root, "raw"), { recursive: true });
  writeFileSync(path.join(root, "raw", "rust.json"), JSON.stringify(rust));
  writeFileSync(path.join(root, "raw", "go.json"), JSON.stringify(go));
  writeFileSync(path.join(root, "run-manifest.json"), JSON.stringify({
    scenario: { expected: {
      expectedTurns: 1,
      requireUsage: false,
      workspaceMutation: "none",
      eventSequenceComparison: "semantic-tools",
      agentMessageComparison: "final-per-turn",
      requireStartedCompletedPairs: ["command_execution"],
      requireSingleFinalAgentMessagePerTurn: true,
      forbidEmptyCommandExecutions: true,
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
