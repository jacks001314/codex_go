import assert from "node:assert/strict";
import test from "node:test";
import { getScenario } from "./scenarios.ts";

test("init scenario preserves an existing AGENTS.md with the Rust prompt", () => {
  const scenario = getScenario("init-existing-agents-md");
  assert.equal(scenario.optIn, true);
  assert.match(scenario.turns[0]?.prompt ?? "", /do not overwrite or modify it/);
  assert.match(scenario.turns[0]?.prompt ?? "", /200-400 words is optimal/);
  assert.deepEqual(scenario.expected.forbiddenCompletedItemTypes, ["file_change"]);
  assert.deepEqual(scenario.expected.compareWorkspacePaths, ["AGENTS.md"]);
  assert.equal(scenario.expected.workspaceMutation, "none");
});

test("long-context structured output uses a strict service-compatible schema", () => {
  const scenario = getScenario("resume-long-context-tools");
  const schema: any = scenario.turns.at(-1)?.outputSchema;
  assert.equal(schema?.type, "object");
  assert.equal(schema?.properties?.token?.type, "string");
  assert.equal(schema?.properties?.state?.type, "string");
  assert.deepEqual(schema?.required, ["token", "state"]);
  assert.equal(schema?.additionalProperties, false);
});

test("interrupted command recovery remains opt-in while the Rust Windows baseline cannot complete it", () => {
  const scenario = getScenario("resume-tool-interrupted-command");
  assert.equal(scenario.optIn, true);
  assert.match(scenario.description, /Current Rust leaves the PowerShell child running/);
  assert.match(scenario.turns[0]?.prompt ?? "", /Start-Sleep -Seconds 30/);
});

test("multi-agent factorial scenario requires collaboration and the exact semantic result", () => {
	const scenario = getScenario("multi-agent-factorial-100");
  assert.equal(scenario.optIn, true);
  assert.equal(scenario.codexConfig?.features?.multi_agent_v2, undefined);
  assert.match(scenario.turns[0]?.prompt ?? "", /create multiple agents/);
  assert.equal(scenario.expected.minCompletedCollabSpawnCalls, 2);
	assert.deepEqual(scenario.expected.requiredCompletedCollabTools, ["collaboration.spawn_agent", "collaboration.wait_agent"]);
	assert.deepEqual(scenario.expected.forbiddenPublicEventPatterns, ["collaboration\\.", "gAAAA"]);
  assert.equal(scenario.expected.minSubagentRollouts, 2);
  assert.equal(scenario.expected.commandOutputComparison, "informational");
  assert.match("100! = 93326215443944152681699238856266700490715968264381621468592963895217599993229915608941463976156518286253697920827223758251185210916864000000000000000000000000", new RegExp(scenario.expected.finalAgentMessagePatterns?.[0] ?? "$^"));
});

test("multi-agent V2 lifecycle scenario covers named follow-up without workspace changes", () => {
	const scenario = getScenario("multi-agent-v2-lifecycle");
	assert.equal(scenario.optIn, true);
	assert.match(scenario.turns[0]?.prompt ?? "", /lifecycle_worker/);
	assert.match(scenario.turns[0]?.prompt ?? "", /followup_task/);
	assert.equal(scenario.expected.minSubagentRollouts, 1);
	assert.deepEqual(scenario.expected.subagentRolloutPatterns, ["LIFECYCLE_FIRST", "LIFECYCLE_SECOND"]);
	assert.deepEqual(scenario.expected.exactAgentMessages, ["MULTI_AGENT_V2_LIFECYCLE_OK"]);
	assert.equal(scenario.expected.workspaceMutation, "none");
});

test("multi-agent V2 auto completion requires the canonical parent FINAL_ANSWER envelope", () => {
	const scenario = getScenario("multi-agent-v2-auto-completion");
	assert.equal(scenario.optIn, true);
	assert.match(scenario.turns[0]?.prompt ?? "", /forbid it from calling send_message/);
	assert.equal(scenario.turns[1]?.resume, true);
	assert.deepEqual(scenario.expected.exactAgentMessages, ["ROOT_SAW_CHILD_AUTO_COMPLETION", "ROOT_RESUME_NO_NEW_CHILD"]);
	assert.deepEqual(scenario.expected.rootRolloutPatterns, [
		"Message Type: FINAL_ANSWER\\nTask name: /root\\nSender: /root/auto_worker\\nPayload:\\nCHILD_AUTO_COMPLETION_TOKEN",
	]);
	assert.deepEqual(scenario.expected.rootRolloutPatternCounts, {
		"Message Type: FINAL_ANSWER\\nTask name: /root\\nSender: /root/auto_worker\\nPayload:\\nCHILD_AUTO_COMPLETION_TOKEN": 1,
	});
	assert.equal(scenario.expected.requireStableThreadId, true);
	assert.deepEqual(scenario.expected.forbiddenRootRolloutPatterns, ["<subagent_notification>"]);
});

test("multi-agent V2 send_message scenario requires queued delivery evidence", () => {
	const scenario = getScenario("multi-agent-v2-send-message");
	assert.equal(scenario.optIn, true);
	assert.match(scenario.turns[0]?.prompt ?? "", /send_message exactly once/);
	assert.deepEqual(scenario.expected.requiredCompletedCollabTools, [
		"collaboration.spawn_agent", "collaboration.send_message", "collaboration.wait_agent",
	]);
	assert.deepEqual(scenario.expected.subagentRolloutPatterns, ["MESSAGE_RECEIVED_TOKEN"]);
	assert.deepEqual(scenario.expected.subagentFinalMessagePatterns, ["MESSAGE_RECEIVED_TOKEN"]);
	assert.deepEqual(scenario.expected.forbiddenPublicEventPatterns, ["collaboration\\.", "gAAAA"]);
});

test("multi-agent V2 interrupt scenario requires recovery on the same agent", () => {
	const scenario = getScenario("multi-agent-v2-interrupt");
	assert.equal(scenario.optIn, true);
	assert.match(scenario.turns[0]?.prompt ?? "", /followup_task on \/root\/interrupt_worker/);
	assert.deepEqual(scenario.expected.requiredCompletedCollabTools, [
		"collaboration.spawn_agent", "collaboration.interrupt_agent", "collaboration.list_agents", "collaboration.followup_task",
	]);
	assert.deepEqual(scenario.expected.subagentRolloutPatterns, ["INTERRUPT_RECOVERED_TOKEN"]);
	assert.deepEqual(scenario.expected.subagentFinalMessagePatterns, ["INTERRUPT_RECOVERED_TOKEN"]);
	assert.deepEqual(scenario.expected.rootRolloutPatterns, [
		'"previous_status"\\s*:\\s*"running"',
		'"agent_name"\\s*:\\s*"/root/interrupt_worker"\\s*,\\s*"agent_status"\\s*:\\s*"interrupted"',
		"Message Type: FINAL_ANSWER\\nTask name: /root\\nSender: /root/interrupt_worker\\nPayload:\\nINTERRUPT_RECOVERED_TOKEN",
	]);
});

test("multi-agent V2 missing-target scenario requires tool-error recovery", () => {
	const scenario = getScenario("multi-agent-v2-missing-target-recovery");
	assert.equal(scenario.optIn, true);
	assert.match(scenario.turns[0]?.prompt ?? "", /interrupt_agent exactly once/);
	assert.deepEqual(scenario.expected.requiredCompletedCollabTools, ["collaboration.spawn_agent"]);
	assert.deepEqual(scenario.expected.rootRolloutPatterns, ["missing_worker", "not found"]);
	assert.deepEqual(scenario.expected.exactAgentMessages, ["MISSING_TARGET_RECOVERY_OK"]);
});

test("Java quicksort audit checks deterministic side effects without comparing model-authored source bytes", () => {
	const scenario = getScenario("java-quicksort-file-order-audit");
	assert.equal(scenario.expected.eventSequenceComparison, "model-selected-tools");
	assert.equal(scenario.expected.commandOutputComparison, "informational");
	assert.deepEqual(scenario.expected.workspaceRequiredPaths, ["quicksort.java"]);
	assert.deepEqual(scenario.expected.compareWorkspacePaths, []);
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

test("standalone weather search scenario exercises one web lifecycle without shell fallback", () => {
  const scenario = getScenario("standalone-web-search-weather");
  assert.equal(scenario.optIn, true);
  assert.equal(scenario.codexConfig?.features?.standalone_web_search, true);
  assert.equal(scenario.codexConfig?.features?.code_mode, true);
  assert.equal(scenario.codexConfig?.features?.web_search_request, true);
  assert.deepEqual(scenario.expected.exactCompletedItemTypeCounts, { web_search: 1, agent_message: 1 });
  assert.equal(scenario.threadOptions.webSearchMode, undefined);
  assert.match(scenario.turns[0]?.prompt ?? "", /one weather operation containing all four locations/);
  assert.deepEqual(scenario.expected.requiredCompletedItemTypes, ["web_search", "agent_message"]);
  assert.deepEqual(scenario.expected.forbiddenCompletedItemTypes, ["command_execution", "file_change"]);
  assert.equal(scenario.expected.workspaceMutation, "none");
});
