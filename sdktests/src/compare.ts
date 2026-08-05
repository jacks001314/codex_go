import { existsSync, writeFileSync } from "node:fs";
import path from "node:path";
import { isDeepStrictEqual } from "node:util";
import { readJson, writeJson } from "./util.ts";

export type CompareClassification =
  | "parity"
  | "go-bug"
  | "sdk-assumption"
  | "baseline-drift"
  | "model-nondeterminism"
  | "platform-difference"
  | "infra-failure";

export type CompareResult = {
  status: "pass" | "behavior_mismatch" | "infra_failure";
  classification: CompareClassification;
  confidence: "low" | "medium" | "high";
  checks: { name: string; ok: boolean; detail?: string }[];
  eventTypes: Record<string, string[]>;
  firstMismatch: string | null;
};

export function compareArtifact(artifactDir: string): CompareResult {
  const rawDir = path.join(artifactDir, "raw");
  const rust = readJson(path.join(rawDir, "rust.json"));
  const go = readJson(path.join(rawDir, "go.json"));
  const manifestPath = path.join(artifactDir, "run-manifest.json");
  const manifest = existsSync(manifestPath) ? readJson(manifestPath) : {};
  const expected = manifest?.scenario?.expected ?? {};
  const expectedTurns = Number(expected.expectedTurns ?? 1);
  const expectsFailure = expected.outcome === "failure";

  const checks = [
    checkOutcome("rust", rust, expectsFailure, expected.errorPattern),
    checkOutcome("go", go, expectsFailure, expected.errorPattern),
    checkBackendInfrastructure("rust", rust, expectsFailure),
    checkBackendInfrastructure("go", go, expectsFailure),
    checkLifecycle("rust", rust, expectedTurns, expectsFailure),
    checkLifecycle("go", go, expectedTurns, expectsFailure),
    checkEventTypeSequence(rust, go, expected.eventSequenceComparison),
    checkItemTypeSequence(rust, go, expected.eventSequenceComparison),
    checkAgentMessage("rust", rust, expectsFailure),
    checkAgentMessage("go", go, expectsFailure),
    checkExpectedAgentMessages("rust", rust, expected.exactAgentMessages ?? legacyExactMessages(expected), expected.agentMessageComparison),
    checkExpectedAgentMessages("go", go, expected.exactAgentMessages ?? legacyExactMessages(expected), expected.agentMessageComparison),
    checkStructuredAgentMessages("rust", rust, expected.structuredAgentMessages),
    checkStructuredAgentMessages("go", go, expected.structuredAgentMessages),
    checkAgentMessageContracts("rust", rust, expected.agentMessageContracts),
    checkAgentMessageContracts("go", go, expected.agentMessageContracts),
    checkRequiredCompletedItemTypes("rust", rust, expected.requiredCompletedItemTypes),
    checkRequiredCompletedItemTypes("go", go, expected.requiredCompletedItemTypes),
    checkForbiddenCompletedItemTypes("rust", rust, expected.forbiddenCompletedItemTypes),
    checkForbiddenCompletedItemTypes("go", go, expected.forbiddenCompletedItemTypes),
    checkUniqueCompletedItems("rust", rust, expected.uniqueCompletedItemTypes),
    checkUniqueCompletedItems("go", go, expected.uniqueCompletedItemTypes),
    checkExactCompletedItemTypeCounts("rust", rust, expected.exactCompletedItemTypeCounts),
    checkExactCompletedItemTypeCounts("go", go, expected.exactCompletedItemTypeCounts),
    checkUniqueCommandExecutions("rust", rust, expected.uniqueCommandExecutions),
    checkUniqueCommandExecutions("go", go, expected.uniqueCommandExecutions),
    checkStartedCompletedPairs("rust", rust, expected.requireStartedCompletedPairs),
    checkStartedCompletedPairs("go", go, expected.requireStartedCompletedPairs),
    checkSingleFinalAgentMessagePerTurn("rust", rust, expected.requireSingleFinalAgentMessagePerTurn),
    checkSingleFinalAgentMessagePerTurn("go", go, expected.requireSingleFinalAgentMessagePerTurn),
    checkNoEmptyCommandExecutions("rust", rust, expected.forbidEmptyCommandExecutions),
    checkNoEmptyCommandExecutions("go", go, expected.forbidEmptyCommandExecutions),
    checkCommentaryBeforeTool("rust", rust, expected.requireCommentaryBeforeTool),
    checkCommentaryBeforeTool("go", go, expected.requireCommentaryBeforeTool),
    checkCollaborationContract("rust", rust, expected.minCompletedCollabSpawnCalls, expected.requiredCompletedCollabTools),
    checkCollaborationContract("go", go, expected.minCompletedCollabSpawnCalls, expected.requiredCompletedCollabTools),
    checkSubagentRollouts("rust", rust, expected.minSubagentRollouts),
    checkSubagentRollouts("go", go, expected.minSubagentRollouts),
    checkSubagentRolloutPatterns("rust", rust, expected.subagentRolloutPatterns),
    checkSubagentRolloutPatterns("go", go, expected.subagentRolloutPatterns),
    checkSubagentFinalMessagePatterns("rust", rust, expected.subagentFinalMessagePatterns),
    checkSubagentFinalMessagePatterns("go", go, expected.subagentFinalMessagePatterns),
    checkRootRolloutPatterns("rust", rust, expected.rootRolloutPatterns, expected.forbiddenRootRolloutPatterns),
    checkRootRolloutPatterns("go", go, expected.rootRolloutPatterns, expected.forbiddenRootRolloutPatterns),
    checkRootRolloutPatternCounts("rust", rust, expected.rootRolloutPatternCounts),
    checkRootRolloutPatternCounts("go", go, expected.rootRolloutPatternCounts),
    checkForbiddenPublicEventPatterns("rust", rust, expected.forbiddenPublicEventPatterns),
    checkForbiddenPublicEventPatterns("go", go, expected.forbiddenPublicEventPatterns),
    checkFinalAgentMessagePatterns("rust", rust, expected.finalAgentMessagePatterns),
    checkFinalAgentMessagePatterns("go", go, expected.finalAgentMessagePatterns),
    checkExpectedCommandExecutions("rust", rust, expected.commandExecutions, expected.commandOutputComparison),
    checkExpectedCommandExecutions("go", go, expected.commandExecutions, expected.commandOutputComparison),
    checkExpectedFileChanges("rust", rust, expected.fileChanges),
    checkExpectedFileChanges("go", go, expected.fileChanges),
    checkThreadContinuity("rust", rust, expected.requireStableThreadId),
    checkThreadContinuity("go", go, expected.requireStableThreadId),
    checkAgentMessageCount(rust, go, expected.agentMessageComparison),
    checkErrorItemSymmetry(rust, go),
    checkUsage("rust", rust, expectedTurns, expected.requireUsage !== false),
    checkUsage("go", go, expectedTurns, expected.requireUsage !== false),
    checkCommandExecutionSemantics(rust, go, expected.commandOutputComparison),
    checkWorkspaceUnchanged("rust", rust, expected.workspaceMutation === "none"),
    checkWorkspaceUnchanged("go", go, expected.workspaceMutation === "none"),
    checkExpectedWorkspaceChanges("rust", rust, expected.workspaceChanges),
    checkExpectedWorkspaceChanges("go", go, expected.workspaceChanges),
    checkWorkspaceRequiredPaths("rust", rust, expected.workspaceRequiredPaths),
    checkWorkspaceRequiredPaths("go", go, expected.workspaceRequiredPaths),
    checkRequiredRolloutItemTypes("rust", rust, expected.requiredRolloutItemTypes),
    checkRequiredRolloutItemTypes("go", go, expected.requiredRolloutItemTypes),
    checkRequireRolloutCompaction("rust", rust, expected.requireRolloutCompaction),
    checkRequireRolloutCompaction("go", go, expected.requireRolloutCompaction),
    checkCompactionWarning("rust", rust, expected.requireRolloutCompaction),
    checkCompactionWarning("go", go, expected.requireRolloutCompaction),
    checkWorkspaceSideEffects(rust, go, expected.compareWorkspacePaths),
  ];
  const firstFailure = checks.find((check) => !check.ok);
  const baselineDrift = Boolean(
    manifest?.baseline?.rustBaselineDrift || manifest?.baseline?.parityRecordDrift,
  );
  const infraFailure = !expectsFailure && checks.some(
    (check) => !check.ok && (
      check.name.endsWith(": process completed") ||
      check.name.endsWith(": backend infrastructure") ||
      check.name.endsWith(": lifecycle")
    ),
  );
  const parityFailure = checks.some(
    (check) =>
      !check.ok &&
      (check.name.startsWith("strict ") ||
        check.name === "semantic completed tool sequence" ||
        check.name.endsWith(": root rollout patterns") ||
        check.name.endsWith(": root rollout pattern counts") ||
        check.name === "agent message count" ||
        check.name === "final agent message count" ||
        check.name === "error item symmetry" ||
        check.name === "command execution semantics" ||
        check.name === "workspace side effects match"),
  );
  const status = infraFailure ? "infra_failure" : firstFailure ? "behavior_mismatch" : "pass";
  const classification = classifyResult({
    infraFailure,
    baselineDrift,
    firstFailure,
    parityFailure,
    checks,
    hint: expected.mismatchClassification,
  });
  const result: CompareResult = {
    status,
    classification,
    confidence: firstFailure ? "medium" : "high",
    checks,
    eventTypes: {
      rust: eventTypes(rust),
      go: eventTypes(go),
    },
    firstMismatch: firstFailure ? `${firstFailure.name}: ${firstFailure.detail ?? "check failed"}` : null,
  };
  writeJson(path.join(artifactDir, "comparison.json"), result);
  writeFileSync(path.join(artifactDir, "report.md"), renderReport(result, artifactDir, manifest), "utf8");
  return result;
}

function classifyResult(input: {
  infraFailure: boolean;
  baselineDrift: boolean;
  firstFailure: { name: string } | undefined;
  parityFailure: boolean;
  checks: { name: string; ok: boolean }[];
  hint: unknown;
}): CompareClassification {
  if (input.infraFailure) return "infra-failure";
  if (!input.firstFailure) return input.baselineDrift ? "baseline-drift" : "parity";

  const hint = parseMismatchClassification(input.hint);
  if (hint) return hint;
  if (hasSymmetricImplementationFailure(input.checks)) return "sdk-assumption";
  if (input.baselineDrift) return "baseline-drift";
  return input.parityFailure ? "go-bug" : "model-nondeterminism";
}

function parseMismatchClassification(value: unknown): "sdk-assumption" | "platform-difference" | undefined {
  return value === "sdk-assumption" || value === "platform-difference" ? value : undefined;
}

function hasSymmetricImplementationFailure(checks: { name: string; ok: boolean }[]): boolean {
  const failed = checks.filter((check) => !check.ok).map((check) => check.name);
  const rust = new Set(failed.filter((name) => name.startsWith("rust: ")).map((name) => name.slice("rust: ".length)));
  const go = new Set(failed.filter((name) => name.startsWith("go: ")).map((name) => name.slice("go: ".length)));
  return rust.size > 0 && [...rust].some((name) => go.has(name));
}

function checkOutcome(label: string, recording: any, expectsFailure: boolean, errorPattern: unknown) {
  if (!expectsFailure) {
    const ok = recording.status === "ok";
    return { name: `${label}: process completed`, ok, detail: ok ? undefined : recording.error?.message ?? "run failed" };
  }
  const message = String(recording.error?.message ?? "");
  const pattern = typeof errorPattern === "string" ? new RegExp(errorPattern, "i") : null;
  const ok = recording.status === "error" && message.length > 0 && (!pattern || pattern.test(message));
  return { name: `${label}: expected failure`, ok, detail: ok ? undefined : `${label} status=${recording.status} error=${message}` };
}

function checkBackendInfrastructure(label: string, recording: any, expectsFailure: boolean) {
  if (expectsFailure) {
    return { name: `${label}: backend infrastructure`, ok: true, detail: "expected-failure scenario permits backend errors" };
  }
  const failures: string[] = [];
  for (const line of String(recording?.responsesDebug ?? "").split(/\r?\n/)) {
    if (!line.trim()) continue;
    let entry: any;
    try {
      entry = JSON.parse(line);
    } catch {
      continue;
    }
    if (entry?.event !== "sampling.failed") continue;
    const error = String(entry?.error ?? entry?.message ?? "");
    if (isBackendInfrastructureError(error)) failures.push(error);
  }
  return {
    name: `${label}: backend infrastructure`,
    ok: failures.length === 0,
    detail: failures.length === 0 ? undefined : `${label}: ${failures.join("; ")}`,
  };
}

function isBackendInfrastructureError(error: string): boolean {
  return /\bstatus\s+(?:401|402|403|408|409|429|5\d\d)\b/i.test(error) ||
    /server[_ -]?overloaded|rate[_ -]?limit|insufficient[_ -]?quota|billing|account balance|service unavailable|gateway timeout/i.test(error);
}

function checkLifecycle(label: string, recording: any, expectedTurns: number, expectsFailure: boolean) {
  const types = eventTypes(recording);
  if (expectsFailure) {
    const completed = types.includes("turn.completed");
    return { name: `${label}: failure lifecycle`, ok: !completed, detail: completed ? `${label} unexpectedly completed a turn` : undefined };
  }
  const count = (type: string) => types.filter((value) => value === type).length;
  const ok =
    types[0] === "thread.started" &&
    count("thread.started") >= expectedTurns &&
    count("turn.started") >= expectedTurns &&
    count("turn.completed") === expectedTurns &&
    types.at(-1) === "turn.completed";
  return { name: `${label}: lifecycle`, ok, detail: ok ? undefined : `${label} events: ${types.join(" -> ")}` };
}

function checkAgentMessage(label: string, recording: any, expectsFailure: boolean) {
  if (expectsFailure) {
    return { name: `${label}: agent message`, ok: agentMessages(recording).length === 0, detail: "expected failure must not produce an agent message" };
  }
  const messages = agentMessages(recording);
  const ok = messages.some((text) => text.trim().length > 0);
  return { name: `${label}: agent message`, ok, detail: ok ? undefined : `${label} has no completed agent_message text` };
}

function legacyExactMessages(expected: any): string[] | undefined {
  return typeof expected?.exactAgentMessage === "string" ? [expected.exactAgentMessage] : undefined;
}

function checkExpectedAgentMessages(label: string, recording: any, expected: unknown, comparison: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: exact agent messages`, ok: true, detail: "artifact has no exact-message contract" };
  }
  const messages = comparison === "final-per-turn" ? finalAgentMessagesByTurn(recording) : agentMessages(recording);
  const ok = JSON.stringify(messages) === JSON.stringify(expected);
  return {
    name: `${label}: exact agent messages`,
    ok,
    detail: ok ? undefined : `${label} messages: ${JSON.stringify(messages)}`,
  };
}

function checkStructuredAgentMessages(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: structured agent messages`, ok: true, detail: "artifact has no structured-message contract" };
  }
  const messages = finalAgentMessagesByTurn(recording);
  let parsed: unknown[];
  try {
    parsed = messages.map((message) => JSON.parse(message));
  } catch (error: any) {
    return { name: `${label}: structured agent messages`, ok: false, detail: `${label} JSON parse failed: ${error?.message ?? error}` };
  }
  const ok = isDeepStrictEqual(parsed, expected);
  return {
    name: `${label}: structured agent messages`,
    ok,
    detail: ok ? undefined : `${label}: ${JSON.stringify(parsed)}; expected: ${JSON.stringify(expected)}`,
  };
}

function finalAgentMessagesByTurn(recording: any): string[] {
  if (Array.isArray(recording.turns) && recording.turns.length > 0) {
    return recording.turns.flatMap((turn: any) => {
      const messages = agentMessages({ events: turn.events });
      return messages.length > 0 ? [messages.at(-1)!] : [];
    });
  }
  const messages = agentMessages(recording);
  return messages.length > 0 ? [messages.at(-1)!] : [];
}

function checkAgentMessageContracts(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: per-turn agent message contracts`, ok: true, detail: "artifact has no per-turn message contract" };
  }
  const messages = finalAgentMessagesByTurn(recording);
  const ok = messages.length === expected.length && expected.every((contract: any, index: number) => {
    if (typeof contract?.exact === "string") {
      return messages[index] === contract.exact;
    }
    if (Object.prototype.hasOwnProperty.call(contract ?? {}, "structured")) {
      try {
        return isDeepStrictEqual(JSON.parse(messages[index]), contract.structured);
      } catch {
        return false;
      }
    }
    return false;
  });
  return {
    name: `${label}: per-turn agent message contracts`,
    ok,
    detail: ok ? undefined : `${label}: ${JSON.stringify(messages)}; expected: ${JSON.stringify(expected)}`,
  };
}

function checkRequiredCompletedItemTypes(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: required completed item types`, ok: true, detail: "artifact has no required-item contract" };
  }
  const actual = itemTypes(recording);
  const missing = expected.filter((type) => !actual.includes(String(type)));
  return {
    name: `${label}: required completed item types`,
    ok: missing.length === 0,
    detail: missing.length === 0 ? undefined : `${label} missing: ${missing.join(", ")}; actual: ${actual.join(", ")}`,
  };
}

function checkForbiddenCompletedItemTypes(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: forbidden completed item types`, ok: true, detail: "artifact has no forbidden-item contract" };
  }
  const actual = itemTypes(recording);
  const present = expected.filter((type) => actual.includes(String(type)));
  return {
    name: `${label}: forbidden completed item types`,
    ok: present.length === 0,
    detail: present.length === 0 ? undefined : `${label} unexpected: ${present.join(", ")}`,
  };
}

function checkUniqueCompletedItems(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: unique completed items`, ok: true, detail: "artifact has no uniqueness contract" };
  }
  const duplicates: string[] = [];
  for (const type of expected.map(String)) {
    for (const turn of recording.turns ?? [{ index: 0, events: recording.events ?? [] }]) {
      const seen = new Set<string>();
      for (const event of turn.events ?? []) {
        if (event.type !== "item.completed" || event.item?.type !== type) continue;
        const item = event.item ?? {};
        const key = String(item.id ?? item.callId ?? item.call_id ?? `${type}:${item.text ?? ""}:${item.server ?? ""}:${item.tool ?? ""}`);
        if (seen.has(key)) duplicates.push(`turn${turn.index}:${type}:${key}`);
        seen.add(key);
      }
    }
  }
  return { name: `${label}: unique completed items`, ok: duplicates.length === 0, detail: duplicates.length === 0 ? undefined : duplicates.join(", ") };
}

function checkUniqueCommandExecutions(label: string, recording: any, required: unknown) {
  if (!required) {
    return { name: `${label}: unique command executions`, ok: true, detail: "scenario has no command uniqueness contract" };
  }
  const duplicates: string[] = [];
  for (const turn of recording.turns ?? [{ index: 0, events: recording.events ?? [] }]) {
    const seen = new Set<string>();
    for (const event of turn.events ?? []) {
      if (event.type !== "item.completed" || event.item?.type !== "command_execution") continue;
      const key = JSON.stringify({
        command: String(event.item?.command ?? "").trim(),
        status: String(event.item?.status ?? ""),
        exitCode: event.item?.exit_code,
        output: String(event.item?.aggregated_output ?? "").replaceAll("\r\n", "\n").trim(),
      });
      if (seen.has(key)) duplicates.push(`turn${turn.index}:${key}`);
      seen.add(key);
    }
  }
  return { name: `${label}: unique command executions`, ok: duplicates.length === 0, detail: duplicates.length === 0 ? undefined : duplicates.join(", ") };
}

function checkCommentaryBeforeTool(label: string, recording: any, required: unknown) {
  if (!required) {
    return { name: `${label}: commentary before tool`, ok: true, detail: "scenario has no commentary ordering contract" };
  }
  const failures: string[] = [];
  for (const turn of recording.turns ?? [{ index: 0, events: recording.events ?? [] }]) {
    const events = turn.events ?? [];
    const firstTool = events.findIndex((event: any) => event.type === "item.started" && ["command_execution", "web_search", "mcp_tool_call", "tool_call"].includes(event.item?.type));
    if (firstTool < 0) continue;
    const commentary = events.findIndex((event: any, index: number) => index < firstTool && event.type === "item.completed" && event.item?.type === "agent_message" && (event.item?.phase === "commentary" || event.item?.phase == null));
    if (commentary < 0) failures.push(`turn${turn.index}: first tool at event ${firstTool} has no preceding assistant commentary`);
  }
  return { name: `${label}: commentary before tool`, ok: failures.length === 0, detail: failures.length === 0 ? undefined : failures.join(", ") };
}

function checkThreadContinuity(label: string, recording: any, required: unknown) {
  if (!required) {
    return { name: `${label}: thread ID continuity`, ok: true, detail: "scenario does not require resume" };
  }
  const ids = (recording.threadIds ?? []).filter((value: unknown) => typeof value === "string" && value.length > 0);
  const ok = ids.length > 1 && ids.every((id: string) => id === ids[0]);
  return {
    name: `${label}: thread ID continuity`,
    ok,
    detail: ok ? undefined : `${label} thread IDs: ${JSON.stringify(ids)}`,
  };
}

function checkExpectedCommandExecutions(label: string, recording: any, expected: unknown, comparison: unknown) {
  if (!Array.isArray(expected)) {
    return { name: `${label}: expected command executions`, ok: true, detail: "artifact has no command contract" };
  }
  const actual = normalizeCommandOrder(commandExecutions(recording), comparison);
  expected = normalizeCommandOrder(expected, comparison);
  const ok = expected.every((contract: any, index: number) => {
    const item = actual[index];
    if (!item || item.status !== contract.status || item.exitCode !== contract.exitCode) {
      return false;
    }
    if (typeof contract.output === "string" && item.output !== contract.output) {
      return false;
    }
    return typeof contract.outputPattern !== "string" || new RegExp(contract.outputPattern).test(item.output);
  }) && actual.length === expected.length;
  return {
    name: `${label}: expected command executions`,
    ok,
    detail: ok ? undefined : `${label}: ${JSON.stringify(actual)}; expected: ${JSON.stringify(expected)}`,
  };
}

function checkExpectedFileChanges(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected)) {
    return { name: `${label}: expected file changes`, ok: true, detail: "artifact has no file-change contract" };
  }
  const actual = (recording.events ?? [])
    .filter((event: any) => event.type === "item.completed" && event.item?.type === "file_change")
    .map((event: any) => ({ status: String(event.item?.status ?? ""), stderr: String(event.item?.stderr ?? "") }));
  const ok = actual.length === expected.length && expected.every((contract: any, index: number) => {
    const item = actual[index];
    return Boolean(item) && item.status === contract.status &&
      (typeof contract.stderrPattern !== "string" || new RegExp(contract.stderrPattern, "i").test(item.stderr));
  });
  return { name: `${label}: expected file changes`, ok, detail: ok ? undefined : `${label}: ${JSON.stringify(actual)}; expected: ${JSON.stringify(expected)}` };
}

function checkEventTypeSequence(rust: any, go: any, comparison: unknown) {
  if (comparison === "model-selected-tools") {
    return { name: "model-selected event lifecycle", ok: true, detail: "scenario permits each model to choose whether and how many tools to invoke; per-side lifecycle, ordering, and uniqueness remain contractual" };
  }
  if (comparison === "semantic-tools") {
    return { name: "semantic event lifecycle", ok: true, detail: "scenario compares completed tool semantics and terminal lifecycle; commentary and started-event interleaving are non-contractual" };
  }
  if (comparison === "compaction-tolerant") {
    const left = withoutCompactionWarning(rust).map((event: any) => String(event.type));
    const right = withoutCompactionWarning(go).map((event: any) => String(event.type));
    const ok = JSON.stringify(left) === JSON.stringify(right);
    return {
      name: "strict event type sequence",
      ok,
      detail: ok
        ? "compaction warning error item is an independent event whose position Rust varies across runs; removed from strict ordering"
        : `rust: ${left.join(" -> ")}; go: ${right.join(" -> ")}`,
    };
  }
  const left = eventTypes(rust);
  const right = eventTypes(go);
  const strictOk = JSON.stringify(left) === JSON.stringify(right);
  if (strictOk) {
    return { name: "strict event type sequence", ok: true };
  }
  const compatibleLeft = comparableEventTypes(rust);
  const compatibleRight = comparableEventTypes(go);
  const compatible = JSON.stringify(compatibleLeft) === JSON.stringify(compatibleRight);
  return {
    name: "strict event type sequence",
    ok: compatible,
    detail: compatible
      ? `compatible after recoverable reconnect events: rust: ${left.join(" -> ")}; go: ${right.join(" -> ")}`
      : `rust: ${left.join(" -> ")}; go: ${right.join(" -> ")}`,
  };
}

function comparableEventTypes(recording: any): string[] {
  return (recording.events ?? []).flatMap((event: any) => {
    if (event.type !== "error") {
      return [String(event.type)];
    }
    const message = String(event.message ?? "");
    return /^Reconnecting\.\.\. \d+\/\d+ \(/.test(message) ? [] : ["error"];
  });
}

function checkItemTypeSequence(rust: any, go: any, comparison: unknown) {
  if (comparison === "model-selected-tools") {
    return { name: "model-selected completed tool sequence", ok: true, detail: "scenario does not require identical model-selected tool counts" };
  }
  if (comparison === "compaction-tolerant") {
    const left = withoutCompactionWarning(rust)
      .filter((event: any) => event.type === "item.completed")
      .map((event: any) => String(event.item?.type));
    const right = withoutCompactionWarning(go)
      .filter((event: any) => event.type === "item.completed")
      .map((event: any) => String(event.item?.type));
    const ok = JSON.stringify(left) === JSON.stringify(right);
    return {
      name: "strict completed item type sequence",
      ok,
      detail: ok ? undefined : `rust: ${left.join(" -> ")}; go: ${right.join(" -> ")}`,
    };
  }
  if (comparison === "semantic-tools") {
    const comparable = (recording: any) => (recording.events ?? [])
      .filter((event: any) => event.type === "item.completed")
      .filter((event: any) => event.item?.type !== "agent_message")
      .filter((event: any) => event.item?.type !== "error")
      .filter((event: any) => event.item?.type !== "web_search")
      .filter((event: any) => !(event.item?.type === "file_change" && event.item?.status === "failed"))
      .map((event: any) => String(event.item?.type));
    const left = comparable(rust);
    const right = comparable(go);
    const ok = JSON.stringify(left) === JSON.stringify(right);
    return { name: "semantic completed tool sequence", ok, detail: ok ? undefined : `rust: ${left.join(" -> ")}; go: ${right.join(" -> ")}` };
  }
  const left = itemTypes(rust);
  const right = itemTypes(go);
  const ok = JSON.stringify(left) === JSON.stringify(right);
  return { name: "strict completed item type sequence", ok, detail: ok ? undefined : `rust: ${left.join(" -> ")}; go: ${right.join(" -> ")}` };
}

function checkAgentMessageCount(rust: any, go: any, comparison: unknown) {
  if (comparison === "final-per-turn") {
    const left = finalAgentMessagesByTurn(rust).length;
    const right = finalAgentMessagesByTurn(go).length;
    const ok = left === right;
    return { name: "final agent message count", ok, detail: ok ? undefined : `rust: ${left}; go: ${right}` };
  }
  const left = agentMessages(rust).length;
  const right = agentMessages(go).length;
  const ok = left === right;
  return { name: "agent message count", ok, detail: ok ? undefined : `rust: ${left}; go: ${right}` };
}

function checkErrorItemSymmetry(rust: any, go: any) {
  const actionable = (recording: any) => errorItems(recording).filter((event: any) => {
    const message = String(event.item?.message ?? event.message ?? "");
    return !message.includes("Under-development features enabled") && !message.includes("web_search_request` is deprecated");
  });
  const left = actionable(rust).length;
  const right = actionable(go).length;
  const ok = left === right;
  return { name: "error item symmetry", ok, detail: ok ? undefined : `rust: ${left}; go: ${right}` };
}

function checkUsage(label: string, recording: any, expectedTurns: number, required: boolean) {
  if (!required) {
    return { name: `${label}: usage`, ok: true, detail: "scenario does not require usage" };
  }
  const completed = (recording.events ?? []).filter((event: any) => event.type === "turn.completed");
  const withUsage = completed.filter((event: any) => Boolean(event.usage));
  const ok = completed.length === expectedTurns && withUsage.length === expectedTurns;
  return { name: `${label}: usage`, ok, detail: ok ? undefined : `${label} usage events: ${withUsage.length}/${expectedTurns}` };
}

function checkCommandExecutionSemantics(rust: any, go: any, comparison: unknown) {
  if (comparison === "informational") {
    return { name: "command execution semantics", ok: true, detail: "scenario permits model-selected command differences; per-side lifecycle and uniqueness remain contractual" };
  }
  const left = normalizeCommandOrder(commandExecutions(rust), comparison);
  const right = normalizeCommandOrder(commandExecutions(go), comparison);
  const comparable = (items: any[]) =>
    comparison === "status-exit-code"
      ? items.map((item) => ({ status: item.status, exitCode: item.exitCode }))
      : items;
  const ok = JSON.stringify(comparable(left)) === JSON.stringify(comparable(right));
  return {
    name: "command execution semantics",
    ok,
    detail: ok ? undefined : `rust: ${JSON.stringify(left)}; go: ${JSON.stringify(right)}`,
  };
}

function normalizeCommandOrder(items: any[], comparison: unknown) {
  if (comparison === "unordered") {
    return [...items].sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  }
  if (comparison !== "parallel-prefix-unordered" || items.length < 2) return items;
  const prefix = items.slice(0, 2).sort((left, right) => JSON.stringify(left).localeCompare(JSON.stringify(right)));
  return [...prefix, ...items.slice(2)];
}

function checkExactCompletedItemTypeCounts(label: string, recording: any, expected: unknown) {
  if (!expected || typeof expected !== "object" || Array.isArray(expected)) {
    return { name: `${label}: exact completed item type counts`, ok: true, detail: "artifact has no exact-count contract" };
  }
  const actual = itemTypes(recording);
  const mismatches = Object.entries(expected as Record<string, unknown>).flatMap(([type, rawCount]) => {
    const wanted = Number(rawCount);
    const count = actual.filter((actualType) => actualType === type).length;
    return Number.isInteger(wanted) && wanted >= 0 && count === wanted
      ? []
      : [`${type}=${count}, expected=${String(rawCount)}`];
  });
  return {
    name: `${label}: exact completed item type counts`,
    ok: mismatches.length === 0,
    detail: mismatches.length === 0 ? undefined : `${label}: ${mismatches.join(", ")}`,
  };
}

function checkCollaborationContract(label: string, recording: any, minimumSpawnCalls: unknown, requiredTools: unknown) {
  const minimum = typeof minimumSpawnCalls === "number" ? minimumSpawnCalls : 0;
  const required = Array.isArray(requiredTools) ? requiredTools.map((tool) => canonicalCollaborationTool(String(tool))) : [];
  if (minimum <= 0 && required.length === 0) {
    return { name: `${label}: collaboration contract`, ok: true, detail: "scenario has no collaboration contract" };
  }
  const completedCalls = new Set<string>();
  for (const event of recording.events ?? []) {
    if (event.type !== "item.completed") continue;
    const item = event.item ?? {};
    let rawTool = "";
    if (item.type === "collab_tool_call" && item.status === "completed") rawTool = String(item.tool ?? "");
    if (item.type === "tool_call") rawTool = String(item.tool_name ?? "");
    const tool = canonicalCollaborationTool(rawTool);
    if (tool === "") continue;
    const callID = String(item.call_id ?? item.callId ?? item.id ?? completedCalls.size);
    completedCalls.add(`${tool}:${callID}`);
  }
	for (const call of completedCollaborationCallsFromRootRollout(recording)) {
		completedCalls.add(call);
	}
  const tools = new Set([...completedCalls].map((call) => call.slice(0, call.lastIndexOf(":"))));
  const rootThreadID = String(recording?.threadId ?? "").trim();
  const linkedSubagentThreads = new Set(
    (Array.isArray(recording?.rolloutRecords) ? recording.rolloutRecords : [])
      .filter((record: any) => String(record?.parentThreadId ?? "").trim() === rootThreadID)
      .filter((record: any) => String(record?.threadSource ?? "").trim().toLowerCase() === "subagent")
      .map((record: any) => String(record?.threadId ?? "").trim())
      .filter(Boolean),
  );
  if (linkedSubagentThreads.size > 0) tools.add("collaboration.spawn_agent");
  const completedSpawnCalls = [...completedCalls].filter((call) => call.startsWith("collaboration.spawn_agent:")).length;
  const spawnCalls = Math.max(completedSpawnCalls, linkedSubagentThreads.size);
  const missing = required.filter((tool: string) => !tools.has(tool));
  const ok = spawnCalls >= minimum && missing.length === 0;
  return {
    name: `${label}: collaboration contract`,
    ok,
    detail: ok ? undefined : `${label}: completed tools=${JSON.stringify([...tools])}, spawn evidence=${spawnCalls}/${minimum}, missing=${JSON.stringify(missing)}`,
  };
}

function completedCollaborationCallsFromRootRollout(recording: any): Set<string> {
	const calls = new Map<string, string>();
	const outputs = new Set<string>();
	for (const line of String(recording?.rolloutJsonl ?? "").split(/\r?\n/)) {
		if (!line.trim()) continue;
		let raw: any;
		try {
			raw = JSON.parse(line);
		} catch {
			continue;
		}
		const item = raw?.type === "response_item" ? raw.payload : raw?.type === "item" ? raw.item : undefined;
		if (!item || typeof item !== "object") continue;
		const callID = String(item.call_id ?? item.callId ?? "").trim();
		if (callID === "") continue;
		if (item.type === "function_call" && String(item.namespace ?? "").trim() === "collaboration") {
			const tool = canonicalCollaborationTool(String(item.name ?? ""));
			if (tool !== "") calls.set(callID, tool);
			continue;
		}
		if (item.type === "function_call_output") {
			outputs.add(callID);
			continue;
		}
		if (item.type === "tool_output" && item?.data?.success !== false && item?.metadata?.success !== false) {
			outputs.add(callID);
		}
	}
	const completed = new Set<string>();
	for (const [callID, tool] of calls) {
		if (outputs.has(callID)) completed.add(`${tool}:${callID}`);
	}
	return completed;
}

function canonicalCollaborationTool(value: string): string {
  switch (value.trim().toLowerCase()) {
    case "spawn":
    case "spawn_agent":
    case "collaboration.spawn_agent":
      return "collaboration.spawn_agent";
    case "wait":
    case "wait_agent":
    case "collaboration.wait_agent":
      return "collaboration.wait_agent";
    case "send_message":
    case "collaboration.send_message":
      return "collaboration.send_message";
    case "followup_task":
    case "collaboration.followup_task":
      return "collaboration.followup_task";
    case "interrupt_agent":
    case "collaboration.interrupt_agent":
      return "collaboration.interrupt_agent";
    case "list_agents":
    case "collaboration.list_agents":
      return "collaboration.list_agents";
    default:
      return "";
  }
}

function checkSubagentRollouts(label: string, recording: any, minimumSubagents: unknown) {
  const minimum = typeof minimumSubagents === "number" ? minimumSubagents : 0;
  if (minimum <= 0) {
    return { name: `${label}: subagent rollout contract`, ok: true, detail: "scenario has no subagent rollout contract" };
  }
  const rootThreadID = String(recording?.threadId ?? "").trim();
  const linked = Array.isArray(recording?.rolloutRecords)
    ? recording.rolloutRecords.filter((record: any) => {
        const threadID = String(record?.threadId ?? "").trim();
        const parentThreadID = String(record?.parentThreadId ?? "").trim();
        const source = String(record?.threadSource ?? "").trim().toLowerCase();
        return threadID !== "" && threadID !== rootThreadID && parentThreadID === rootThreadID && source === "subagent";
      })
    : [];
  const linkedThreadIDs = new Set(linked.map((record: any) => String(record.threadId).trim()));
  const ok = rootThreadID !== "" && linkedThreadIDs.size >= minimum;
  return {
    name: `${label}: subagent rollout contract`,
    ok,
    detail: ok ? undefined : `${label}: unique linked subagent threads=${linkedThreadIDs.size}/${minimum}, root thread=${JSON.stringify(rootThreadID)}`,
  };
}

function checkSubagentRolloutPatterns(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: subagent rollout patterns`, ok: true, detail: "scenario has no subagent rollout pattern contract" };
  }
  const rootThreadID = String(recording?.threadId ?? "").trim();
  const linked = Array.isArray(recording?.rolloutRecords)
    ? recording.rolloutRecords.filter((record: any) => {
        const threadID = String(record?.threadId ?? "").trim();
        return threadID !== "" && threadID !== rootThreadID &&
          String(record?.parentThreadId ?? "").trim() === rootThreadID &&
          String(record?.threadSource ?? "").trim().toLowerCase() === "subagent";
      })
    : [];
  const rolloutText = linked.map((record: any) => String(record?.jsonl ?? "")).join("\n");
  const missing = expected.map(String).filter((pattern) => !new RegExp(pattern, "s").test(rolloutText));
  return {
    name: `${label}: subagent rollout patterns`,
    ok: missing.length === 0,
    detail: missing.length === 0 ? undefined : `${label}: missing subagent rollout patterns=${JSON.stringify(missing)}`,
  };
}

function checkSubagentFinalMessagePatterns(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: subagent final message patterns`, ok: true, detail: "scenario has no subagent-final contract" };
  }
  const finalMessages = linkedSubagentRolloutRecords(recording).flatMap((record: any) => finalAssistantMessagesFromRollout(record?.jsonl));
  const text = finalMessages.join("\n");
  const missing = expected.map(String).filter((pattern) => !new RegExp(pattern, "s").test(text));
  return {
    name: `${label}: subagent final message patterns`,
    ok: missing.length === 0,
    detail: missing.length === 0 ? undefined : `${label}: finals=${JSON.stringify(finalMessages)}, missing=${JSON.stringify(missing)}`,
  };
}

function linkedSubagentRolloutRecords(recording: any): any[] {
  const rootThreadID = String(recording?.threadId ?? "").trim();
  return Array.isArray(recording?.rolloutRecords)
    ? recording.rolloutRecords.filter((record: any) => {
        const threadID = String(record?.threadId ?? "").trim();
        const parentThreadID = String(record?.parentThreadId ?? "").trim();
        const source = String(record?.threadSource ?? "").trim().toLowerCase();
        return threadID !== "" && threadID !== rootThreadID && parentThreadID === rootThreadID && source === "subagent";
      })
    : [];
}

function finalAssistantMessagesFromRollout(jsonl: unknown): string[] {
  const messages: string[] = [];
  for (const line of String(jsonl ?? "").split(/\r?\n/)) {
    if (!line.trim()) continue;
    let raw: any;
    try {
      raw = JSON.parse(line);
    } catch {
      continue;
    }
    if (raw?.type === "event_msg" && raw?.payload?.type === "agent_message" && raw?.payload?.phase === "final_answer") {
      messages.push(String(raw.payload.message ?? ""));
      continue;
    }
    const item = raw?.type === "response_item" ? raw.payload : raw?.type === "item" ? raw.item : undefined;
    if (!item || item.type !== "message" || item.role !== "assistant" || item.phase !== "final_answer") continue;
    const content = Array.isArray(item.content)
      ? item.content.map((part: any) => String(part?.text ?? "")).join("")
      : String(item.text ?? "");
    messages.push(content);
  }
  return [...new Set(messages)];
}

function checkRootRolloutPatterns(label: string, recording: any, required: unknown, forbidden: unknown) {
  const requiredPatterns = Array.isArray(required) ? required.map(String) : [];
  const forbiddenPatterns = Array.isArray(forbidden) ? forbidden.map(String) : [];
  if (requiredPatterns.length === 0 && forbiddenPatterns.length === 0) {
    return { name: `${label}: root rollout patterns`, ok: true, detail: "scenario has no root-rollout pattern contract" };
  }
  const rolloutText = rootRolloutSearchText(recording);
  const missing = requiredPatterns.filter((pattern) => !new RegExp(pattern, "s").test(rolloutText));
  const present = forbiddenPatterns.filter((pattern) => new RegExp(pattern, "s").test(rolloutText));
  const ok = missing.length === 0 && present.length === 0;
  return {
    name: `${label}: root rollout patterns`,
    ok,
    detail: ok ? undefined : `${label}: missing=${JSON.stringify(missing)}, forbidden present=${JSON.stringify(present)}`,
  };
}

function checkRootRolloutPatternCounts(label: string, recording: any, expected: unknown) {
  if (!expected || typeof expected !== "object" || Array.isArray(expected) || Object.keys(expected).length === 0) {
    return { name: `${label}: root rollout pattern counts`, ok: true, detail: "scenario has no root-rollout count contract" };
  }
  const rolloutText = rootRolloutSearchText(recording);
  const mismatches: string[] = [];
  for (const [pattern, rawCount] of Object.entries(expected as Record<string, unknown>)) {
    const expectedCount = Number(rawCount);
    const actualCount = rolloutText.match(new RegExp(pattern, "gs"))?.length ?? 0;
    if (!Number.isInteger(expectedCount) || expectedCount < 0 || actualCount !== expectedCount) {
      mismatches.push(`${JSON.stringify(pattern)}=${actualCount}/${rawCount}`);
    }
  }
  return {
    name: `${label}: root rollout pattern counts`,
    ok: mismatches.length === 0,
    detail: mismatches.length === 0 ? undefined : `${label}: root rollout pattern counts ${mismatches.join(", ")}`,
  };
}

function checkForbiddenPublicEventPatterns(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: forbidden public event patterns`, ok: true, detail: "scenario has no public-event exclusion contract" };
  }
  const publicEvents = JSON.stringify(recording?.events ?? []);
  const present = expected.map(String).filter((pattern) => new RegExp(pattern, "s").test(publicEvents));
  return {
    name: `${label}: forbidden public event patterns`,
    ok: present.length === 0,
    detail: present.length === 0 ? undefined : `${label}: forbidden public event patterns present=${JSON.stringify(present)}`,
  };
}

function rootRolloutSearchText(recording: any): string {
  const raw = String(recording?.rolloutJsonl ?? "");
  const decoded: string[] = [];
  const visit = (value: any) => {
    if (typeof value === "string") {
      decoded.push(value);
      return;
    }
    if (Array.isArray(value)) {
      for (const item of value) visit(item);
      return;
    }
    if (value && typeof value === "object") {
      for (const item of Object.values(value)) visit(item);
    }
  };
  for (const line of raw.split(/\r?\n/)) {
    if (!line.trim()) continue;
    try {
      visit(JSON.parse(line));
    } catch {
      // The raw text remains searchable even when an individual JSONL line is corrupt.
    }
  }
  return `${raw}\n${decoded.join("\n")}`;
}

function checkFinalAgentMessagePatterns(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: final agent message patterns`, ok: true, detail: "scenario has no final-message pattern contract" };
  }
  const final = finalAgentMessagesByTurn(recording).at(-1) ?? "";
  const missing = expected.map(String).filter((pattern) => !new RegExp(pattern, "s").test(final));
  return {
    name: `${label}: final agent message patterns`,
    ok: missing.length === 0,
    detail: missing.length === 0 ? undefined : `${label}: final=${JSON.stringify(final)}, missing patterns=${JSON.stringify(missing)}`,
  };
}

function checkStartedCompletedPairs(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: started/completed pairs`, ok: true, detail: "scenario has no pair contract" };
  }
  const required = new Set(expected.map(String));
  const observedTypes = new Set<string>();
  const failures: string[] = [];
  for (const turn of turnsForChecks(recording)) {
    const counts = new Map<string, { type: string; started: number; completed: number; firstStarted: number; firstCompleted: number }>();
    for (let index = 0; index < (turn.events ?? []).length; index += 1) {
      const event = turn.events[index];
      const type = String(event.item?.type ?? "");
      if (!required.has(type) || (event.type !== "item.started" && event.type !== "item.completed")) continue;
      const id = String(event.item?.id ?? event.item?.call_id ?? event.item?.callId ?? "");
      const key = `${type}:${id || "<missing-id>"}`;
      const count = counts.get(key) ?? { type, started: 0, completed: 0, firstStarted: -1, firstCompleted: -1 };
      if (event.type === "item.started") {
        count.started += 1;
        if (count.firstStarted < 0) count.firstStarted = index;
      } else {
        count.completed += 1;
        if (count.firstCompleted < 0) count.firstCompleted = index;
      }
      counts.set(key, count);
    }
    for (const [key, count] of counts) {
      observedTypes.add(count.type);
      if (count.started !== 1 || count.completed !== 1 || count.firstStarted >= count.firstCompleted) {
        failures.push(`turn${turn.index}:${key}:started=${count.started},completed=${count.completed},order=${count.firstStarted}<${count.firstCompleted}`);
      }
    }
  }
  for (const type of required) {
    if (!observedTypes.has(type)) failures.push(`${type}:missing`);
  }
  return { name: `${label}: started/completed pairs`, ok: failures.length === 0, detail: failures.length === 0 ? undefined : failures.join("; ") };
}

function checkSingleFinalAgentMessagePerTurn(label: string, recording: any, required: unknown) {
  if (!required) {
    return { name: `${label}: single final agent message`, ok: true, detail: "scenario has no single-final contract" };
  }
  const failures: string[] = [];
  for (const turn of turnsForChecks(recording)) {
    const finals = (turn.events ?? []).filter((event: any) =>
      event.type === "item.completed" && event.item?.type === "agent_message" && event.item?.phase !== "commentary");
    if (finals.length !== 1) failures.push(`turn${turn.index}:finals=${finals.length}`);
  }
  return { name: `${label}: single final agent message`, ok: failures.length === 0, detail: failures.length === 0 ? undefined : failures.join("; ") };
}

function checkNoEmptyCommandExecutions(label: string, recording: any, required: unknown) {
  if (!required) {
    return { name: `${label}: non-empty command executions`, ok: true, detail: "scenario permits empty commands" };
  }
  const failures: string[] = [];
  for (const turn of turnsForChecks(recording)) {
    for (const event of turn.events ?? []) {
      if (event.item?.type !== "command_execution" || (event.type !== "item.started" && event.type !== "item.completed")) continue;
      const command = Array.isArray(event.item?.command) ? event.item.command.join(" ") : String(event.item?.command ?? "");
      if (command.trim().length === 0) failures.push(`turn${turn.index}:${event.type}:${String(event.item?.id ?? "<missing-id>")}`);
    }
  }
  return { name: `${label}: non-empty command executions`, ok: failures.length === 0, detail: failures.length === 0 ? undefined : failures.join("; ") };
}

function turnsForChecks(recording: any): any[] {
  return Array.isArray(recording.turns) && recording.turns.length > 0
    ? recording.turns
    : [{ index: 0, events: recording.events ?? [] }];
}

function commandExecutions(recording: any) {
  return (recording.events ?? [])
    .filter((event: any) => event.type === "item.completed" && event.item?.type === "command_execution")
    .map((event: any) => ({
      status: event.item?.status,
      exitCode: event.item?.exit_code,
      output: String(event.item?.aggregated_output ?? "").replaceAll("\r\n", "\n").trim(),
    }));
}

function checkWorkspaceUnchanged(label: string, recording: any, required: boolean) {
  if (!required) {
    return { name: `${label}: workspace unchanged`, ok: true, detail: "scenario permits workspace mutation" };
  }
  const before = recording.workspace?.before ?? {};
  const after = recording.workspace?.after ?? {};
  const ok = JSON.stringify(before) === JSON.stringify(after);
  return {
    name: `${label}: workspace unchanged`,
    ok,
    detail: ok ? undefined : `${label} changes: ${JSON.stringify(workspaceChanges(before, after))}`,
  };
}

function checkRequiredRolloutItemTypes(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: required rollout item types`, ok: true, detail: "scenario has no rollout-item contract" };
  }
  const actual = rolloutItemTypes(recording);
  const missing = expected.map(String).filter((type) => !actual.includes(type));
  return {
    name: `${label}: required rollout item types`,
    ok: missing.length === 0,
    detail: missing.length === 0 ? undefined : `${label} missing: ${missing.join(", ")}; actual: ${actual.join(", ")}`,
  };
}

function rolloutItemTypes(recording: any): string[] {
  const found = new Set<string>();
  for (const line of String(recording?.rolloutJsonl ?? "").split(/\r?\n/)) {
    if (!line.trim()) continue;
    let entry: any;
    try {
      entry = JSON.parse(line);
    } catch {
      continue;
    }
    const itemType = String(entry?.item?.type ?? "");
    const itemName = String(entry?.item?.name ?? "");
    const eventType = String(entry?.payload?.type ?? "");
    const responseToolName = String(entry?.payload?.name ?? "");
    const responseToolInput = String(entry?.payload?.input ?? "");
    if (["imageView", "image_view", "imageview"].includes(itemType)) found.add("image_view");
    if (entry?.type === "item" && itemName === "view_image") found.add("image_view");
    if (["view_image_tool_call", "image_view"].includes(eventType)) found.add("image_view");
    if (entry?.type === "response_item" && responseToolName === "view_image") found.add("image_view");
    if (
      entry?.type === "item" &&
      itemType === "custom_tool_call" &&
      itemName === "exec" &&
      /\btools\.view_image\s*\(/.test(String(entry?.item?.input ?? ""))
    ) found.add("image_view");
    if (
      entry?.type === "response_item" &&
      eventType === "custom_tool_call" &&
      responseToolName === "exec" &&
      /\btools\.view_image\s*\(/.test(responseToolInput)
    ) found.add("image_view");
  }
  return [...found].sort();
}

function checkRequireRolloutCompaction(label: string, recording: any, required: unknown) {
  if (!required) {
    return { name: `${label}: rollout compaction marker`, ok: true, detail: "scenario has no rollout-compaction contract" };
  }
  const found = String(recording?.rolloutJsonl ?? "")
    .split(/\r?\n/)
    .some((line) => {
      if (!line.trim()) return false;
      try {
        return JSON.parse(line)?.type === "compacted";
      } catch {
        return false;
      }
    });
  return {
    name: `${label}: rollout compaction marker`,
    ok: found,
    detail: found ? undefined : `${label} rollout has no compacted record`,
  };
}

function checkCompactionWarning(label: string, recording: any, required: unknown) {
  if (!required) {
    return { name: `${label}: compaction warning item`, ok: true, detail: "scenario has no compaction-warning contract" };
  }
  const count = (recording.events ?? []).filter((event: any) =>
    event?.type === "item.completed" &&
    event?.item?.type === "error" &&
    String(event?.item?.message ?? "").includes("Heads up: Long threads"),
  ).length;
  return {
    name: `${label}: compaction warning item`,
    ok: count >= 1,
    detail: count >= 1 ? undefined : `${label} emitted ${count} compaction warning error items`,
  };
}

function withoutCompactionWarning(recording: any): any[] {
  return (recording.events ?? []).filter((event: any) =>
    !(event?.type === "item.completed" &&
      event?.item?.type === "error" &&
      String(event?.item?.message ?? "").includes("Heads up: Long threads")),
  );
}

function checkWorkspaceSideEffects(rust: any, go: any, selectedPaths?: unknown) {
  const left = workspaceChanges(rust.workspace?.before ?? {}, rust.workspace?.after ?? {});
  const right = workspaceChanges(go.workspace?.before ?? {}, go.workspace?.after ?? {});
  const selected = Array.isArray(selectedPaths) ? new Set(selectedPaths.map(String)) : null;
  const comparable = (items: any[]) => selected ? items.filter((item) => selected.has(item.path)) : items;
  const ok = JSON.stringify(comparable(left)) === JSON.stringify(comparable(right));
  return {
    name: "workspace side effects match",
    ok,
    detail: ok ? undefined : `rust: ${JSON.stringify(left)}; go: ${JSON.stringify(right)}`,
  };
}

function checkExpectedWorkspaceChanges(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected)) {
    return { name: `${label}: expected workspace changes`, ok: true, detail: "artifact has no workspace-change contract" };
  }
  const actual = workspaceChanges(recording.workspace?.before ?? {}, recording.workspace?.after ?? {});
  const ok = actual.length === expected.length && expected.every((contract: any, index: number) => {
    const item = actual[index];
    if (!item || item.path !== contract.path || item.change !== contract.change) {
      return false;
    }
    if (typeof contract.hash === "string") {
      return item.hash === contract.hash;
    }
    return true;
  });
  return {
    name: `${label}: expected workspace changes`,
    ok,
    detail: ok ? undefined : `${label}: ${JSON.stringify(actual)}; expected: ${JSON.stringify(expected)}`,
  };
}

function checkWorkspaceRequiredPaths(label: string, recording: any, expected: unknown) {
  if (!Array.isArray(expected) || expected.length === 0) {
    return { name: `${label}: required workspace paths`, ok: true, detail: "artifact has no required-workspace-path contract" };
  }
  const after = recording.workspace?.after ?? {};
  const missing = expected.filter((entry) => !(String(entry) in after));
  return {
    name: `${label}: required workspace paths`,
    ok: missing.length === 0,
    detail: missing.length === 0 ? undefined : `${label} missing: ${missing.join(", ")}`,
  };
}

function workspaceChanges(before: Record<string, string>, after: Record<string, string>) {
  const names = [...new Set([...Object.keys(before), ...Object.keys(after)])].sort();
  return names.flatMap((name) => {
    if (!(name in before)) {
      return [{ path: name, change: "added", hash: after[name] }];
    }
    if (!(name in after)) {
      return [{ path: name, change: "removed", hash: before[name] }];
    }
    if (before[name] !== after[name]) {
      return [{ path: name, change: "modified", before: before[name], after: after[name] }];
    }
    return [];
  });
}

function itemTypes(recording: any): string[] {
  return (recording.events ?? [])
    .filter((event: any) => event.type === "item.completed")
    .map((event: any) => String(event.item?.type));
}

function eventTypes(recording: any): string[] {
  return Array.isArray(recording.events) ? recording.events.map((event: any) => String(event.type)) : [];
}

function agentMessages(recording: any): string[] {
  return (recording.events ?? [])
    .filter((event: any) => event.type === "item.completed" && event.item?.type === "agent_message")
    .map((event: any) => String(event.item?.text ?? ""));
}

function errorItems(recording: any): any[] {
  return (recording.events ?? []).filter((event: any) => event.type === "item.completed" && event.item?.type === "error");
}

function renderReport(result: CompareResult, artifactDir: string, manifest: any): string {
  const checks = result.checks.map((check) => `- ${check.ok ? "PASS" : "FAIL"} ${check.name}${check.detail ? `: ${check.detail}` : ""}`).join("\n");
  const baselineDrift: string[] = [];
  if (manifest?.baseline?.rustBaselineDrift) {
    const actual = manifest?.binaries?.rust ?? {};
    const expected = manifest?.baseline?.expectedRustBinary ?? {};
    baselineDrift.push(
      `Rust binary is ${actual.version ?? "<unknown>"} (${actual.sha256 ?? "<unknown hash>"}); ` +
      `parity.json expects ${expected.version ?? "<unknown>"} (${expected.sha256 ?? "<unknown hash>"}).`,
    );
  }
  if (manifest?.baseline?.parityRecordDrift) {
    baselineDrift.push(
      `local SDK/Rust checkout ${manifest.baseline.rustUpstreamCommit ?? "<unknown>"} differs from parity.json ` +
      `${manifest.baseline.parityRustUpstreamHead ?? manifest.baseline.parityRustBaseline ?? "<unknown>"}.`,
    );
  }
  const baseline = baselineDrift.length > 0
    ? `Baseline drift: ${baselineDrift.join(" ")}`
    : "Baseline drift: none recorded.";
  return `# SDK Parity Report

Status: ${result.status}
Classification: ${result.classification}
Confidence: ${result.confidence}

${baseline}

Artifact: ${artifactDir}

## Event Types

- rust: ${result.eventTypes.rust.join(" -> ")}
- go: ${result.eventTypes.go.join(" -> ")}

## Checks

${checks}

## Reproduce

\`\`\`powershell
npm --prefix sdktests run report -- --artifact "${artifactDir}"
\`\`\`
`;
}
