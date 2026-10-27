# 2026-10-27 non-TUI sync window

Upstream window: `8196bcdc18..c587d6e54c` (Rust `codex` at `c587d6e54c`).
This lane owns non-TUI core packages only. `tui/`, `app/agents_dashboard.go`,
`app/tui_telemetry.go` and `update/plan_2026_10_27.md` belong to the parallel
TUI lane and are not modified here.

## Implemented

| Upstream | Requirement | Go change |
| --- | --- | --- |
| `#57242` | Default thread unload delay is 1 minute | `config/config.go`, unload-delay test |
| `#56525` | Drop the 1 MiB explicit model-catalog response cap | `model/models_endpoint.go`, catalog URL test |
| `#57293` | Add GPT-6.1 Terra to bundled/Bedrock catalogs | `model/provider_info.go`, `model/catalog.go`, `model/catalog_test.go` |
| `#57325` | Granular approval catalog templates | `model/catalog.go`, `sandbox/permission_prompt.go`, `appserver/permissions_world_state.go`, catalog message tests |
| `#57273` | `codex queue` rejects unsupported options | `cli/cli.go` (`parseQueue`, `validateQueueSharedOptions`), queue parse tests |
| `#57232` | `codex queue --resume` resumes an unloaded thread | `cli/cli.go`, `app/session.go`, `app/app.go`, app queue tests |
| `#57204` | Feature `untrusted_as_tool_output` (under development, off) | `features/features.go` |
| `#57249` | Feature `plugin_discovery_prefetch` (stable, on) gates prewarm | `features/features.go`, `appserver/turn_runtime.go` |
| `#56020` `#56031` `#56364` | Thread sample protocol fields and latest-turn semantics | `appserver/thread_sample.go`, `thread_sample_protocol.go`, `thread_sample_like_rust_test.go` |
| `#57235` | `exec_command` reports `shell_snapshot_mode` telemetry | `tool/shell_executor.go`, `turn/tools.go`, `appserver/runtime_router.go`, `exec/exec.go` |
| `#56133` | Defer agent mail after empty explicit final answers | `eventmap/stream.go`, stream tests, `turn/mailbox_preemption_test.go` |
| `#56024` | Bound decoded HTTP/3 response headers (64 KiB) | `tcptunnel/tunnel.go`, tunnel test |
| `#57227` | Restore acknowledged permissions on resume/fork | `rollout/rollout.go`, `rollout/session_items.go`, `appserver/persisted_resume_settings.go`, `appserver/router.go`, `appserver/runtime_router.go`, like-Rust tests |
| `#57317` | Feature `simplified_selection_hints` (off): concise plugin hints, 4 KiB bound | `features/features.go`, `plugin/plugin.go`, `appserver/turn_runtime.go`, plugin/RuntimeRouter like-Rust tests |
| `#57279` | Untrusted additional context as agent-namespaced tool outputs | `turn/api.go`, `appserver/additional_context.go`, `appserver/turn_runtime.go`, `appserver/runtime_router.go`, like-Rust tests |
| `#57172` | Lifecycle timestamps on app-server thread items | `appserver/protocol.go`, `appserver/item_lifecycle_times.go`, `appserver/router.go`, `appserver/runtime_router.go`, `appserver/turn_runtime.go`, `state/thread_history_query.go`, like-Rust tests |
| `#56465` | `thread/memory/read` returns the selected version's memory summary | `appserver/protocol.go`, `appserver/thread_memory_read.go`, `appserver/schema.go`, `appserver/rust_protocol_surface_test.go`, like-Rust tests |
| `#55940` | `feedback/save` writes a local diagnostic ZIP instead of uploading | `appserver/protocol.go`, `appserver/feedback.go`, `appserver/feedback_archive.go`, `appserver/feedback_thread_index.go`, `appserver/runtime_router.go`, `appserver/schema.go`, like-Rust tests |

Grounding notes:

- `#56234` (prompt telemetry ordering) is structurally N/A: Go never wires
  `RunUserPromptSubmit` into turns, so there is no pre-hook telemetry path to
  order against. Recorded in `update/plan_2026_09_11.md`.
- `#57293` GCP/Vertex picker portion is N/A: the Go build has no GCP provider.
- `#57249` directory/featured prefetch sites do not exist in Go; only the
  suggested-plugin prewarm path is gated.

`#57227` details: settings snapshots now persist `approvals_reviewer` and both
named/unnamed permission profiles. `rollout.LatestPersistedResumeSettings`
mirrors Rust's `PersistedResumeSettings::from_history` (thread-owned snapshot
precedence over stale TurnContext; a legacy snapshot following the owner's
checkpoint applies; a foreign-owned snapshot clears accumulated legacy; only an
owned snapshot supplies unnamed raw permissions). Resume and cold fork restore
approval policy, reviewer, named profile, and the unnamed base profile while
explicit request values win. Go keeps its #53419 portability filter for raw
permission grants, so executor-materialized path grants are not restored even
from an owned snapshot.

`#57279` details: with `features.untrusted_as_tool_output` enabled, untrusted
additional context becomes a `function_call_output` item named after the
explicit entry `name` or derived from the source key (spaces to underscores,
ASCII alphanumerics/underscore, 128 chars, `untrusted_content` fallback) and
placed in the `agent` namespace before user input. Values are middle-truncated
to 1,000 tokens before comparison or delivery. `RuntimeRouter` now owns the
per-thread deduplication store that mirrors Rust's `AdditionalContextStore`:
unchanged values are not re-emitted, a name change re-emits, a change confined
to truncated content does not, and an empty context map clears the store.
Application context still rides in developer instructions, and steering now
places every context item before the steered user message.

`#57172` details: every wire `ThreadItem` now carries nullable
`startedAtMs`/`completedAtMs` (always present, `null` when unrecorded), matching
Rust's flat payload. Values come from session metadata for live/runtime items,
from the persisted `thread_items.started_at_ms`/`completed_at_ms` columns for
paginated history and resume (with the recorded-zero legacy rule), and are
injected onto `item/started`/`item/completed` payloads before dispatch. The
router records the start time published on `item/started` per turn and reuses
it on `item/completed`, so the pair reports the same `startedAtMs` even when the
completion payload is rebuilt from persisted metadata; a completion with no
recorded start falls back to its completion time, as Rust does. A code-mode
command completion now carries the start time it began with.

`#56465` details: `thread/memory/read` requires a loaded thread (Rust loads it
through the thread manager before reading), loads the thread record, resolves
the effective config with the record's thread overrides, and returns the raw
`<codexHome>/<version.DirectoryName()>/memory_summary.md` text only when the
`memories` feature and `memories.use_memories` are both enabled. A missing,
unreadable, or blank summary yields `summary: null`; Go does not fall back to
another codex version. The method is experimental in the schema surface.

`#55940` details: `feedback/save` validates the same upload payload plus an
absolute `savePath` and writes a diagnostic ZIP without contacting the upload
service, so it works even with `feedback.enabled=false`. The archive mirrors
Rust's layout (`report.json`, `manifest.json`,
`attachments/%04d/<basename>`) and limits: 200 MiB uncompressed including the
report and manifest, at most 128 attachments (rollout last), strict
missing/non-regular/oversized attachment failures, and a same-directory temp
file published with a no-clobber link so an existing path is never replaced.
Only an already materialized rollout is attached, so saving an untouched
thread does not materialize one; the thread index reports
`rolloutArchiveFilename: null` in save mode.

## Pending in this window

- Remaining ~240 non-TUI commits in `update/_cls_core.tsv` are still being
  triaged: high-value candidates include `#57311` rollouts trace versions,
  `#57280` full MCP results,
  `#57287` prediction-fork tool declarations, `#57286` managed
  `network_proxy`, `#57263`/`#57276`
  permission wording/world-state, `#57171` runtime auth and
  `#57270` cloud config for sandbox/exec-server, and
  `#56036`/`#56049` remote credentials. The `feedback/save` and
  `thread/memory/read` schema drift is fixed by the entries above.

## Verification

- `gofmt` on all touched packages.
- Isolated worktree `D:\qax\reagent\dev\codex_go_wt\nontui_verify` (clean
  HEAD + non-TUI patch): `go build ./...` passes.
- `go test ./tool ./turn ./eventmap ./tcptunnel -count=1` passes.
- `go test ./appserver -run 'ThreadSample|ShellSnapshot|GranularApproval|PermissionPrompt' -count=1` passes.
- `go test ./app ./cli ./model ./config ./sandbox -count=1` passes.
