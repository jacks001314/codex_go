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

test("compareArtifact enforces both rollout compaction and warning contracts", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const events = [
      event("thread.started"),
      event("turn.started"),
      event("item.completed", { id: "warn-1", type: "error", message: "Heads up: Long threads may be compacted." }),
      event("item.completed", { id: "msg-1", type: "agent_message", text: "COMPACTION_OK" }),
      event("turn.completed"),
    ];
    const recordingWithCompaction = {
      ...recording(events),
      rolloutJsonl: JSON.stringify({ type: "compacted" }),
    };
    writeArtifact(root, recordingWithCompaction, recordingWithCompaction, undefined, {
      exactAgentMessages: ["COMPACTION_OK"],
      requireRolloutCompaction: true,
      requireStartedCompletedPairs: [],
      requireSingleFinalAgentMessagePerTurn: true,
      forbidEmptyCommandExecutions: false,
    });
    let result = compareArtifact(root);
    assert.equal(result.status, "pass");
    assert.equal(result.checks.find((check) => check.name === "rust: rollout compaction marker")?.ok, true);
    assert.equal(result.checks.find((check) => check.name === "rust: compaction warning item")?.ok, true);

    const missingMarker = { ...recordingWithCompaction, rolloutJsonl: "" };
    writeArtifact(root, missingMarker, missingMarker, undefined, {
      exactAgentMessages: ["COMPACTION_OK"],
      requireRolloutCompaction: true,
      requireStartedCompletedPairs: [],
      requireSingleFinalAgentMessagePerTurn: true,
      forbidEmptyCommandExecutions: false,
    });
    result = compareArtifact(root);
    assert.equal(result.status, "behavior_mismatch");
    assert.equal(result.checks.find((check) => check.name === "rust: rollout compaction marker")?.ok, false);

    const missingWarning = {
      ...recordingWithCompaction,
      events: events.filter((entry) => !String(entry.item?.message ?? "").includes("Heads up: Long threads")),
      turns: [{ index: 0, events: events.filter((entry) => !String(entry.item?.message ?? "").includes("Heads up: Long threads")) }],
    };
    writeArtifact(root, missingWarning, missingWarning, undefined, {
      exactAgentMessages: ["COMPACTION_OK"],
      requireRolloutCompaction: true,
      requireStartedCompletedPairs: [],
      requireSingleFinalAgentMessagePerTurn: true,
      forbidEmptyCommandExecutions: false,
    });
    result = compareArtifact(root);
    assert.equal(result.status, "behavior_mismatch");
    assert.equal(result.checks.find((check) => check.name === "rust: compaction warning item")?.ok, false);
  } finally {
    rmSync(root, { recursive: true, force: true });
  }
});

test("compareArtifact treats structured JSON object key order as insignificant", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const events = validEvents();
    events[4] = event("item.completed", {
      id: "msg-1",
      type: "agent_message",
      text: '{"state":"LONG_CONTEXT","token":"ALPHA_7K"}',
    });
    const valid = recording(events);
    const expected = { token: "ALPHA_7K", state: "LONG_CONTEXT" };
    writeArtifact(root, valid, valid, undefined, {
      structuredAgentMessages: [expected],
      agentMessageContracts: [{ structured: expected }],
    });

    const result = compareArtifact(root);
    assert.equal(result.status, "pass");
    assert.equal(result.checks.find((check) => check.name === "rust: structured agent messages")?.ok, true);
    assert.equal(result.checks.find((check) => check.name === "go: per-turn agent message contracts")?.ok, true);
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

test("compareArtifact proves hidden collaboration calls from paired root rollout items", () => {
	const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
	try {
		const events = [
			event("thread.started"),
			event("turn.started"),
			event("item.completed", { id: "msg-1", type: "agent_message", text: "DONE" }),
			event("turn.completed"),
		];
		const rustRollout = [
			{ type: "response_item", payload: { type: "function_call", namespace: "collaboration", name: "send_message", call_id: "call-send" } },
			{ type: "response_item", payload: { type: "function_call_output", call_id: "call-send", output: "" } },
		].map(JSON.stringify).join("\n");
		const goRollout = [
			{ type: "item", item: { type: "function_call", namespace: "collaboration", name: "send_message", call_id: "call-send" } },
			{ type: "item", item: { type: "tool_output", call_id: "call-send", data: { success: true } } },
		].map(JSON.stringify).join("\n");
		const rust = { ...recording(events), rolloutJsonl: rustRollout };
		const go = { ...recording(events), rolloutJsonl: goRollout };
		writeArtifact(root, rust, go, undefined, {
			requireStartedCompletedPairs: [],
			requiredCompletedCollabTools: ["collaboration.send_message"],
		});
		assert.equal(compareArtifact(root).status, "pass");

		go.rolloutJsonl = JSON.stringify({ type: "item", item: { type: "function_call", namespace: "collaboration", name: "send_message", call_id: "call-send" } });
		writeArtifact(root, rust, go, undefined, {
			requireStartedCompletedPairs: [],
			requiredCompletedCollabTools: ["collaboration.send_message"],
		});
		const missingOutput = compareArtifact(root);
		assert.equal(missingOutput.status, "behavior_mismatch");
		assert.equal(missingOutput.checks.find((check) => check.name === "go: collaboration contract")?.ok, false);
	} finally {
		rmSync(root, { recursive: true, force: true });
	}
});

test("compareArtifact requires a real subagent final and rejects public collaboration leaks", () => {
	const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
	try {
		const events = validEvents();
		const rust = {
			...recording(events),
			threadId: "thread-root",
			rolloutRecords: [{
				threadId: "thread-child",
				threadSource: "subagent",
				parentThreadId: "thread-root",
				jsonl: JSON.stringify({ type: "response_item", payload: { type: "message", role: "assistant", phase: "final_answer", content: [{ type: "output_text", text: "CHILD_OK" }] } }),
			}],
		};
		const go = {
			...recording(events),
			threadId: "thread-root",
			rolloutRecords: [{
				threadId: "thread-child",
				threadSource: "subagent",
				parentThreadId: "thread-root",
				jsonl: JSON.stringify({ type: "item", item: { type: "message", role: "assistant", phase: "final_answer", content: [{ type: "output_text", text: "CHILD_OK" }] } }),
			}],
		};
		writeArtifact(root, rust, go, undefined, {
			requireStartedCompletedPairs: [],
			subagentFinalMessagePatterns: ["CHILD_OK"],
			forbiddenPublicEventPatterns: ["collaboration\\.", "gAAAA"],
		});
		assert.equal(compareArtifact(root).status, "pass");

		go.rolloutRecords[0].jsonl = JSON.stringify({ type: "message", text: "prompt mentions CHILD_OK only" });
		writeArtifact(root, rust, go, undefined, {
			requireStartedCompletedPairs: [],
			subagentFinalMessagePatterns: ["CHILD_OK"],
			forbiddenPublicEventPatterns: ["collaboration\\.", "gAAAA"],
		});
		let result = compareArtifact(root);
		assert.equal(result.status, "behavior_mismatch");
		assert.equal(result.checks.find((check) => check.name === "go: subagent final message patterns")?.ok, false);

		go.rolloutRecords = rust.rolloutRecords;
		go.events = [
			...events.slice(0, -1),
			event("item.completed", { type: "function_call", name: "collaboration.send_message", arguments: "gAAAA-secret" }),
			events.at(-1),
		];
		writeArtifact(root, rust, go, undefined, {
			requireStartedCompletedPairs: [],
			subagentFinalMessagePatterns: ["CHILD_OK"],
			forbiddenPublicEventPatterns: ["collaboration\\.", "gAAAA"],
		});
		result = compareArtifact(root);
		assert.equal(result.status, "behavior_mismatch");
		assert.equal(result.checks.find((check) => check.name === "go: forbidden public event patterns")?.ok, false);
	} finally {
		rmSync(root, { recursive: true, force: true });
	}
});

test("compareArtifact classifies a child sampling 402 as infrastructure failure", () => {
	const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
	try {
		const rust = recording(validEvents());
		const go = {
			...recording(validEvents()),
			responsesDebug: JSON.stringify({ event: "sampling.failed", error: "responses API request failed with status 402: account balance exhausted" }),
		};
		writeArtifact(root, rust, go);
		const result = compareArtifact(root);
		assert.equal(result.status, "infra_failure");
		assert.equal(result.classification, "infra-failure");
		assert.equal(result.checks.find((check) => check.name === "go: backend infrastructure")?.ok, false);
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
        item: {
          type: "custom_tool_call",
          name: "exec",
          input: 'const result = await tools.view_image({ path: "screenshot.png" }); image(result);',
        },
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

test("compareArtifact rejects a duplicated canonical child completion in the root rollout", () => {
  const root = mkdtempSync(path.join(os.tmpdir(), "sdktests-compare-"));
  try {
    const envelope = "Message Type: FINAL_ANSWER\nTask name: /root\nSender: /root/auto_worker\nPayload:\nCHILD_AUTO_COMPLETION_TOKEN";
    const rust = { ...recording(validEvents()), rolloutJsonl: JSON.stringify({ payload: { text: envelope } }) };
    const go = { ...recording(validEvents()), rolloutJsonl: `${JSON.stringify({ item: { text: envelope } })}\n${JSON.stringify({ item: { text: envelope } })}` };
    writeArtifact(root, rust, go, undefined, { rootRolloutPatternCounts: { [envelope]: 1 } });
    const result = compareArtifact(root);
    assert.equal(result.status, "behavior_mismatch");
    assert.equal(result.classification, "go-bug");
    assert.equal(result.checks.find((check) => check.name === "rust: root rollout pattern counts")?.ok, true);
    assert.equal(result.checks.find((check) => check.name === "go: root rollout pattern counts")?.ok, false);
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
