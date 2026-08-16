# SDK event normalization rules

The parity runner records the raw TypeScript SDK event stream from each
implementation and compares the **observable contract**, not exact bytes.
Volatile or identity-bearing fields are normalized before comparison; every
rule below documents its reason, an example, and the field paths it applies
to. The implementation lives in `src/normalize.ts` (mapped placeholders keep
reference relationships between values, so two different IDs never collapse
into the same placeholder).

| # | Rule | Reason | Example | Field paths |
|---|---|---|---|---|
| 1 | UUID-like strings become stable mapped placeholders `<ID_n>` | Session/thread/item UUIDs differ between runs; the *relationship* between IDs is the contract, so each distinct UUID maps to one placeholder across the recording | `"8f7c4ac2-6141-42da-b4d5-7032a8e8df3b"` → `"<ID_1>"` (same value later → same placeholder) | any string matching `^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$` |
| 2 | Keys ending `_id` or equal to `id` become mapped placeholders `<ID_n>` | Durable ids (threadId, turnId, item ids, request ids) are run-specific identities; the references between them are the contract | `"threadId": "th_abc"` → `"threadId": "<ID_2>"` | any object key ending `_id`, or the key `id` |
| 3 | RFC-3339 timestamps become `<TIMESTAMP>` | Wall-clock timestamps are not contract and differ across runs/machines | `"2025-08-10T03:12:26.500Z"` → `"<TIMESTAMP>"` | any string matching `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}` |
| 4 | Absolute/session-temp paths become mapped placeholders `<PATH_n>` | Isolated `CODEX_HOME` and workspace copies use run-specific temp paths; the path topology is the contract | `"D:\\sdktests\\.tmp\\case-7\\a.txt"` → `"<PATH_1>"` | Windows drive paths (`^[A-Za-z]:[\\/]`) and paths containing `\sdktests\.tmp\` |
| 5 | Volatile numeric fields become `<KEY_UPPER>` | Durations, elapsed times and process exit codes vary run to run at the raw event layer; exit codes are asserted explicitly at the command-execution contract level instead (see below) | `"durationMs": 1234` → `"durationMs": "<DURATIONMS>"` | object keys `durationMs`, `duration_ms`, `elapsed_ms`, `exitCode` |
| 6 | `usage` objects become `<TOKEN_COUNT>` per key | Token usage counts vary with model response; presence of usage and its key set is the contract, not the counts | `"usage": {"input_tokens": 123}` → `"usage": {"input_tokens": "<TOKEN_COUNT>"}` | the `usage` object (all descendant keys) |

## Contract fields asserted outside raw normalization

Some observable fields are compared explicitly at the scenario-assertion level
even though the raw event layer normalizes them:

- **Command exit codes and statuses**: `expected.commandExecutions[]` asserts
  `status` and `exitCode` for every executed command
  (`src/compare.ts` `commandExecutions` comparison), so a Go/Rust exit-code
  difference fails the scenario despite rule 5 masking the raw field.
- **Completed item type sequences**: `expected.requiredCompletedItemTypes` /
  `forbiddenCompletedItemTypes` / `exactCompletedItemTypeCounts` assert the
  item surface.
- **Workspace side effects**: `expected.fileChanges` / `workspaceChanges` /
  `workspaceMutation` assert file-level outcomes.
- **Agent message text**: `expected.exactAgentMessages` /
  `structuredAgentMessages` / `agentMessageContracts` assert message content
  only where the scenario declares it; otherwise agent prose is compared via
  `agentMessageComparison` (structural, not byte-exact).

## Comparison modes

`expected.eventSequenceComparison` selects how strictly event sequences are
compared (`strict`, `semantic-tools`, `model-selected-tools`,
`compaction-tolerant`); `expected.commandOutputComparison` selects the command
output comparison mode. Scenario authors choose the mode that matches the
contract being pinned, mirroring the djalign principle that the comparison
target is the observable contract, not byte-for-byte output.
