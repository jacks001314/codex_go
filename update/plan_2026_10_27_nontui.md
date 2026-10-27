# 2026-10-27 non-TUI sync window

Upstream window: `8196bcdc18..c587d6e54c`, extended by
`c587d6e54c..6fe77fc123` (Rust `codex` at `6fe77fc123`; the non-TUI commits in
that extension are `#57394`, `#57360`, `#57358`, `#57357`, `#57353`, `#57352`).
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
| `#57313` | Gateway OAuth tokens default to `Authorization: Bearer` | `model/provider_info.go`, `model/gateway_auth.go`, `model/provider_config.go`, gateway tests |
| `#56309` `#57263` | Permission catalog path wording (`writable_root(s)`, `omitted_paths`, `denied_reads`) | `model/catalog.go`, `sandbox/permission_prompt.go`, `appserver/permissions_world_state.go`, catalog/permission tests |
| `#57286` | Managed `[network_proxy]` requirements with `[experimental_network]` alias | `config/requirements_file.go`, `config/cloud_config.go`, `config/ignored_settings.go`, `appserver/managed_network_constraints.go`, config tests |
| `#57304` | Surface permanent refresh-token rejection during workspace routing | `appserver/workspace_routing.go`, `appserver/turn_runtime.go`, like-Rust test |
| `#57287` | Preserve parent tool declarations in MCP-disabled prediction forks | `features/features.go`, `appserver/protocol.go`, `appserver/thread_sample_protocol.go`, `appserver/prediction_parent_tools.go`, `appserver/runtime_router.go`, `appserver/thread_sample.go`, `appserver/turn_runtime.go`, `appserver/incremental_tools.go`, `turn/runtime.go`, like-Rust tests |
| `#57270` | Load cloud config for sandbox and remote exec-server commands | `app/app.go`, sandbox/exec-server cloud-config tests |
| `#57192` | Gate capability discovery V2 on executor version | `execserver/capability_discovery_v2_compat.go`, `execserver/client.go`, `appserver/executor_capability_discovery_v2.go`, `appserver/initialize.go`, `cmd/codex/main.go`, version tests |
| `#56049` | Configured credential providers in remote credential masking | `network/proxy_credential_providers.go`, `execserver/network_policy.go`, `execserver/server.go`, `tool/unified_exec.go`, capability/masking tests |
| `#57187` | Advisory rollout budgets with optional limit enforcement | `config/rollout_budget.go`, `config/config.go`, `runtimeutil/budget.go`, `appserver/runtime_router.go`, config/budget tests |
| `#56445` | Treat `model_not_found` provider errors as terminal failures | `codexapi/error.go`, `codexapi/response_debug.go`, `model/responses_stream.go`, `model/responses_agent.go`, `rollout/codex_error_info.go`, `appserver/turn_runtime.go`, like-Rust tests |
| `#56468` | Preserve `SystemRoot` for Windows filesystem sandbox helpers | `execserver/fs_helper.go`, helper environment test |
| `#56022` | Preserve config overrides across CLI subcommand boundaries | `cli/cli.go`, `cli/cli_test.go` |
| `#56552` `#57225` `#57226` `#55975` | DELETE journaling on NFS: initialize new databases, migrate existing WAL databases under managed enforcement, ship `codex migrate-nfs-db` | `state/sqlite.go`, `state/nfs_migration.go`, `state/nfs_lock.go`, `config/api.go`, `config/config.go`, `config/requirements_file.go`, `appserver/runtime_router.go`, `appserver/websocket.go`, `appserver/message_board.go`, `cli/cli.go`, `app/migrate_nfs_db.go`, `app/app.go`, like-Rust tests |
| `#56495` | Paginated subscribed-channel listing on the local message board | `agentboard/local.go`, `agentboard/local_test.go` |
| `#55690` | Reclaim aged legacy marketplace staging directories | `plugin/marketplace_staging.go`, `plugin/marketplace_install.go`, `plugin/marketplace_staging_test.go` |
| `#57251` | Native dollar thread usage in app-server `ThreadUsage` (core surface + coverage) | `appserver/runtime_router_test.go` (type/forwarding landed with `auth/account.go`, `appserver/runtime_router.go`) |
| `#56553` | Thread-scoped `hooks/list` and `environmentId` in hook metadata | `appserver/hooks.go`, `appserver/runtime_router.go`, `appserver/hooks_runner.go`, like-Rust tests |
| `#57186` | Fail closed on unreadable deny-glob search root metadata errors | `sandbox/linuxsandbox/bwrap.go`, `sandbox/linuxsandbox/unreadable_glob_root_test.go` |
| `#57209` | Turn analytics critical-path fields (disabled/null default; capture pending) | `telemetry/turn_event.go`, `telemetry/turn_event_test.go` |
| `#57201` | Capture direct-tool critical paths behind `critical_path_timing` | `criticalpath/critical_path.go`, `tooltiming/tool_timing.go`, `features/features.go`, `turn/turn.go`, `turn/tool_dispatcher.go`, `turn/agent_loop.go`, `turn/runtime.go`, `appserver/turn_runtime.go`, `appserver/thread_manager.go`, `telemetry/turn_event.go`, like-Rust tests |
| `#57207` | Attribute approval waits and Guardian reviews to the owning tool call | `tool/handlers.go`, `turn/tools.go`, `appserver/runtime_router.go`, `appserver/guardian_reviewer.go`, `appserver/network_approval.go`, `appserver/mcp_elicitation.go`, `appserver/mcp_tool_approval.go`, `appserver/agent_runtime.go`, like-Rust tests |
| `#57198` | `PreToolUse` hook input reports the local command's resolved working directory | `tool/hooks.go`, `tool/shell_executor.go`, `appserver/hooks_events.go`, `appserver/tool_hooks_adapter.go`, like-Rust tests |
| `#57203` | Preserve the command cwd in `PermissionRequest` and `PostToolUse` hook input | `tool/hooks.go`, `tool/shell_executor.go`, `appserver/hooks_events.go`, `appserver/tool_hooks_adapter.go`, `appserver/runtime_router.go`, `appserver/network_approval.go`, like-Rust tests |
| `#56494` | Channel upserts on message boards (`upsert_channel`; `description` on `create_channel`) | `agentboard/api.go`, `agentboard/inmemory.go`, `agentboard/local.go`, `agentboard/remote.go`, `agentboard/tools.go`, `agentboard/upsert_channel_test.go`, `agentboard/tools_test.go` |
| `#57212` | zstd-compressed code-mode gRPC responses for clients that advertise support | `codemode/grpc_zstd.go`, `codemode/grpc_stdio_host.go`, `codemode/grpc_session_provider.go`, `codemode/grpc_process_provider.go`, `codemode/grpc_zstd_test.go` |
| `#55846` | Measure Guardian review latency once on the monotonic clock | `appserver/guardian_reviewer.go`, `appserver/guardian_review_metrics.go`, like-Rust test |
| `#57220` | Log WebSocket compression payload statistics | `appserver/websocket_compression_stats.go`, `appserver/websocket_compression.go`, `appserver/websocket.go`, like-Rust test |
| `#55958` | Preserve provider catalogs across failed refreshes | `model/models_endpoint.go`, `model/gateway_auth.go`, `auth/gateway_login.go`, `auth/gateway_manager.go`, like-Rust tests |
| `#57178` | Route FedRAMP API keys to government endpoints by default | `auth/api_key.go`, `model/fedramp_routing.go`, `model/auth.go`, `model/provider.go`, `model/models_identity.go`, `config/config.go`, `appserver/runtime_router.go`, `appserver/realtime_runtime.go`, `doctor/doctor.go`, like-Rust tests |
| `#55973` | Let consumers select message-board tools | `agentboard/tools.go`, `agentboard/append_test.go` |
| `#56461` | Silent message-board `append` without subscriptions or notifications | `agentboard/api.go`, `agentboard/inmemory.go`, `agentboard/local.go`, `agentboard/remote.go`, `agentboard/tools.go`, like-Rust tests |
| `#55591` | Measure Guardian approval blocking time per turn | `appserver/guardian_review_blocking.go`, `appserver/guardian_reviewer.go`, `appserver/runtime_router.go`, `appserver/turn_runtime.go`, `appserver/thread_shell_command.go`, `telemetry/metric_names.go`, like-Rust tests |
| `#56265` | Built-in Google Cloud Vertex AI provider, `gcp` account type, Vertex catalog and capabilities | `model/provider_info.go`, `model/provider.go`, `model/provider_config.go`, `model/auth.go`, `appserver/protocol.go`, `appserver/schema.go`, `appserver/rust_protocol_surface_test.go`, `auth/account.go`, `model/gcp_test.go` |
| `#56560` | `model_providers.gcp.project_id`/`location` override the environment | `model/provider_info.go`, `model/provider_config.go`, `model/gcp_test.go` |
| `#57188` | `model_providers.gcp.credential_file` for command auth | `model/provider_info.go`, `model/auth.go`, `model/provider_config.go`, `model/gcp_test.go` |
| `#57394` | Experimental `account/gcp/setup`; `config/read` reloads requirements and republishes the network policy | `appserver/gcp.go`, `appserver/gcp_test.go`, `appserver/runtime_router.go`, `appserver/bedrock.go`, `config/api.go`, `config/gcp_prepare_test.go`, `app/app.go` |
| `#57360` | Bound the thread writer coordination lock wait (50 ms retry, 30 s timeout) | `session/writer_lock.go`, `session/writer_lock_timeout_test.go` |
| `#57358` | Serialize session index mutations across processes; preserve index permissions | `rollout/session_index.go`, `rollout/session_index_lock_test.go` |
| `#57357` | `migrate-nfs-db` reports no databases instead of success | `app/migrate_nfs_db.go`, `app/migrate_nfs_db_test.go` |
| `#57352` | npm wrapper preserves child signal failure exit status | `npm/codex/bin/codex.js` |
| `#57241` | Encrypted Guardian transcripts honor the configured message limit instead of a hard-coded 9,000-token ceiling | `appserver/guardian_reviewer.go`, `appserver/guardian_native_agent_message_like_rust_test.go` |
| `#55991` | Avoid double-counting retained reasoning when the server reports it as included | `compact/compact.go`, `compact/compact_test.go`, `turn/agent_loop.go`, `appserver/turn_runtime.go`, `appserver/runtime_router_test.go`, `exec/exec.go` |
| `#56211` | Fresh memory profiles default to V2; reset of an absent workspace is a no-op; fresh profiles report `v2_ready` | `config/memories.go`, `config/config.go`, `config/memories_version_test.go`, `appserver/memory_status.go`, `appserver/memory_status_test.go`, `memories/control.go`, `memories/control_test.go` |
| `#57275` | Premium `imagegen_premium` usage-limit failures keep structured metadata and fall back to the exhausted window's reset time | `turn/image_generation.go`, `turn/image_generation_test.go` |
| `#56033` | Bedrock setup wizard is stable and enabled by default, with an explicit opt-out | `features/features.go`, `features/features_test.go` |
| `#55701` | MCP OAuth refresh coordination is stable and enabled by default | `features/features.go`, `features/features_test.go` |
| `#56163` | Reasoning-effort override is stable and enabled by default | `features/features.go`, `features/features_test.go`, `exec/reasoning_effort_override_test.go` |
| `#56371` | `auto_review.timeout_ms` configures the overall Guardian review budget; `0` waits without a deadline | `config/config.go`, `config/guardian_timeout_test.go`, `appserver/guardian_reviewer.go`, `appserver/agent_runtime.go`, `appserver/guardian_review_timeout_like_rust_test.go` |
| `#57272` | Code-mode output-truncation contracts: session options, wait/terminate budgets, `bounded-output-v1`, stdio and gRPC schemas | `codemode/protocol_types.go`, `codemode/protocol_host.go`, `codemode/session_provider.go`, `codemode/session_runtime.go`, `codemode/session_host.go`, `codemode/stdio_host.go`, `codemode/grpc/codex.code_mode.v1.proto`, regenerated `codemode/grpc` bindings, `codemode/protocol_truncation_test.go` |
| `#55545` | Preserve undecodable compressed rollouts for diagnostics and log bounded read details | `appserver/rollout_archive.go`, `rollout/read_diagnostics.go`, `rollout/line_reader.go`, like-Rust tests |
| `#55581` | A failed background compaction releases the foreground waiter so it falls back instead of hanging | `appserver/async_compaction.go`, like-Rust test |
| `#55971` | Local message boards can select an isolated storage namespace | `agentboard/local_options.go`, `agentboard/local.go`, `agentboard/connection.go`, `agentboard/configure_local_board.go`, like-Rust test |
| `#55692` | Tool-registration metrics carry the effective `incremental_tools` mode | `turn/tool_registration_metrics.go`, `turn/agent_loop.go`, `turn/runtime.go`, `appserver/turn_runtime.go`, like-Rust tests |
| `#55644` | Pending injections interrupt `wait_agent` and `clock.sleep` | `turn/steer_mailbox.go`, `turn/tool_dispatcher.go`, `turn/agent_loop.go`, `tool/step_activity.go`, `tool/handlers.go`, `appserver/agent_controller.go`, `exec/agent_controller.go`, like-Rust tests |
| `#57181` | Optional names on untrusted additional context validate before input admission | `turn/api.go`, `appserver/runtime_router.go`, like-Rust tests |
| `#57176` | Sync Guardian transcripts resolve the node_repl output cap from the turn's evidence mode | `appserver/node_repl_evidence.go`, `appserver/guardian_reviewer.go`, `appserver/turn_runtime.go`, like-Rust test |
| `#57274` | Node REPL evidence renders in Rust's multimodal layout | `context/node_repl_review_evidence.go`, like-Rust tests |
| `#57240` | Memory baselines are bounded to 10 MiB and recover as empty snapshots under a cross-process lock | `memories/workspace.go`, `memories/workspace_test.go`, `memories/pipeline_test.go` |
| `#56250` | Resuming a thread with unchanged settings no longer appends a duplicate settings checkpoint | `rollout/session_items.go`, `rollout/rollout.go`, `appserver/router.go`, like-Rust tests |
| `#57185` | Unified-exec captured output keeps Rust's byte-omission marker through model truncation | `tool/unified_exec_buffer.go`, `tool/unified_exec.go`, `tool/shell.go`, `tool/shell_executor.go`, like-Rust tests |
| `#56414` | External-agent memory imports populate the selected namespace and always v2, queueing consolidation per namespace | `config/external_memory_import.go`, `config/external_memory_import_test.go`, `config/api.go`, `appserver/external_memory_settings.go`, like-Rust tests |
| `#56462` | Imports and consolidation share a cross-process workspace lock; a busy workspace rejects imports and skips consolidation | `memorylock/lock.go`, `memories/pipeline.go`, `config/external_memory_import.go`, app-server/busy-workspace tests |
| `#55987` | Registry requests refresh rejected remote exec-server credentials and replay once; executor credentials retry only after the parent rotates them | `execserver/remote_auth.go`, `execserver/remote.go`, `execserver/remote_harness.go`, `execserver/remote_relay.go`, `execserver/executor_credential.go`, `app/exec_server_auth.go`, `app/app.go`, like-Rust tests |
| `#57441` | Feature `guardian_native_multi_agent_history` (under development, off) for native MAv2 calls in Guardian history | `features/features.go`, registration test |
| `#57425` | Guardian root request preparation is centralized: a 16-message shared cap, legacy-checkpoint copies deduplicated after truncation, verified-answer and incomplete-context notices, and all user restrictions preserved during handoff filtering | `appserver/guardian_root_context.go`, `appserver/agent_runtime.go`, `appserver/guardian_root_handoff.go`, `appserver/retained_context.go`, `state/guardian_retained_context.go`, `context/user_goal.go`, like-Rust tests |
| `#57420` | Thread item `started_at_ms`/`completed_at_ms` are optional nullable in the TypeScript exports so older servers can omit them | `appserver/schema/precomputed/app-server-exports-stable.json.zst`, `appserver/schema/precomputed/app-server-exports-experimental.json.zst`, pinned SHA-256 values re-anchored in `appserver/schema_test.go` |

Grounding notes:

- `#57276` (extension world-state window prefixes) is N/A: Rust adds
  `WorldStateSectionContribution::with_window_prefix()` for extension
  contributors, and Go has no extension world-state section registry to carry
  that prefix; its built-in world-state updates already place
  `WorldStatePrefix` with the tool catalog.
- `#56234` (prompt telemetry ordering) is structurally N/A: Go never wires
  `RunUserPromptSubmit` into turns, so there is no pre-hook telemetry path to
  order against. Recorded in `update/plan_2026_09_11.md`.
- `#57293` GCP/Vertex picker portion is now covered by the `#56265` provider
  catalog (only Vertex-capable models are listed for `gcp`).
- `#57249` directory/featured prefetch sites do not exist in Go; only the
  suggested-plugin prewarm path is gated.
- `#56263` is N/A: Go has no marketplace allowed-source/host-pattern policy
  (`restrict_to_allowed_sources`, `host_pattern`), so there is no allowlist
  decision to harden against URL-parser backslash handling.
- `#55593` is N/A: the Go build ships no `windows-sandbox-service` binary or
  package-identity connection gate to tighten.
- `#57233` is N/A: Go has no codex-home `.env`/dotenv loader to deduplicate.
- `#57247` is N/A: release rollout maintenance uses `gofrs/flock` with explicit
  `Release()` and no `Drop`-style finalizer.
- `#57193` is N/A: Go has no rollout-trace/replay-bundle package.
- `#57245` is N/A: Rust test-only queue stabilization.
- `#57199` is N/A: the 240-byte JSON summary truncation lives in Rust's
  `rollout-trace` reducer, which has no Go counterpart.

- The `rollout-trace` family is N/A: `#57311`, `#57280`, `#56524`, `#56515`,
  `#56342`, `#56312`, `#55725`, `#55710`, `#57194`, `#56011`, `#55993`. Go has
  no `rollout-trace` recorder or event model — no thread trace events such as
  `codex.tool_result_ready`, per-turn/code-cell/terminal spans, or the
  conversation/tool reducer these commits extend. `rollout.ReduceTraceBundle`
  only summarizes an existing `trace.jsonl`, `memories` extracts conversation
  items straight from rollout JSONL, and the `codemode` package has no tracer,
  so these reducer and instrumentation changes have no Go surface.
- `#56535` is N/A: the commit extracts Rust's head-tail buffer into a utility
  crate; Go already has `exec.HeadTailBuffer` and `utils.PlanTokenTruncation`.
- `#55652` is N/A: Go has no rollout-file search module
  (`codex-rs/rollout/src/search.rs`); thread history search reads the SQLite
  state database instead.
- `#55456` is N/A: Go has no system config layer or system `requirements.toml`
  (`config/local_layers.go` documents this), so there is no Windows registry
  override to merge into the requirements stack.
- `#55677` and `#56439` are N/A: a Rust toolchain/CI pin and a Cargo dependency
  bump (`ctutils`), neither of which exists in the Go module.
- `#56454`, `#56453`, `#56293`, `#56292`, `#56018`, `#56007`, `#56006`, and
  `#56301` are N/A: Rust test-only stabilization, fixture, or `#[cfg(test)]`
  helper changes with no Go analogue.
- `#57261` is N/A: behavior-preserving refactor of Rust's apply_patch
  handler/runtime (`prepare_apply_patch_request`,
  `ApplyPatchRequest::approval_action`). Go's `tool.ApplyPatchExecutor` has no
  separate orchestrator/runtime/approval-action layer and no intercepted-shell
  patch path, so there is no equivalent split to extract.
- `#57202` is N/A: the commit instruments a returned Rust tool-runtime future
  with the dispatch span (`.instrument`/`.in_current_span`). Go carries spans
  in `context.Context` through synchronous calls and has no detached futures to
  instrument.
- `#57260` is N/A: introduces Rust's `PreparedPermissionRequest` to share
  argument parsing, path resolution, and approval-action construction. Go's
  `tool/request_permissions.go` resolves its request inside one executor and the
  approval path lives in the app-server, so the Rust type split has no Go
  counterpart.
- `#57257` is N/A: removes redundant connector-directory loads and clones in
  Rust's discoverable-app suggestions. Go builds `DiscoverableInfo` candidates
  directly from plugin detail and templates and has no cached connector
  directory (`Arc<[AppInfo]>`) to skip.
- `#57229` is N/A: removes Rust code-mode cell-actor pending-initial-yield state.
  Go's `codemode` package has no cell actor, observation routing, or buffered
  completion state; it provides the stdio and gRPC hosts only.
- `#57294` is N/A: moves Rust plugin-manager error types into a new module and
  boxes a realtime handoff future to bound its size. Go's plugin errors already
  live in `plugin`, and Go futures are goroutines without enum-size concerns.
- `#57278` is N/A: extracts Rust agent spawn/startup/communication helpers with
  no behavior change. Go already splits those responsibilities across
  `appserver/agent_controller.go`, `appserver/agent_runtime.go`, and
  `exec/agent_controller.go`.
- `#56505`, `#56533`, and `#56555` are N/A: Rust defers synchronous tool-call,
  tool-output, and message truncation from `collect_transcript_for_target` to
  `ContextProfile::prepare_transcript`. Go's transcript has no separate shared
  collection stage: `guardianReviewTranscriptSelectionWithProfile` resolves the
  target profile once and applies the same per-entry caps immediately before
  `RenderTranscript` charges them, so the rendered text, token costs, and
  `original_bytes` already match Rust's post-refactor output. The host's
  full/delta slice is selected by index, so truncation order cannot change which
  entries are retained.
- `#57295` is N/A: Rust now collects retained instructions, assistant context,
  and verified answers before applying synchronous request limits. Go already
  renders those sections through one snapshot path (`state.RenderRetainedInstructions`,
  `RetainedAssistantMessage`, `RenderVerifiedAnswer`) that applies the same
  900-token whole-record rule and emits the same user/assistant omission notices,
  so no collection/preparation split exists to defer across. The commit's other
  change — applying the retained-source size check to synchronous transcript
  preparation — has no Go effect because Go's transcript entries never carry
  Rust's `RetainedTranscriptSource` label (a pre-window gap from `#48109`/`#51466`).
- `#57312` was a structural move of root-message preparation and handoff
  context selection from `core` into `codex-guardian-context`. Its behavior
  (shared text limits, whole-record verified-answer admission, delivered
  assistant lookup, retained-assistant fallback, and the omission notices) is
  ported with `#57425` in `appserver/guardian_root_context.go`. Go still has no
  `GuardianRootSnapshot::authorization_version`, so the incomplete-evidence
  marker is rendered while Rust's authorization-version flag remains absent;
  trusted-skill paths from the latest legacy turn are likewise not carried.
- `#57258` is N/A: Rust's remote plugin catalog reduces cache probing overhead
  by validating a cache envelope without deserializing plugin records, reading a
  256-byte stale-catalog prefix, and skipping a global-scope probe during
  discovery. Go's cache (`plugin/remote_catalog.go`) is a per-scope JSON file
  keyed by scope/collection and read in full into a typed envelope; discovery
  callers (`app/plugin.go`) request one scope at a time, and a missing `plugins`
  array already falls back to a network fetch, so there is no probe path or
  multi-scope probe to optimize.
- `#57228` is N/A: Rust gives the daemon control-socket `start_pairing` client a
  single 35-second deadline instead of its 2-second per-message timeout. Go's
  `remote-control pair` runs through the in-process `remotecontrol.Manager`
  (`runRemoteControlPair`), not the control-socket client, so no pairing response
  is subject to `ControlSocketResponseTimeout`; that timeout still bounds the
  enable/disable exchanges only.

`#57176` details: the synchronous transcript's node_repl output cap now
follows Rust's `node_repl_result_token_limit` — 6,000 while node_repl evidence is
enabled and the ordinary 1,000-token tool entry cap otherwise — instead of a
hard-coded 6,000. Go applies the cap inside its single profile-driven
collection/retention step, where Rust's `prepare_sync_content` runs before
retention accounting, so the rendered text, token costs, and `original_bytes`
agree. Resolving the mode also repaired evidence collection: it called
`activeRuntimeTurnStateSnapshot` with an empty turn id and therefore never
recorded accepted node_repl results. The dispatcher now passes the executed turn
id, and the mode check uses `ComputerUseReviewRequired` as Rust's
`node_repl_review_evidence_mode` does.

`#57274` details: Go's multimodal node_repl evidence now follows Rust's
`guardian-context/src/node_repl.rs` layout — the `<node_repl_review_evidence>`
opening marker and introduction, each retained response's header and text with
its accepted inline images in place (deduplicated across the snapshot), a
`<omitted node_repl_responses="N" reason="resource_bounds" />` notice, and the
closing marker. Text and multimodal layouts share one newest-suffix selection
helper with Rust's 32,000-byte rendered budget and layout-specific notice
reservation, so an oversized snapshot keeps the same responses in both layouts.
Go previously delivered the multimodal evidence as one text blob without marker
framing followed by every image, which lost the per-response headers, image
positions, and marker boundaries.

`#57240` details: memory baselines now carry a `1` version marker under `.git`,
and preparation, diffing, and resets serialize on a `flock` inside
`.codex-baseline/.git`, which sits outside the replaceable repository and is
excluded from scans and diffs. Missing, legacy, or interrupted metadata is
replaced by an empty-tree baseline, so existing working files remain visible to
the next consolidation as additions; `ResetWorkspaceBaseline` records the
current files instead. Files above 10 MiB are never staged into the snapshot,
are skipped by recovery additions and consolidation diffs, and a tracked file
that grows past the limit invalidates the baseline rather than staying silently
processed. `readWorkspaceEntry` bounds its read with an EOF probe byte.

`#56250` details: a resume compares the effective settings snapshot with the
latest `thread_settings_applied` event owned by the same thread before appending
a checkpoint. Unchanged settings no longer add a duplicate event even when no
turn follows the resume; changed settings, or a missing/foreign-only snapshot,
still persist. Comparison normalizes fields exactly as persistence does and
keeps an explicit empty runtime-workspace-root list distinct from an omitted
selection.

`#57185` details: unified-exec collection drains its head/tail buffers with
Rust's `... N bytes omitted ...` marker at the retained boundary, and transcript
end events carry the same marker. `ShellResult` retains the omitted byte count
so model-facing truncation re-adds the notice when middle truncation removed
it, without duplicating a marker that survived. Local shell output is unchanged.

`#56414`/`#56462` details: external-agent memory imports now mirror Rust's
`memory_import.rs`. Detection and import resolve the applicable namespaces
(always v2, plus v1 when selected or when a legacy `memories` workspace
exists), compare each owned project's files, `scope.json`, and extra paths to
decide what is pending, copy raw source files with compact `{"cwd": ...}`
scope files and the shared extension instructions, and enqueue one
consolidation for each namespace whose resources changed. Import and
consolidation exclude each other through `memorylock`'s sibling
`memories.lock`/`memories_v2.lock` files: every destination is locked before
any workspace is touched, a busy lock rejects the import with Rust's retryable
`external_agent_config_import_error`/`import_request_failed` result, and phase
two records `skipped_workspace_busy` (or `failed_workspace_lock`) instead of
claiming the job. Go still carries discovered files in
`MigrationDetails.MemoryFiles` rather than Rust's project-key list, so the
import re-discovers source files under the locks and cleans up owned projects
without a current source; success entries use Rust's project-key `cwd` and v2
resources target.

`#55987` details: remote exec-server registry traffic now goes through one
helper that resolves auth per attempt and replays a request at most once after
HTTP 401 when the provider reports replacement credentials
(`RemoteEnvironmentConfig.RefreshAfterUnauthorized`). Registration, Noise
connect, and harness-key validation all use it, the connect deadline still
covers auth, refresh, the replay, and the response parse, and every other
status or transport failure stays terminal. The sealed executor-credential
pipe retries only when its parent already delivered a different bearer. The CLI
now wires ChatGPT auth (and workload identity) to a live store-backed provider
instead of one-time static headers: it reloads the stored credential, refreshes
proactively, refreshes the rotated ChatGPT token under a guard so concurrent
callers share one refresh, reuses a credential another writer already stored,
and refuses to replay a credential with a recorded permanent failure. Go has no
long-lived `AuthManager`, so the account store is the source of truth and a
mutex stands in for `refresh_lock`; the harness-key validation request still
omits Rust's optional `x-codex-noise-stream-id` header because Go's provider
signature never carried the stream id.

`#57425` details: `prepareGuardianRootContext` now applies Rust's fixed root
request policy in one place. Root user instructions and verified answers come
from the thread's retained evidence (with the raw-item fallback when none is
retained yet); assistant evidence comes from live output plus retained
fallbacks, and delivered messaging calls supply their host-confirmed text. All
user restrictions are kept during handoff filtering, the shared cap is 16
(`#47947`), and dropping user records, verified answers, or assistant context
adds the matching host notice instead of silently narrowing authorization.
Legacy checkpoint instructions are prepended behind `LegacyContextScope`, and a
record without a message ID is matched to a retained source by turn ID and
post-truncation text so the worker receives one copy. Go retains the original
legacy body (including surrounding whitespace) and now records retained
evidence through `GuardianTruncateText` so both sides use Rust's omission
marker. Retained evidence compaction bounds still keep 8 records per family, so
storage eviction itself marks the projection incomplete. Since Go has no
`authorization_version`, the only observable difference from Rust is that the
snapshot flag is absent while the omission notice is present.

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

`#57313`: omitted `gateway_oauth.delivery` now resolves to header
`Authorization` / scheme `Bearer` (also when the table itself is absent). The
provider is rejected when it already supplies credentials via
`requires_openai_auth`, `env_key`, `experimental_bearer_token`, or command auth,
and `model_catalog_url` must share scheme/host/port with `base_url`. A gateway
token may no longer collide with a provider-set header.

`#56309`/`#57263`: the permission catalog in `model/catalog.go` carries
`writable_root`, `writable_roots`, `omitted_paths`, and `denied_reads`, and
`sandbox/permission_prompt.go` renders them with the same bounds as Rust
(500-byte presentation budget, 1000-byte writable-root template with exactly
one `{{ roots }}`, `{{ denied_reads }}` only within path budget + policy + 500,
and `omitted_paths` prepended to the mandatory warning). All control
characters except newline, plus U+2028/U+2029 and permission tags, are rejected.

`#57286`: `network_proxy` is now the canonical managed requirements table and
`experimental_network` remains a compatibility alias. Parsing, cloud-config
layer merging, and direct map readers all normalize the alias before merging.
A single layer that declares both is rejected as a duplicate `network_proxy`
field, and mixed-shape diagnostics use Rust's wording (`domains` /
`unix_sockets` cannot be combined with their legacy list forms in managed
network requirements). The ignored-setting hint points at `[network_proxy]`,
the recognized requirements key list accepts both spellings, and
credential-broker project discovery works through either spelling. The TUI
debug-config label stays in the TUI lane.

`#57304`: workspace discovery refresh failures now surface the cached permanent
`RefreshTokenFailedError` instead of the generic 401 when the auth generation is
unchanged and workload-identity external auth is not selected; transient
refresh failures keep the generic discovery error. Turn failures carrying that
error classify as `codexErrorInfo: unauthorized` with
`codexErrorKind: refresh_token_failed`, so clients show the sign-in guidance.

`#57287`: `thread/fork` gains `experimentalDisablePredictionMcp`, rejected
without `experimentalPredictionMode` with Rust's message, and
`thread/sample` gains `disableMCP`. A prediction fork, or a sample, only
disables MCP when the inherited request history already carries an
`additional_tools` catalog and the model uses Responses Lite. That thread is
marked `mcp_servers_disabled`: the MCP projection then carries no servers, apps,
connectors, plugin contributions, or executor-discovered servers, and executor
discovery is skipped. With the default-enabled
`prediction_preserve_parent_tools` feature the thread keeps the inherited
declarations instead of re-declaring tools inside the copied context window
(only a new window re-derives the catalog), records no removal notice, and
appends the developer notice
`<mcp_tools>\nMCP tools are unavailable for this request.\n</mcp_tools>`
(hidden from the thread). Samples send no new `tools` catalog, and a sample
with `disableMCP` for a non-lite model or a history without a catalog keeps the
current behavior.

`#57270`: sandbox invocations now mirror Rust's managed-requirements mode:
omitting an explicit permission profile includes managed requirements, while a
profile invocation only does so with `--include-managed-config`. Cloud config is
bootstrapped whenever that mode includes managed config, except when
`--sandbox-state-json` supplies the sandbox state. Eligible ChatGPT identities
therefore receive cloud providers and requirements even without `-P`, and a
local `application.network` policy limits the bootstrap request.

Remote `exec-server` registrations also load cloud config for ChatGPT auth so
cloud-defined providers and requirements are present before the registry client
is created. The loader is skipped for `--executor-credential-stdin` and
`--use-agent-identity-auth`; `CODEX_API_KEY` (and the other API-key selections)
bypass ChatGPT cloud config. Every exec-server config path includes managed
requirements and activates its effective application network policy before
creating HTTP clients, so a cloud policy can deny registration. Go has no
`--aws-sigv4` transport flag, so that Rust exclusion has no counterpart.

`#57192` details: V2 discovery now requires both the `capabilityDiscoveryV2`
flag and an executor version at least `0.164.0`, mirroring Rust's
`supports_capability_discovery_v2` (build info compared by release precedence,
so the cutoff prerelease is incompatible and a newer prerelease is not).
Versions that Rust's `semver::Version::parse` rejects — empty, malformed,
shorthand `0.164`, or `v`-prefixed — stay incompatible. The compatibility check
is shared by the exec-server client (`DiscoverCapabilitiesV2` rejects before the
RPC with `capabilities/discoverV2 is unavailable in this environment session`)
and by session discovery. When incompatible executors yield no V2 snapshots the
step returns nil, so V1 discovery keeps selected roots and skills and any
previous V2 inventory is dropped. `cmd/codex` stamps `environmentInfo`'s
`executorVersion` from `appserver.CodexVersion()` at startup; unstamped builds
report `0.0.0` and are incompatible, as in Rust.

`#56049` details: `RemoteNetworkProxyConfig` now carries `credentialProviders`
(the snake_case `CredentialProviderConfig` wire shape) alongside
`credentialMasking`. Provider rules are dropped on decode whenever masking is
disabled, and the executor copies them into its proxy settings so the
executor-local broker resolves configured providers exactly like built-in
ones; credential values and destination context still stay on the executor.
`EnvironmentCapabilities` advertises `credentialMaskingCustomProviders` and
`NetworkProxy()` projects the bits into Rust's `RemoteCredentialMasking`
(disabled / built-in-only / with-configured-providers). Unified exec requests
configured-provider masking, then downgrades to unmasked execution before the
process starts when the executor accepts only built-in providers, emitting
Rust's "unavailable for this network proxy configuration" warning; executors
without masking at all keep the "does not support it" warning.

`#57187` details: `features.rollout_budget.enforce_limit` defaults to `true`
and is validated as a boolean. `false` keeps shared weighted-token accounting
and `<rollout_budget>` reminders active (including the zero-remaining
reminder) while `RecordUsage` never reports exhaustion, so the current and
later turns continue; the Go budget models this as `BudgetConfig.Advisory`,
whose zero value still enforces the limit.

`#56445` details: a provider error whose `code` is `model_not_found` is now a
terminal classification instead of a retryable generic failure. HTTP 400/403/404
bodies, standalone streamed `error` events and the `response.error` payload of
`response.failed` all map through Rust's `parse_terminal_provider_error`: the
provider message is preserved (falling back to `model_not_found` when blank) and
the HTTP status is kept when the error arrived with one, so statusless stream
errors report no status in analytics. Other statuses keep their existing
classification, and WebSocket error events are terminal on 400/403/404 or when
statusless. The new `ErrorModelNotFound` kind joins the non-retryable set, maps
to `modelNotFound`/`model_not_found` for app-server turn analytics (reporting
`httpStatusCode` only when present), is accepted by the rollout error-info
reader, and gets Rust's "model not found" response-debug telemetry text.

`#56468` details: the remote fs-helper environment filter now mirrors Rust's
`helper_env_from_vars`. Alongside the portable `PATH`/`TMPDIR`/`TMP`/`TEMP`
allowlist it keeps the Windows platform entries MXC needs (`SystemDrive`,
`SystemRoot`, `LOCALAPPDATA`) with case-insensitive name matching while
preserving the host's own spelling, and macOS keeps
`__CF_USER_TEXT_ENCODING`. Non-Windows hosts still ignore the Windows entries.

`#56022` details: `-c`/`--config` values supplied at the root and at a nested
command are all retained in parse order. Go's hand-rolled parser already
collects each scope separately, but `mcp`, `plugin` (including `plugin
marketplace`) and `cloud` only received their own options, so root overrides
were dropped. `cli.Parse` now prepends the root overrides for those commands
(the ones whose runners already merge `RootOptions` are untouched, avoiding
duplicates), while forwarded `--` arguments keep their existing treatment.

`#56552` `#57225` `#57226` details: a writable SQLite pool now probes the
directory that holds its own database, so a separately configured logs home is
evaluated independently. New databases initialize with DELETE journaling on NFS
and WAL elsewhere; connections that end up in DELETE raise `synchronous` to
EXTRA, and existing schemas keep their journal and vacuum modes. The managed
`unsafe_enforce_sqlite_journal_mode` requirement (`requirements.toml` and cloud
layers, snake_case or camelCase; managed-only, as in Rust, since ConfigToml has
no such field) is read from `Config.Requirements` and reaches `SqliteConfig`
through `appserver.resolveDefaultStateRuntime`,
`prepareSharedStateRuntime` (effective config with managed layers) and the
message-board database. On NFS it migrates every runtime database in the home
under the directory lock before the writable pool opens, then each connection
must report DELETE (`NFS database requires DELETE journaling, found <mode>`).
Migration stages a local copy, requires DELETE plus `integrity_check(1)`,
preserves the source mode bits, writes a private `<name>_wal_migration_backup`
directory with a `codex-nfs-migration-v1` marker, renames the original and any
remaining sidecars aside, and hard-links the replacement into place; an
interrupted cutover is resumed from the marker by the migration itself, and a
resumed install hard-links rather than truncating an in-use inode. Deliberate
Go difference: modernc exposes no `SQLITE_DBCONFIG_NO_CKPT_ON_CLOSE`, so the
source connection is closed (checkpointing the WAL into the still-installed
source) before the original is moved, keeping the backup a complete database;
the staged copy is validated first and the descriptors close after the
connection, so the exclusive lock is held through the copy. Migration failures
are re-created without the SQLite error chain so automatic backup-and-rebuild
corruption recovery never discards the preserved WAL backup, and the 0644
consent disclosure is printed with a `[y/N]` gate. CLI: `codex migrate-nfs-db
--staging-dir <dir>` (defaults to the system temporary directory, Unix only)
skips the logs database when `log_dir` is configured, as Rust does. Go has no
`legacy_logs_db_path` (it never relocates the logs database), so Rust's
enforcement-time skip of legacy NFS log cleanup has no counterpart here; that
`log_dir` relocation remains a separate pending upstream item.

`#56495` details: `LocalAgentMessageBoard.SubscribedChannels` lists the caller's
channel subscriptions on the current board in name order using the existing
`PageRequest` budget (normalized default 20, hard cap 50, base64 offset cursor
and one-item lookahead). It validates caller membership like every other board
operation, probes `subscriptions` with Rust's `json_object('Channel', c.name)`
correlation so thread subscriptions never appear, and returns a pure `Page[string]`
(vs. `ListChannels`, which adds board permissions). Like Rust, the method is on
the local board rather than the `Board` interface.

`#55690` details: marketplace staging cleanup now reclaims directories left
behind by older binaries, which publish no owner lock. Before creating a
staging checkout, `materializeGitMarketplace` scans the `.staging` directory it
is about to use (`<install root>/.staging`, matching Rust's layout) and removes
up to four entries per pass: `marketplace-add-*` directories older than 24 hours
and `marketplace-upgrade-*` directories older than 10 minutes by modification
time. Recent clones are preserved so a concurrent upgrade can coexist with an
in-flight checkout, and symlinks, regular files and unrelated names are never
touched. Go publishes no owner lock, so every Go checkout is treated as a
legacy entry; cleanup is best effort and cannot fail an install. Go never
creates `marketplace-upgrade-*` directories itself, so that branch reclaims
Rust leftovers in a shared `CODEX_HOME`.

`#57251` details: the app-server `ThreadUsage` payload already gained the
optional `nativeUsageUsdMicros` field and the backend-to-wire forwarding in the
TUI lane's change to `auth/account.go` and `appserver/runtime_router.go`. This
lane adds the core regression coverage Rust ships with the change: the
`account/usage/read` response forwards a backend native amount alongside the
credit estimate, an explicit zero stays zero, and a missing native amount stays
absent so callers see unknown rather than `$0.00`. Rust exposes no native
amount on `ThreadUsageBreakdownGroup`; Go's cloud client decodes that group
field but the app-server type still omits it, matching the Rust wire surface.
Credit/native display selection (ENT26) remains TUI-owned.

`#56553` details: `hooks/list` accepts `threadId` alongside `cwds`. Thread mode
rejects a request that names both, requires a thread loaded in this process
(`thread not found: <id>` otherwise, matching Rust's thread-manager lookup),
and returns exactly one entry keyed by the thread's working directory. Because
Go has no per-environment hook declarations (`[environment_hooks]`) and no
`TurnEnvironmentSnapshot` hook sources, the thread's cwd inventory is already
its complete inventory; Rust's extra environment-hook merge has no Go
counterpart to add. `HookMetadata` now serializes `environmentId` (always
present, null for host-local hooks) instead of keeping the owner private, so
the wire shape matches Rust's SDK surface. The TUI hooks browser switch to
thread mode belongs to the TUI lane.

`#57186` details: the ripgrep-backed deny-glob expander now inspects each
search root before grouping patterns. A directory (including a symlinked
directory, because `os.Stat` follows links) is scanned, an absent path or a
path below a non-directory contributes no matches, and any other metadata
failure rejects the whole expansion with the root and underlying error instead
of dropping the deny rule. The in-process fallback already failed closed via
`doublestar.WithFailOnIOErrors`.

`#57209` `#57201` `#57207` details: `codex_turn_event` carries Rust's eight
critical-path fields (`critical_path_status` and the duration/inference/
auto-review/user-wait/tools/overhead/unknown milliseconds). With the
default-off `critical_path_timing` feature the payload reports `disabled` with
null durations; nulls are preserved rather than zeroed so a missing measurement
can never be read as a zero duration.

`criticalpath` ports `codex-utils-critical-path`: immutable shared-prefix paths,
latest-wins joins, clipping, a 16,384-segment bound that collapses into unknown
work, and exclusive interval attribution with unrecorded gaps as overhead.
`tooltiming` ports `codex-direct-tool-timing`: per-invocation guards, duplicate
external call IDs invalidating attribution, user waits taking precedence over
auto reviews, batch completion, and execution-gate handoffs. The turn profile
extends the path on every phase transition (sampling and compaction as
inference, tool blocking through the batch's completed dependency, idle time as
overhead) and freezes the categories into `Profile.CriticalPath`.

Capture is enabled only for root-agent turns (`tasks/regular.rs`), before
`runtime.Run`, so pre-turn failures and compaction still report coverage. Code
Mode and multi-agent tool paths are not captured yet; those dispatches
invalidate the turn instead of guessing an owner. Review turns do not capture.
Failed turns report `incomplete` rather than publishing unsettled totals.

`#57207` details: `request_user_input` now passes the calling tool's call ID
through a `UserInputCallIDResponder`; command, file-change, permission, and
`write_stdin` approvals plus Guardian reviews and network approvals start a
user-wait/review guard on the owning invocation. Waits without an owner
(including MCP elicitations and the MCP dependency-install prompt) invalidate
the turn's capture instead of charging an unrelated call.

Caveats: Go has a single cancellation path, equivalent to Rust's graceful
interrupt, and every tool dispatch settles before the turn profile freezes, so
Rust's forced-teardown invalidation (`tasks/mod.rs`) has no Go counterpart; no
invalidation is needed. Config changes to `critical_path_timing` take effect on
the next turn because the dispatcher policy is fixed at turn start.

`#57198` `#57203` details: tool hook requests now carry a `toolCWD` that
becomes the hook input's `cwd`, while `cwd` still selects the directory the
hook process runs in. `exec_command` resolves it with the same
`resolveShellRequestCWD` used for execution, from the selected turn
environment's cwd plus the command's `cwd`/`workdir`, so default, empty,
relative, and absolute workdirs agree with where the command actually runs.
Remote environments report nothing, keeping the host cwd, because their paths
do not exist on the host that launches hooks; Go has no per-environment hook
process relocation to add. `PermissionRequest` derives the same value for shell
approvals from the resolved `ShellRequest.CWD` (local by construction at that
call site), for `write_stdin` from the request's own environment and cwd, and
for network approvals from the originating call's trigger cwd when the
environment is local. `apply_patch`, `request_permissions`, and MCP approvals
keep the host cwd, matching Rust's `tool_cwd: None` for those payloads.

`#56494` details: `upsert_channel` creates a channel or replaces only its
description. The local board updates an existing row with a single `UPDATE`
(no host-clock call), and only falls back to `INSERT ... ON CONFLICT DO UPDATE`
after the clock when the channel is new, so posts, author, creation time, and
subscriptions survive and no notification is sent. The in-memory board mirrors
that recheck-after-clock ordering. The remote board rejects the operation with
Rust's exact message because its service has no description-update RPC. The
tool surface now also accepts `description` on `create_channel` (Rust's spec
lists it), validates the 512-byte bound at argument decode and again in the
boards, and reports `null` on success. Read-only local connections are rejected
through the `channel.edit_metadata` write requirement.

`#57212` details: the code-mode host negotiates zstd for gRPC responses.
Go registers a klauspost/compress-backed `zstd` encoding, which makes every
code-mode client advertise `grpc-accept-encoding: zstd`, and the host selects it
per stream with `grpc.SetSendCompressor`. A client that does not advertise zstd
makes that call fail harmlessly, so its responses stay identity-encoded, exactly
Rust's `send_compressed(Zstd)`/`accept_compressed(Zstd)` pairing. The encoder is
pooled and uses the fastest level, matching Rust's Bazel patch that forces tonic
level 1 for response latency; there is no Go build patch to carry.

Reviewed in this window with no Go change:

- `#57314` (format Rust sources), `#57277` (crates.io `i18n-config`),
  `#57255`/`#57206`/`#55547`/`#55616` (dependency advisories and lockfiles) are
  Rust build/dependency maintenance. Go pins its own module set, so there is
  nothing to mirror.
- Test-only stabilizations `#57248`, `#57244`, `#57239`, `#57238`, `#57234`,
  `#56557`, `#55848`, `#55524`, and `#55620` change Rust test harnesses; Go has
  no counterpart fixtures or tokio scheduling to stabilize.
- `#57290` (plugin process host with IPC) is absent from Go: no
  `ProcessHost`/plugin-IPC symbols exist outside the code-mode host test helper,
  and plugins run in-process.
- `#57262` (`skill_catalog_deltas` incremental catalogs) is a feature Go does
  not define; the flag and its delta protocol have no counterpart.
- `#55946` (bounded remote installed-plugin inventory pagination) is N/A: the Go
  plugin client has no cursor-paginated inventory fetch (`plugin/` has no
  `NextCursor`/cursor for installed plugins).
- `#55645` (in-process app-server shutdown with full queues) is N/A: Go has no
  Rust-style `InProcessClientHandle`; `DisableCodeModeInProcessFallback` is
  unrelated.
- `#57193` (replay failed `write_stdin` rollout traces) and `#57199` (UTF-8-safe
  240-byte JSON summary truncation in rollout traces) are N/A: Go has no
  `rollouttrace`/`rollout_trace` package.
- `#57167` (CUA tool-call lifecycle starts) is N/A: Go exposes no CUA/computer-use
  tool, only the `browser_computer_use` config surface.
- `#57256` (parent history in attached thread views) targets TUI thread views;
  it belongs to the TUI lane.
- `#57253` (slow daemon startup with early-exit detection) lives in
  `app/daemon_startup.go`, which this lane does not touch (TUI lane owns it).
- `#57201`/`#57207` (critical-path capture and approval-wait attribution) are
  implemented above; see their caveats for forced teardown and unsupported
  Code Mode/multi-agent ownership.

`#55846` details: the review metric used to subtract two wall-clock
millisecond timestamps, so a clock change or a delayed emission changed the
reported latency. The reviewer now captures `time.Now()` when the review
starts and derives the duration with a single `time.Since`, which reads Go's
monotonic clock; non-positive or unrepresentable intervals saturate at zero.
Go records no Guardian analytics `completion_latency_ms`, so the one captured
interval is the metric's only consumer.

`#57220` details: the transport now pairs its TX and RX zstd codecs with one
connection identity (`ID`, `Kind`) and accumulates uncompressed/compressed byte
totals, message counts, and the saved percentage. A listener-created connection
is logged every 60 seconds and once more when it closes; the native-client codec
pair helper exposes the same statistics for client transports to reuse. Only
successfully decoded messages contribute RX statistics, so a rejected frame
cannot inflate the payload totals. `#57218` (remote app-server client zstd
negotiation) is implemented in `app/remote_tui.go`, which the TUI lane owns;
this lane supplies the shared statistics/codec pairing it can consume.

`#55958` details: a failed refresh of an explicit provider catalog no longer
clears the last successful models, ETag, freshness, or persisted cache entry.
Only rejected access does: HTTP 401/403, `RefreshTokenFailedError`, and a
gateway sign-in that is actually required. Transient failures (network,
timeout, 429, 503, 404, malformed bodies) and a sign-in already in progress
preserve the snapshot. Go now keeps the two interactive-sign-in outcomes typed:
`auth.ErrGatewayLoginRequired` invalidates, `auth.ErrGatewayLoginInProgress`
does not, and `composeGatewayAuth` preserves both instead of replacing every
issuer error with the generic message. Rejection also resets `fetched` so
`RefreshOnlineIfUncached` retries; a successful empty response stays
authoritative.

`#55973` details: `NewMessageBoardToolsWithSelection` builds only the
selected `MessageBoardTool` values, in the supplied order, and
`NewMessageBoardTools`/`NewMessageBoardToolsWithOverrides` keep the registered
discussion set (`MessageBoardToolNames`: Rust's `DISCUSSION` order plus
`upsert_channel`) through that same path. Selection only chooses tools; it does
not grant storage permissions the board connection lacks. `append` is not part
of `MessageBoardDiscussionTools`, so the default agent catalog is unchanged.

`#56461` details: `Board.Append` shares posting's write path but never
subscribes the author, collects subscribers, or sends notices.
`AppendRequest` has no recipient field, so the `append` tool's strict decoding
rejects `agents_to_notify`, and the tool is only exposed when a consumer selects
`MessageBoardToolAppend`. Retry IDs share the post namespace: an identical
append returns the original metadata, while a changed payload or an ID already
used by a post (or the reverse) is an invalid request. The local backend keeps
the request fingerprint compatible for posts and stores
`["append", <request>]` for appends, so persisted retries stay distinguishable;
read-only connections keep the same `channel.post`/`channel.reply` permission
checks, and the write still outlives caller cancellation. Remote boards reject
appends without an HTTP request.

`#55591` details: `codex.guardian.turn.blocking_ms` exports, per turn, the
union of the thread's pending approval waits that turn serviced, tagged
`outcome=completed|aborted`. `guardianReviewBlocking` mirrors
`ext/guardian-reviewer/src/blocking.rs`: an interval that opens on the first
pending review and closes on the last, a current turn plus saved durations for
turns that finished late, and one sample per turn. A turn start re-anchors the
interval, so a review that began while the thread was idle charges only the
time after that turn started, and overlapping reviews are counted once. The
`Review` decision enters the guard, so fast decisions, preparation and
cancellation are included; `notifyTurnCompletedOnce` exports `completed` for
turn stop and `aborted` for an interrupted turn, and thread unload drops saved
samples the way Rust's `ReviewBlockingTurn` drop does. Rust records through the
servicing turn's `SessionTelemetry`, whose model attribution Go's metric sink
does not carry; Go exports the same `outcome` tag through `TurnMetrics`.

`#57178` details: `sk-proj-fedramp-` and `sk-svcacct-fedramp-` prefixes are
routing hints, not credential validation. When the selected credential is such
a key, `X-OpenAI-Fedramp: true` is attached and the complete unchanged key still
authenticates. `ResolveAPIProvider` selects `https://gov.api.openai.com/v1`
only when the provider has no configured `base_url`, so an explicit URL -
including an explicitly configured commercial default - always wins. Model
discovery switches to `https://gov.chatgpt.com/backend-api/codex`, the catalog
identity digests that routing, and realtime WebSocket startup resolves both its
host and `Authorization` header from the same selected key (an explicit
`experimental_realtime_ws_base_url` still wins). `config.ChatGPTBackendURL`
mirrors Rust's `ChatgptBaseUrl`: it remembers whether a layer above packaged
defaults supplied `chatgpt_base_url` and resolves the government ChatGPT
backend (`https://gov.chatgpt.com/backend-api/`) from the request's auth
snapshot; app-server config reads use origin metadata so an explicit commercial
default is not mistaken for the packaged default. Provider `APIProviderAndAuth`
returns routing and headers from one snapshot and is used by image generation,
web search, history notes, and the doctor WebSocket probe. Go's analytics
client still admits only Codex-backend auth, so API-key analytics event
eligibility (`can_send_with_api_key_auth`) remains a separate gap; the URL
resolution plumbing is in place for when that gate is widened.

`#56265`/`#56560`/`#57188`/`#57394` details: the Go port now ships the built-in
`gcp` provider (`model_provider = "gcp"`). The Vertex AI OpenAI-compatible
endpoint prefers `model_providers.gcp.project_id`/`location` over
`GOOGLE_CLOUD_PROJECT`/`GOOGLE_CLOUD_LOCATION`, defaults the location to
`global`, and preserves an explicit `base_url`. A configured empty project or
location is rejected rather than falling back to the environment.
`credential_file` must be absolute and is injected as
`GOOGLE_APPLICATION_CREDENTIALS` only into the provider auth command's
environment, so token refreshes keep the override without mutating the parent
process. The runtime provider reports account type `gcp` with
`requiresOpenaiAuth: false`, forces Responses-direct tool mode, disables
Responses Lite/custom tools/web search/image generation/reasoning summaries,
keeps function tools, namespaces and client-side tool search, and uses remote
compaction V2. Its catalog is limited to `gpt-6-astra`, `gpt-6-luna`,
`gpt-6.1-sol`, and `gpt-6.1-terra`.

`account/gcp/setup` is experimental and mirrors Rust: provider login policy is
checked first, the write is previewed against the complete startup layer stack
(packaged/user/project/profile), a custom `base_url` is rejected, credentials
are verified under the application network permit, and the user layer is then
committed against its previewed version (a concurrent write yields
`configVersionConflict`). Failures carry structured reasons
(`gcloudNotInstalled`, `credentialFileInvalid`, `authCommandFailed`) or a
`config_write_error_code`. Success cancels active logins and invalidates the
model catalog so the next `model/list` rebuilds the Vertex catalog. The default
`gcloud` command runs in the app-server startup cwd (`--cd` or the process cwd,
plumbed as `RuntimeRouterOptions.StartupCWD`), never `CODEX_HOME`. `config/read`
now reloads `requirements.toml` and republishes the application network policy
on every call, matching Rust's `ConfigManager::read`; this revokes outstanding
permits, which the network-revocation test now triggers through `config/read`
like the Rust test.

`#57360`/`#57358`/`#57357`/`#57352` details: writer cleanup and session-index
mutations now poll their coordination/file lock every 50 ms and fail with a
timeout (`os.ErrDeadlineExceeded`) after 30 s instead of blocking forever, so a
suspended host cannot prevent idle thread unloading; a timed-out session-index
append leaves the index untouched. Index removals are rewritten through a
unique `session-index-*` temp file that copies the index's permission bits, and
an absent index/home is a no-op. `codex migrate-nfs-db` counts migrated and
already-DELETE databases and prints `No databases found.` when none exist.
The npm wrapper sets `process.exitCode = 128 + signal` and removes terminating
signal listeners before re-emitting the child's signal. `#57353` is N/A: the
Rust change lives in the `mxc-sandbox` crate's policy normalizer, which has no
Go counterpart; `#57373` is a Rust toolchain pin with no Go analogue.

`#55545` details: a compressed rollout that cannot be decoded is now preserved
verbatim when a feedback report reads it and no preferred plain sibling exists,
so the attachment carries the raw `.jsonl.zst` bytes (its filename gains `.zst`
unless an override already ends with it). OS-level read failures still fail,
the size limit still applies, and a plain rollout is never replaced by a
potentially stale compressed sibling. The rollout line reader now wraps the
klauspost decoder and, on the first stream failure, logs
`compressed rollout read failed` with `error_kind`, `compressed_bytes`,
`first_frame_content_bytes`, `decoded_bytes`, and `file_changed` at most once
per process per minute, without paths or contents. `first_frame_content_bytes`
comes from the zstd frame header (RFC 8878); it is absent for Go-written
archives because klauspost's streaming encoder does not advertise a content
size, and a truncated block is withheld by that decoder, so `decoded_bytes`
can be lower than Rust's partial-block output. `read_rollout_prefix` keeps the
plain decoder, as in Rust.

`#55581` details: Go's speculative compaction now delivers a nil result when the
background run fails or produces no candidate, so `TakeReady` and
`WaitForPending` clear the attempt and the turn falls back to blocking
compaction. Previously the runner exited without signalling and
`WaitForPending` blocked until its context ended; this is Go's analogue of
Rust's closed-oneshot panic. `TakeReady` now also clears the result channel
when the candidate's history generation no longer matches, as Rust does.

`#55971` details: `LocalBoardOptions{StorageNamespace}` selects board storage
independently of tool selection and connections, and the zero value keeps the
default namespace. Namespaces are 1–64 lowercase ASCII letters, digits, `_` or
`-`; the default namespace still writes `agent_message_board_1.sqlite`, while a
custom namespace writes `message_board_<namespace>_1.sqlite` so it cannot
collide with another runtime database. `OpenLocalBoardWithOptions`,
`ConfigureLocalBoardWithOptions`, `DeleteLocalBoardsWithOptions`, and
`LocalBoard.WithOptions` all use the same selection, so setup, reopening, and
permanent deletion stay on one namespace and leave other namespaces untouched.
The existing non-options entry points delegate with the default options.

`#55692` details: `codex.tools.registered` now carries an
`incremental_tools` label (`true`/`false`) beside `exposure` and `tool_mode`.
The value is the effective mode for the sampling request — the same decision
that built the tool declarations: Responses Lite plus the feature gate when the
window is new, and whether the current window recorded an incremental catalog
otherwise. `appTurnConfig` computes it once and both `AgentLoopRequest` entry
paths reuse it across sampling iterations and transport retries, so retries do
not duplicate samples.

`#55644` details: `SteerMailbox` records a pending notifying injection per
turn for input queued through `Enqueue` that is not inter-agent mail, mirroring
Rust's `TurnInputQueue::has_pending_injection`; `Drain` and `Clear` drop the bit
with the consumed items. `PendingActivity` reports steering activity for that
bit, a user message, or a function-call output, mailbox activity for queued
inter-agent mail, and none for passive context. `wait_agent` subscribes with
`WatchSteer` and now returns "Wait interrupted by new input." (metrics outcome
`steered`) for a queued injection, including one queued before the wait began;
the exec controller uses the same subscription beside its existing mail
notification channel. Each sampling request also arms `WatchActivity` on its
step context, and `clock.sleep` ends its wait with "Sleep interrupted by new
input." for pending or live activity. That subscription is independent of the
instant-interrupt gate, which only arms code-mode yielding for user messages.

`#57181` details: an `AdditionalContextEntry` name is optional and only valid
on `untrusted` context; it must match `[a-z][a-z0-9_]{0,63}` (at most 64 bytes,
ASCII only). `TurnStartParams.Validate` and `TurnSteerParams.Validate` reject
invalid entries, and `turn/start` validates before it mutates live thread
policy, so both requests return the JSON-RPC invalid-request error (with the
exact Rust message) without admitting input: `turn/start` is rejected before
`turn/started`, and `turn/steer` before the mailbox accepts items. Multiple
invalid entries report the key-sorted first error, matching Rust's `BTreeMap`.
The `name` protocol field and its use as the tool-output label were already
ported with `#57279`, which also keeps Rust's later name-and-value
deduplication for tool outputs.

## Pending in this window

- About 98 non-TUI commits in `update/_cls_core.tsv` still need triage.
  The largest identified gaps, in rough value order, are:
  `#57218` negotiated zstd in the remote app-server client (TUI-owned
  `app/remote_tui.go`; server side and `#57220` statistics are done),
  `#55957`/`#55968` cloud catalog refresh on auth change and failure
  backoff, `#56530` catalog size histograms, `#55631` frozen cloud skill
  snapshots, `#55847` retained-analytics bounds, and API-key analytics event
  eligibility (`can_send_with_api_key_auth`) despite the new FedRAMP URL
  plumbing. Items already verified as having no Go counterpart are listed
  above.

## Verification

- `gofmt` on all touched packages.
- Isolated worktree `D:\qax\reagent\dev\codex_go_wt\nontui_verify` (clean
  HEAD + non-TUI patch): `go build ./...` passes.
- `go test ./tool ./turn ./eventmap ./tcptunnel -count=1` passes.
- `go test ./appserver -run 'ThreadSample|ShellSnapshot|GranularApproval|PermissionPrompt' -count=1` passes.
- `go test ./app ./cli ./model ./config ./sandbox -count=1` passes.
- For `#57304`:
  `go test ./appserver -run 'WorkspaceRouting|Refresh|AuthStatus|Analytics|TurnError' -count=1`
  passes.
- For `#57286`: `go test ./config -count=1` and
  `go test ./appserver -run 'ManagedNetwork|NetworkRequirements|NetworkProxy|Permission|Granular|ConfigRequirements' -count=1`
  pass.
- For `#57287`: `go test ./appserver -run 'PredictionFork|IncrementalToolCatalog|ThreadSample' -count=1`,
  `go test ./features ./turn -count=1`, and the full `go test ./appserver -count=1`
  pass.
- For `#57270`: `go test ./app -run 'TestSandbox|TestRemoteExecServer|TestExecServerRemote|TestCLIHostPublishesApplicationNetworkPolicyLikeRust' -count=1`
  and `go build ./...` pass.
- For `#57192`: `go test ./execserver -count=1` and
  `go test ./appserver -run 'CapabilityDiscoveryV2' -count=1` pass.
- For `#56049`: `go test ./execserver ./tool -count=1` passes.
- For `#57187`: `go test ./config ./runtimeutil -run 'RolloutBudget|Budget' -count=1` passes.
- For `#56445`: `go test ./codexapi ./model ./rollout -count=1` and
  `go test ./appserver -run 'TurnError|Analytics|ErrorInfo|ModelNotFound' -count=1`
  pass.
- For `#56468`: `go test ./execserver -count=1` passes.
- For `#56022`: `go test ./cli -run 'PreservesConfigOverrides' -count=1` and
  `go test ./app -run 'MCP|Plugin|Cloud|CLI|Parse' -count=1` pass.
- For `#56552`/`#57225`/`#57226`: `go build ./...`,
  `go test ./config ./state ./cli -count=1` and
  `go test ./appserver -run 'NFSDeleteEnforcement|StateRuntime|MessageBoard|Sqlite|Corruption' -count=1`
  pass. `NFSSQLiteMigrationSupported`/`NFSDatabaseNeedsPermissionConfirmation`
  gate the conversion, resume and mode-bit tests to Unix, so they skip on this
  Windows host and still need a Linux/macOS run before relying on the cutover.
- For `#56495`: `go test ./agentboard -run 'SubscribedChannels' -count=1` and
  the full `go test ./agentboard -count=1` pass.
- For `#55690`: `go test ./plugin -run 'CleanupMarketplaceStaging' -count=1`
  and the full `go test ./plugin -count=1` pass (the symlink test skips on this
  Windows host because creating symlinks needs a privilege it lacks).
- For `#57251`: `go test ./appserver -run
  'TestRuntimeRouterGetAccountThreadUsageMatchesRust|TestAccountThreadUsagePreservesNativeDollarsLikeRust' -count=1`
  passes.
- For `#56553`: `go test ./appserver -run 'Hook|hooks' -count=1`,
  `go test ./appserver -run 'Schema|ProtocolSurface' -count=1` and
  `go test ./app -run 'Hook' -count=1` pass.
- For `#57186`: `go test ./sandbox/linuxsandbox -run 'InspectUnreadableGlobSearchRoot' -count=1`
  passes; the symlink-loop and symlink-root cases skip on this Windows host
  because creating symlinks needs a privilege it lacks.
- For `#55846`: `go test ./appserver -run 'GuardianReviewElapsedMS|Guardian.*Duration' -count=1`
  passes.
- For `#57212`: `go test ./codemode -run 'GrpcZstd|NegotiatesZstd' -count=1` and
  the full `go test ./codemode -count=1` pass.
- For `#56494`: `go test ./agentboard -run 'UpsertChannel|MessageBoardToolSpecsMatchRust' -count=1`
  and the full `go test ./agentboard -count=1` pass, as do
  `go test ./turn ./appserver -run 'MessageBoard|Board' -count=1`.
- For `#57198`/`#57203`: `go test ./tool -run 'TestShellExecutorHookToolCWDReportsExecutionDirectory' -count=1`
  and `go test ./appserver -run 'TestHookInputCWDReportsToolExecutionDirectoryLikeRust|Hook|Approval' -count=1`
  pass, as does the full `go test ./tool -count=1`.
- For `#57209`: `go test ./telemetry -count=1` and
  `go test ./appserver -run 'Analytics|TurnEvent|TurnProfile|TimingProfile' -count=1`
  pass.
- Plus, for `#57313`/`#56309`/`#57263`: `go test ./model ./sandbox -count=1` and
  `go test ./appserver -run 'Permission|Granular|Schema' -count=1` pass in the
  isolated worktree.
- For `#57220`: `go test ./appserver -run 'WebsocketZstdCodecPair|NextNativeWebsocket|ZstdStreamCodec|WebsocketCompression' -count=1`
  passes; the full `go test ./model -count=1` and `go build ./...` pass.
- For `#55958`: `go test ./model -run 'PreservesExplicitCatalog|InvalidatesExplicitCatalog|PersistsEmptyExplicitCatalog|ComposeGatewayAuth' -count=1`
  and `go test ./auth -run 'GatewayAuthManagerWithoutInteractiveLogin|GatewayAuthManagerRefresh|GatewayAuthManagerReauthorizes' -count=1`
  pass; the full `go test ./model -count=1` passes.
- For `#55973`/`#56461`: `go test ./agentboard -count=1` passes, as do
  `go test ./turn -run 'MessageBoard|Board' -count=1`,
  `go test ./appserver -run 'MessageBoard|Board|Append' -count=1`, and
  `go build ./...`.
- For `#55591`: `go test ./appserver -run 'GuardianReviewBlocking' -count=1`
  passes, as do `go test ./appserver -run 'Guardian' -count=1` and
  `go build ./...`.
- For `#57201`/`#57207`: `go test ./criticalpath ./tooltiming ./turn ./telemetry -count=1`,
  `go test ./appserver -run 'TestUserWaitTiming|TestGuardianReviewTimingUsesOwningCall|TestMCPElicitationInvalidates|TestNetworkApprovalTimingCallID|Guardian|Analytics|TurnEvent|TurnProfile|TimingProfile' -count=1`,
  `go test ./tool -count=1`, the full `go test ./appserver -count=1`, and
  `go build ./...` pass.
- For `#57178`: `go test ./model -run 'Fedramp|APIProviderAndAuth|ConfiguredProviderRoutesAPIKeyModelDiscovery' -count=1`,
  `go test ./config -run 'ChatGPTBackendURL|NewConfigFromRead' -count=1`,
  and `go test ./appserver -run 'RealtimeStartOptions|Analytics|Catalog|ImageGeneration|WebSearch|HistoryNotes' -count=1`
  pass, as do the full `go test ./auth ./model ./config ./doctor ./exec -count=1`
  runs and `go build ./...`.
- For `#56265`/`#56560`/`#57188`/`#57394`: `go test ./appserver -run GCP -count=1`,
  the full `go test ./appserver -count=1` (245.2s), `go test ./app -count=1`,
  and `go test ./model ./config ./auth -count=1` pass; `go build ./...` passes.
  `go vet ./appserver ./model ./config ./auth` reports only the pre-existing
  `appserver/thread_sample.go:220` context-leak warning.
- For `#57360`/`#57358`/`#57357`/`#57352`: `go test ./session ./rollout ./state ./app -count=1`
  passes, as do the focused writer-lock, session-index, and NFS-migration
  tests. The NFS test skips on this Windows host because
  `NFSSQLiteMigrationSupported` requires Unix. The full
  `go test ./appserver -count=1` (233.8s) passes after adding the `gcp_setup`
  row to the Rust v2 suite manifest; `go build ./...` and
  `go vet ./session ./rollout ./app` pass. The npm wrapper has no Go-side JS
  test in this repo.
- For `#57241`/`#55991`/`#56211`: `go test ./compact ./config ./memories ./turn ./exec -count=1`, `go test ./appserver -run 'GuardianNativeAgentMessage|GuardianReviewTranscript|GuardianTranscriptCarries|GuardianReviewDelivers|MemoryStatus|CompactTokenStatusForTurn' -count=1` and `go build ./...` pass.
- For `#57275`: `go test ./turn -run 'ImageGenerationUsageLimit|ImageGenerationPremium|ImageGenerationErrorOutput' -count=1` passes.
- For `#57272`: `go test ./codemode -count=1` and `go build ./...` pass; the proto was regenerated with the checked-in `protoc-gen-go`/`protoc-gen-go-grpc` plugins.
- For `#56033`/`#55701`/`#56163`: `go test ./features ./config ./app -count=1`, `go test ./exec -run 'ReasoningEffort' -count=1`, `go test ./appserver -run 'ReasoningEffort|OAuth|MCP' -count=1`, and `go build ./...` pass.
- For `#56371`: `go test ./config -run GuardianReviewTimeout -count=1` and `go test ./appserver -run 'GuardianReview(Timeout|Context|erUsesConfigured|erUnlimited)' -count=1` pass.
- For `#55581`: `go test ./appserver -run 'AsyncCompaction' -count=1` passes; reverting the fix makes the new test block.
- For `#55692`: `go test ./turn -count=1`, `go test ./appserver -run 'Incremental|ToolRegistration|AsyncCompaction' -count=1`, and `go build ./...` pass.
- For `#55971`: `go test ./agentboard -count=1`,
  `go test ./appserver -run 'MessageBoard|Board' -count=1`, and `go build ./...`
  pass.
- For `#55644`: `go test ./turn -run 'SteerMailbox' -count=1`,
  `go test ./tool -run 'ClockSleep|ClockHandlers' -count=1`,
  `go test ./appserver -run 'PendingInjectionInterruptsWaitAgent|LiveInjectionInterruptsWaitAgent|QueuedMailDoesNotSteerWaitAgent|MultiAgent' -count=1`,
  `go test ./exec -run 'WaitForActivity|AgentController|MultiAgent' -count=1`,
  the full `go test ./turn -count=1` and `go test ./tool -count=1`, and
  `go build ./...` pass.
- For `#55545`: `go test ./rollout -run 'CompressedRolloutReadDiagnostic|FirstFrameContentSize' -count=1`,
  `go test ./appserver -run 'FeedbackReadAttachment' -count=1`, the full
  `go test ./rollout -count=1`, and `go build ./...` pass.
- For `#57176`: `go test ./appserver -run 'Guardian|NodeRepl' -count=1`,
  `go test ./state -count=1`, and `go build ./...` pass.
- For `#57274`: `go test ./context -run 'NodeRepl|Guardian' -count=1`,
  `go test ./appserver -run 'Guardian|NodeRepl' -count=1`, and `go build ./...`
  pass.
- For `#57240`: `go test ./memories -count=1` and `go build ./...` pass.
- For `#56250`: `go test ./rollout -count=1`, `go test ./appserver -run 'Resume|Settings|Revert|Checkpoint' -count=1`, and `go build ./...` pass.
- For `#57185`: `go test ./tool -run 'OmissionMarker|HeadTailBuffer|HookResponse|UnifiedExec' -count=1` (126s) and `go build ./...` pass.
- For `#56414`/`#56462`: `go test ./config ./memorylock -count=1`,
  `go test ./memories -count=1` (22.4s), and
  `go test ./appserver -run 'ExternalAgent' -count=1` pass, including the
  busy-workspace import rejection and retry enqueuing one consolidation per
  namespace.
- For `#55987`: `go test ./execserver -run 'TestRegistry|TestExecutorCredentialRefreshAfterUnauthorized' -count=1`,
  `go test ./app -run 'ExecServer' -count=1`, `go test ./appserver -run 'ExternalAgent' -count=1`,
  and `go build ./execserver ./app` pass. A full `go test ./execserver -count=1`
  run also hits unrelated load-dependent flakes
  (`TestNoiseHandshakeUsesInitializeTimeoutLikeRust`,
  `TestProvisionedNoiseConnectWaitsForOfflineExecutorLikeRust`) and the
  pre-existing Windows failure `TestStdioProcessReadWaitsForOutput`, which also
  fails on the clean `HEAD` worktree.
- For `#57181`: `go test ./turn -run 'AdditionalContext' -count=1`,
  `go test ./appserver -run 'AdditionalContext|TurnSteer|TurnStart' -count=1`,
  `go test ./exec -run 'Agent|Steer|Wait' -count=1`, `go vet ./turn`, the full
  `go test ./appserver -count=1` (223.5s), and `go build ./...` pass.
- For `#57420`: `go test ./parity -run TestPrecomputedAppServerExportsMatchRustTarget -count=1` passes, and
  `go test ./appserver -run 'TestPrecomputedExportArtifactsMatchTargetRustCommit' -count=1` passes after
  re-anchoring the pinned hashes to the artifacts in the `eec8bde1e9` checkout.
- For `#57441`: `go test ./features -run 'GuardianNativeMultiAgentHistory|TestKnownFeature|TestSorted' -count=1` passes.
  `go test ./parity -run 'Feature' -count=1` reports only the pre-existing drift for
  `guardianv3`, `guardian_recorded_items`, `mcp_connection_sharing`, `runtime_agent_instances`
  and `skill_catalog_deltas`, which are unsynced upstream features noted above.
- For `#57425`/`#47947`: `go test ./appserver -run 'TestGuardianRoot' -count=1`,
  `go test ./appserver -run 'Guardian' -count=1`, `go test ./retainedctx ./context -count=1`,
  and the full `go test ./state -count=1` pass. The full `go test ./appserver -count=1`
  (313.6s) also passes. The regression test mirrors Rust's ID-less checkpoint: one
  capped user excerpt, both incomplete-evidence notices, and no duplicate legacy copy.
