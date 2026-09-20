# TUI differential alignment (tuialign lane)

Running record for the skill in `.agents/skills/tui/SKILL.md`: drive the Rust
and Go Codex CLIs through the same real terminal scenarios, capture screen,
protocol and workspace evidence, then close the Go-side differences.

## Harness

The harness lives outside the tracked tree in `.tmp-tui-align/` (untracked):

| File | Role |
|---|---|
| `run_diff.py` | one scenario: isolated `CODEX_HOME` + workspace, scripted mock Responses API, ConPTY driver, screen/protocol/workspace/rollout diff |
| `run_all.py` | the default suite (`startup`, `slash-help`, `slash-status`, `prompt-basic`, `tool-call`, `interrupt`, `approval`, `provider-failure`) |
| `analyze.py` | per-run drill-down: request kinds, header deltas, tool-shape deltas |
| `mock_model.py` | deterministic Responses API mock (scripted answers, JSONL request log) |
| `godriver/tuidriver.exe` | Go ConPTY driver that steers the TUI by what the emulated screen shows |

```powershell
cd D:\qax\reagent\dev\codex_go\.tmp-tui-align
python run_diff.py --scenario scenarios/prompt-basic.json --tag <tag> --impls rust,go
python run_all.py --tag <tag>
python analyze.py --tag <tag> --scenarios prompt-basic,tool-call
```

Rust binary: `..\git\codex\codex-rs\target\release\codex.exe` (build once).
Go binary: `bin\codex.exe` (`powershell -File scripts\build.ps1` after every
change).

## How to read the results

- **Protocol requests**, **workspace side effects** and **rollout event
  sequences** are the alignment targets; they are normalised before diffing.
- **Screens never match**: Go's TUI is its own implementation (different
  startup box, status row, composer) and the alternate-screen owned-transcript
  lane is not ported yet (`update/plan_2026_09_20.md`). Screens are used to
  steer scenarios and to catch behavioural regressions, not as a parity gate.
- IDs, run paths, versions and timestamps differ between the two runs by
  construction; `analyze.py` prints them so real header/field deltas stay
  visible.

## Landed in this session (2026-09-20, tags `H1`-`H4`)

Baseline before the session: Go's TUI lane sent `originator: codex_cli_rs`,
flattened `multi_agent_v1__*` function tools and always advertised
`apply_patch`, `update_plan` and `get_context_remaining`.

1. **TUI identity/originator** - Rust's embedded app server registers the TUI
   connection as `codex-tui` and `initialize_processor.rs` turns that name into
   the process originator (`client_name: "codex-tui"`, `tui/src/lib.rs`), so
   every TUI model request carries `originator: codex-tui`. Go's TUI drives its
   turns through the exec runner, which resolved the originator to the CLI
   default, so the TUI now stamps the identity per request:
   `exec.Request.Originator` + `execAgentOriginator` precedence
   (`CODEX_INTERNAL_ORIGINATOR_OVERRIDE` > request identity > `codex_cli_rs`),
   set in `runInteractiveTurn` (`app/interactive.go`). Verified: `H1` go
   `originator=codex_cli_rs` vs rust `codex-tui` -> `H2`/`H3` both
   `originator=codex-tui`. Tests: `TestExecAgentOriginatorPrefersRequestIdentity`,
   `TestRunAttributesEmbeddedTUIOriginatorLikeRust`.
2. **`multi_agent_v1` namespace** - Rust publishes the V1 family as one
   `ToolSpec::Namespace` (`core/src/tools/handlers/multi_agents_spec.rs`,
   description "Tools for spawning and managing sub-agents."), while Go sent
   five flattened `multi_agent_v1__*` functions. `isResponsesNamespaceTool` now
   groups the namespace and the V1 member specs carry the namespace
   description (`model/responses_tools.go`, `agent/tools.go`). Verified: both
   implementations now send `namespace multi_agent_v1 ->
   [close_agent, resume_agent, send_input, spawn_agent, wait_agent]`.
   Tests: `TestResponsesMultiAgentV1ToolsUseNamespaceLikeRust`,
   `TestMultiAgentV1ToolContractMatchesRust`.
3. **Utility tool gating** - Rust registers `update_plan` only when
   `[tools.update_plan].enabled` is set (`config/mod.rs
   resolve_update_plan_enabled`, default **false**) and `get_context_remaining`
   only with the `token_budget` feature (`core/src/tools/spec_plan.rs`). Only
   the app-server router applied those gates; the exec runner the TUI uses did
   not, so the TUI advertised both. `toolRouterForRequest` now applies them from
   the effective config, and `turn.ToolRegistryOptions` /
   `tool.CoreHandlerOptions` gained `DisableGetContextRemaining` (mirrored in
   `runtime_router.go`). Verified: `H1` go tool list had 16 entries ->
   `H3` 10, matching rust modulo `apply_patch`.
   Tests: `TestToolRouterGatesUtilityToolsFromConfigLikeRust`,
   `TestBuildToolRegistryHonorsToolDisableOptions`.
4. **`apply_patch` gating (TUI/exec lane only)** - Rust registers `apply_patch`
   only when the resolved model metadata declares an apply-patch tool type
   (`spec_plan.rs`: `model_info.apply_patch_tool_type.is_some()`); models with
   no metadata (`mock-model`, `gpt-5`) fall back to shell patches. The exec
   lane now applies the same gate. Verified: `H3` go tool list no longer
   contains `custom apply_patch`; rust/go tool names are identical.

### Verification

- `gofmt` clean on every touched file.
- `go test ./exec/ ./turn/ ./model/ ./agent/ ./tool/ ./parity/ ./app/ ./appserver/ -count=1`
  green (one `tool` flake: `TestUnifiedExecRecoversDisconnectedExecServerSessionLikeRust`
  fails only when packages run concurrently and passes alone; unrelated to this
  change).
- Differential after each landing (tags `H1`, `H2`, `H3`) and the full default
  suite at `H4`: `startup`, `slash-help`, `slash-status` now report
  `requests match`; `prompt-basic` is down to the two open items below.

## Open items

1. **Automatic thread title request (model-visible)** - Rust issues a second
   model request per fresh conversation: a temporary structured thread
   (`ThreadSource::Feature("thread_title")`, `thread_source: "thread_title"`,
   `sandbox: none` / `sandbox_mode: read-only`), no tools, output schema
   `{title: string, 1..36}`, instructions "Generate a concise, single-line task
   title ..." plus `User prompt:` (<=960 bytes), model `gpt-5.6-luna` when the
   account/provider can list it (low effort), otherwise the current model
   (`tui/src/app/thread_title.rs`, `temporary_structured_request.rs`). Go only
   derives a provisional local title (`interactiveAutoThreadTitle`, used at
   `app/interactive.go`, `app/remote_tui.go`). Evidence: `analyze.py` shows
   `aux/output-schema(title)` requests in rust and none in go for
   `prompt-basic`, `tool-call`, `interrupt`. Requires a Go temporary/structured
   thread + title persistence path plus cancellation on manual rename.
2. **`apply_patch` gating in the app-server lane** - the gate above was landed
   for the exec/TUI lane only. Applying it in `runtime_router.go` makes three
   existing app-server tests fail (`TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust`,
   `TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust`,
   `TestRuntimeRouterFileChangeAnalyticsIncludesUserReviewSummaryLikeRust`):
   they run the `applyPatchRuntimeAgent` fake against `model = "gpt-5"`, which
   has no apply-patch metadata in the frozen catalog (only `gpt-6-astra`,
   `gpt-5.6-*`, `gpt-5.5`, `gpt-5.4`, `codex-auto-review`,
   `gpt-daybreak-*-latest` are listed, all `freeform`). Rust's own app-server
   suite injects the field into its model fixture
   (`app-server/tests/suite/v2/turn_start.rs`: `model["apply_patch_tool_type"]
   = "freeform"`), so the Go tests need the same declared metadata (e.g. a
   `model_catalog_json` fixture) before the gate can be added there.
3. **`user-agent` header in the TUI lane** - rust sends
   `codex-tui/0.0.0 (Windows 10.0.26200; x86_64) WindowsTerminal (codex-tui; 0.0.0)`;
   go sends the Go default `Go-http-client/1.1`. The shape is
   `<originator>/<version> (<os>; <arch>) <terminal-name> (<client name>; <client version>)`;
   Go's app-server lane builds an approximation
   (`appserver.InitializeUserAgent`, `... go (name; version)`) but the exec
   lane the TUI uses never sets it.
4. **`x-codex-turn-metadata` fields** - rust carries `window_number`,
   `context_window_id`, `root_turn_id`, `agent_name`, `sandbox`,
   `sandbox_mode`, `workspaces` (with git remote/commit/has_changes),
   `turn_started_at_unix_ms` and `analytics_enabled`; go sends
   `codex_version` plus fewer fields and a `turn-<uuid>` turn id where rust
   uses the bare turn uuid. Needs a field-by-field decision per key.
5. **Rollout transcript lane** - go writes `event_msg:user_message` /
   `event_msg:agent_message` and omits `world_state`, `turn_context`,
   `item_completed`, `token_usage_record` and `token_count`. This is the
   alternate-screen owned-transcript lane recorded in
   `update/plan_2026_09_20.md` (Go's `tui/thread_transcript.go` is still a
   stub); it has to land before the rollout sequences can match.
6. **`approval` and `provider-failure` scenario triage** - rust sends 0 model
   requests while go sends 1 (`approval`) and 2 (`provider-failure`). Either the
   scenarios steer the two TUIs differently, or Go proceeds where Rust blocks
   (approval modal / failed provider). Needs a per-scenario step-by-step run
   before any code change.
