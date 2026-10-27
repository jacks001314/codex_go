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

Grounding notes:

- `#56234` (prompt telemetry ordering) is structurally N/A: Go never wires
  `RunUserPromptSubmit` into turns, so there is no pre-hook telemetry path to
  order against. Recorded in `update/plan_2026_09_11.md`.
- `#57293` GCP/Vertex picker portion is N/A: the Go build has no GCP provider.
- `#57249` directory/featured prefetch sites do not exist in Go; only the
  suggested-plugin prewarm path is gated.

## Pending in this window

- `#57227` persisted resume settings ownership: needs thread-owned snapshot
  precedence, permission profile/reviewer restore, and explicit-override
  respect for both resume and fork. Helpers exist in
  `appserver/persisted_permission_profile.go`; rollout snapshots currently only
  carry CWD/workspace roots and the writer persists only those.
- Remaining ~240 non-TUI commits in `update/_cls_core.tsv` are still being
  triaged: high-value candidates include `#57311` rollouts trace versions,
  `#57279` untrusted context as tool outputs, `#57280` full MCP results,
  `#57287` prediction-fork tool declarations, `#57286` managed
  `network_proxy`, `#57317` concise plugin hints, `#57263`/`#57276`
  permission wording/world-state, `#57171`/`#57172` runtime auth and
  lifecycle timestamps, `#57270` cloud config for sandbox/exec-server, and
  `#56036`/`#56049` remote credentials.

## Verification

- `gofmt` on all touched packages.
- Isolated worktree `D:\qax\reagent\dev\codex_go_wt\nontui_verify` (clean
  HEAD + non-TUI patch): `go build ./...` passes.
- `go test ./tool ./turn ./eventmap ./tcptunnel -count=1` passes.
- `go test ./appserver -run 'ThreadSample|ShellSnapshot|GranularApproval|PermissionPrompt' -count=1` passes.
- `go test ./app ./cli ./model ./config ./sandbox -count=1` passes.