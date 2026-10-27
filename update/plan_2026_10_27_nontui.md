# 2026-10-27 non-TUI sync window

Upstream window: `8196bcdc18..c587d6e54c`, extended by
`c587d6e54c..6fe77fc123`, then by `eec8bde1e9..6b3b67d71f`, then by
`6b3b67d71f..75667b341a`, and finally by `75667b341a..dddc3c0a79` (Rust
`codex` at `dddc3c0a79`; the newest non-TUI commits are `#57451`, `#57455`,
`#57452`, `#57462`, `#57458`, `#57468`, `#57474`, `#57476`, `#57478`, and
`#57483`).
This lane owns non-TUI core packages only. `tui/`, `app/agents_dashboard.go`,
`app/tui_telemetry.go` and `update/plan_2026_10_27.md` belong to the parallel
TUI lane and are not modified here.

## Implemented

| Upstream | Requirement | Go change |
| --- | --- | --- |
| `#56277` | Bound SigV4 request bodies: signing buffers at most the configured `max_body_bytes` (16 MiB cap) under a shared eight-slot buffer budget, fails closed when the body exceeds the limit or the read stalls for 30 seconds, and requires declared framing to match the buffered payload while leaving explicitly unsigned payloads unread. | `network/proxy_aws_sigv4.go`, like-Rust tests |
| `#56182` | AWS SigV4 signing in network proxy MITM hooks: an `aws_sigv4` hook action selects requests whose `Authorization` names the placeholder access key ID, then re-signs them after policy authorization, header actions, and brokered credential injection with credentials that stay in the proxy process. Selected requests with mismatched credential scopes, presigned URLs, unsupported streaming signatures, or ambiguous framing take the hook denial path; generated authorization and session-token headers stay sensitive, guest signing headers survive hop-by-hop removal, and a hook may select at most one signing action. | `network/proxy_aws_sigv4.go`, `network/proxy_mitm_hook.go`, `network/proxy_config_values.go`, `network/proxy_server.go`, like-Rust tests |
| `#57210` | Opt-in protocol discovery for local stdio MCP servers: the disabled-by-default `mcp_connection_sharing` feature makes an eligible local stdio server (not `codex_apps`, local environment, no runtime-managed credentials, no EMA auth, no explicit legacy override) probe `server/discover` before the legacy handshake. A helper that cannot finish discovery is recovered with a fresh handshake after the previous process exits; a working legacy connection is retained for the same helper when an upgrade fails for a non-authentication reason; authentication failures abort without a restart. Discover connections keep the bounded modern message framing and protocol metadata, and share the legacy catalog identity until they negotiate 2026-07-28. | `features/features.go`, `mcp/protocol_2026.go`, `mcp/config.go`, `mcp/api.go`, `mcp/stdio_client.go`, `mcp/protocol_discovery_metrics.go`, `mcp/stdio_discovery_like_rust_test.go` |
| `#57211` | Share eligible local stdio MCP connections across chats: the `mcp_connection_sharing` feature also pools local stdio helpers that negotiated MCP 2026-07-28, keyed by helper identity, environment, client capabilities, startup budget, and catalog limit. Later chats join a ready connection and still list their own catalog; input requests keep using the calling chat's callback. Legacy helpers and helpers that requested client input while starting stay private. Each chat releases its lease independently and the final release stops the helper. A forced refresh (runtime apply, server config change, form toggle, or `Refresh`) retires the pooled connection so new chats start a fresh helper without interrupting chats that still share the retired one. | `mcp/connection_pool.go`, `mcp/api.go`, `mcp/stdio_client.go`, `mcp/connection_sharing_like_rust_test.go` |
| `#57196` | Isolate MCP inputs and timeouts for callers sharing a connection: input requests carry globally unique ids across requests and connections, waiting for client input pauses the request's own timeout budget instead of consuming it, and a cancelled caller on a ready — possibly shared — connection sends `notifications/cancelled` for its own request rather than failing the transport. Tool calls, resource reads, catalog listings, and custom requests all go through the same request path, so cancellation and budget pausing apply uniformly. | `mcp/stdio_client.go`, `mcp/protocol_2026.go`, `mcp/connection_sharing_like_rust_test.go` |
| `#56278` | Negotiated network policy metadata for MCP calls: an optional `NetworkPolicyMetadataProvider` on the runtime supplies call-scoped network restrictions (enabled, mode, allowed and denied domains, hard-deny allowlist misses). The experimental `codex/network-policy-meta` client capability is offered only to a trusted ChatGPT origin when a provider is configured, and policy metadata is attached to a `tools/call` `_meta` only when the session that sends the attempt also advertises it. Caller-supplied policy metadata is always stripped, restrictions come from the call's captured permission profile and server environment, one policy snapshot is reused across an HTTP session-recovery retry, and a resolution failure fails the call before it is sent. Existing runtime callers leave the provider unset. | `mcp/client_capabilities.go`, `mcp/network_policy.go`, `mcp/protocol_2026.go`, `mcp/auth_changes.go`, `mcp/api.go`, `mcp/config.go`, `mcp/http_client.go`, `mcp/stdio_client.go`, `mcp/read_only_tools.go`, `mcp/network_policy_like_rust_test.go` |
| `#55607` | Auth header construction is fallible so a request is never sent with missing or partial credentials: the API auth provider, telemetry, and request application propagate errors; a provider that requires Codex auth reports a transient error when managed authentication is unavailable (catalog identity stays computable); transient MCP auth errors are retryable while auth build errors and policy denials are terminal. | `codexapi/auth.go`, `model/auth.go`, `model/models_identity.go`, `mcp/http_client.go`, like-Rust tests |
| `#55923` | Preserve thread model settings across resume and revert: a cold resume restores the thread-owned service tier, reasoning summary, and personality unless the caller overrides them; the selected tier is kept for persistence and settings snapshots while the effective tier is filtered against the model (flex is always preserved); `thread/revert` carries the runtime model, service tier, and personality into reconstruction; and the initial settings checkpoint is written before user input so an interrupted first turn retains it. | `rollout/rollout.go`, `rollout/session_items.go`, `model/catalog.go`, `appserver/router.go`, `appserver/runtime_router.go`, `appserver/turn_runtime.go`, `appserver/persisted_model_settings.go`, like-Rust tests |
| `#56330` | Interrupts and Plan mode stop reserved agent-message wakes: an interrupt during model discovery rejects the unstarted reservation without a terminal event, preserves its mail for an explicit turn, and blocks later automatic wakes until a new turn starts. Plan mode rejects the wake but does not mark the thread stopped. Accepted wakes keep their admission configuration, and finishing turns stay running and persist only `TurnComplete`. | `turn/api.go`, `appserver/turn_runtime.go`, `appserver/thread_manager.go`, `appserver/agent_controller.go`, `appserver/runtime_router.go`, `appserver/agent_message_wake_controls_like_rust_test.go` |
| `#56284` | Supported thread originators (`codex_work_cca`, `codex_work_web`, `codex_work_desktop`, `codex_work_mobile`, `chatgpt_cca`) select isolated model managers before thread model metadata is resolved. Each partition sends its own `originator` header, reuses its manager, keeps separate in-memory and `models_cache.<originator>.json` disk catalogs, and shares runtime API-key discovery policy; unsupported originators retain the default manager. Thread startup and actual turns use the selected catalog’s instructions and `apply_patch_tool_type`. | `model/models_originator.go`, `model/models_endpoint.go`, `model/lazy_models.go`, `model/api.go`, `model/catalog.go`, `appserver/runtime_router.go`, `appserver/turn_runtime.go`, `appserver/agent_runtime.go`, like-Rust tests |
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
| `#57439` | Guardian reviewer marker, routing-hint suppression and ChatGPT parent credits follow the OpenAI provider and auth instead of the base URL; header-based auth counts as Codex-backend auth, while API-key and other providers stay excluded | `model/responses_agent.go`, like-Rust request/provider tests |
| `#57448` | Native MAv2 calls (`spawn_agent`, `send_message`, `followup_task`, `interrupt_agent`) in the configured namespace stay native in Guardian history, with a paired evidence-only output in delivery and budgeting and whole-item omission past the cap | `appserver/guardian_native_multi_agent_call.go`, `appserver/guardian_reviewer.go`, `state/guardian_profile.go`, `state/guardian_transcript.go`, `state/guardian.go`, like-Rust tests |
| `#57441` | Feature `guardian_native_multi_agent_history` (under development, off) for native MAv2 calls in Guardian history | `features/features.go`, registration test |
| `#57433` | Opt-in synchronous Guardian review for native tool calls: the proposed call is delivered as native evidence (encrypted replay preserved unless hooks rewrite arguments), deduplicated from transcript history, capped with its paired evidence output, and reviewed without action-specific client events; denial, timeout or unavailability stops dispatch before the handler runs | `state/guardian.go`, `state/guardian_action_prompt.go`, `state/guardian_transcript.go`, `model/catalog.go`, `tool/registry.go`, `tool/hooks.go`, `tool/code_mode_exec.go`, `appserver/guardian_native_tool_review.go`, `appserver/guardian_reviewer.go`, `appserver/runtime_router.go`, like-Rust tests |
| `#57430` | A user-origin goal set after a worker's first Guardian review reaches that worker's later review root authorization as `User set the goal: …`; an automatic-origin goal never does, and the earlier review stays unchanged | `appserver/retained_context.go` (raw-only message text), `appserver/guardian_goal_provenance_like_rust_test.go`, `appserver/retained_context_test.go` |
| `#57425` | Guardian root request preparation is centralized: a 16-message shared cap, legacy-checkpoint copies deduplicated after truncation, verified-answer and incomplete-context notices, and all user restrictions preserved during handoff filtering | `appserver/guardian_root_context.go`, `appserver/agent_runtime.go`, `appserver/guardian_root_handoff.go`, `appserver/retained_context.go`, `state/guardian_retained_context.go`, `context/user_goal.go`, like-Rust tests |
| `#57420` | Thread item `started_at_ms`/`completed_at_ms` are optional nullable in the TypeScript exports so older servers can omit them | `appserver/schema/precomputed/app-server-exports-stable.json.zst`, `appserver/schema/precomputed/app-server-exports-experimental.json.zst`, pinned SHA-256 values re-anchored in `appserver/schema_test.go` |
| `#57451` | With `guardian_native_multi_agent_history` enabled, wrapped MAv2 `spawn_agent`, `send_message`, `followup_task`, and `interrupt_agent` calls require synchronous Guardian review before dispatch (read-only `wait_agent`/`list_agents` stay unreviewed); the async V2 scorer arm is N/A because Go has no asynchronous scorer | `tool/guardian_scope.go`, `agent/tools.go`, `agent/tools_v2.go`, `turn/tools.go`, `appserver/runtime_router.go`, `appserver/turn_runtime.go`, like-Rust tests |
| `#57455` | Multi-agent Guardian classification is centralized in `IsStateChangingMultiAgentTool`: an explicit configured namespace must match exactly, while an absent namespace accepts the default `""`/`functions` spellings; both call sites keep their own enablement gate | `tool/guardian_scope.go`, `tool/guardian_scope_test.go`, `agent/tools_v2.go` |
| `#57452` | `policy_capacity_unavailable` is a distinct terminal capacity failure with the display `Policy-restricted capacity is unavailable. Please try again later.`; HTTP 503, `response.failed`, and WebSocket capacity events preserve the classification and `Retry-After`, stream retries and Guardian retries follow that advice, telemetry reports `policy-restricted capacity unavailable`, analytics map it to `policyCapacityUnavailable`/`policy_capacity_unavailable` with no HTTP status, and rollout decoding preserves both spellings | `codexapi/error.go`, `codexapi/response_debug.go`, `model/responses_agent.go`, `model/responses_stream.go`, `appserver/guardian_reviewer.go`, `appserver/turn_runtime.go`, `rollout/codex_error_info.go`, re-vendored precomputed exports, like-Rust tests |
| `#57462` | Code Mode always renders the tool description before the namespace description, then the declaration suffix; `code_mode_tool_description_first` is a removed compatibility flag and its config/plumbing are deleted | `features/features.go`, `config/config.go`, `tool/code_mode_exec.go`, `turn/tools.go`, `exec/exec.go`, `appserver/runtime_router.go`, updated ordering tests |
| `#57476` | Guardian reviewer session helpers: basic-source detection (Go's existing `guardianTurnStart`) and a parent-thread-scoped Responses prompt cache key `guardian:{threadID}` on review requests, matching Rust's `prompt_cache_key_override_for_review_session` (guardian source only, parent required, overrides the fork cache key) | `appserver/guardian_session.go`, `appserver/guardian_reviewer.go`, `appserver/guardian_session_like_rust_test.go` |
| `#56459` | Feature `guardianv3` (under development, off by default) accepts a boolean or a table with only `enabled`; strict config rejects unknown table fields | `features/features.go`, `config/config.go`, `features/features_test.go`, `config/config_test.go` |
| `#56460` | Guardian's durable evidence board helpers: shared `guardian` storage namespace and a read-only-capable board for the owning main thread | `agentboard/guardian.go`, `agentboard/guardian_test.go` |
| `#55633` | Code Mode collaboration calls (`spawn_agent`, `send_message`, `followup_task`) are classified as plaintext, so their message receives the plaintext wrapper with sender and recipient attribution; ordinary direct calls stay encrypted | `agent/tools.go`, `agent/tools_test.go` |
| `#57483` | Selected-thread rollout migration reads the state index first and skips rollout headers whose indexed owner and canonical filename agree that they belong to an unselected thread; selected, misnamed, conflicting and unindexed paths still get a header inspection, and a missing/busy database falls back to inspecting everything | `state/rollout_migration.go`, `rollout/rollout.go`, `rollout/migrate.go`, `app/app.go`, like-Rust tests |
| `#56297` | Built-in dummy credentials carry the provider prefix, a visible `masked` marker and over 128 random alphanumeric bytes after it, copying no punctuation from the real credential; candidate dummies that duplicate or embed an already-issued credential or dummy are rejected instead of being registered | `network/proxy_credential_broker.go`, like-Rust tests |
| `#56266` | Managed-requirements denials keep their reason across runtime overlays unless a trusted host-owned Apps registration replaces the server; an explicit disable that requirements also deny stays combined through repeated filtering; standalone MCP login HTTP clients reject requirements-denied servers | `mcp/catalog_priority.go`, `mcp/config.go`, `mcp/api.go`, `config/mcp_requirements.go`, like-Rust tests |
| `#55978` | `logs_2.sqlite` moves to an explicitly configured `log_dir` (user config or managed requirements) unless a managed `sqlite_home` requirement pins the database home; other databases stay in `sqlite_home` and an unset `log_dir` keeps the old location. The logs directory is created only when its database opens, so an unavailable `log_dir` never blocks login/config reads. Existing logs stay in place: feedback falls back to them as `codex-logs-legacy.log`, and retention pruning plus page reclamation cover both locations with independent maintenance locks and NFS probes | `config/config.go`, `state/sqlite.go`, `state/logs.go`, `state/logs_maintenance.go`, `state/reclamation.go`, `appserver/websocket.go`, `appserver/runtime_router.go`, like-Rust tests |
| `#55856` | Managed app-server and updater stderr logs go through one bounded, nonblocking worker (64 queued records, 16 KiB per record, lossy); a live log above 1 MiB keeps its latest 256 KiB in `.log.previous` and is truncated in place with append-only handles, and updater handoff pauses predecessor pruning until ownership is restored | `appserverdaemon/stderr_log.go`, `appserverdaemon/stderr_append_unix.go`, `appserverdaemon/stderr_append_windows.go`, `appserverdaemon/daemon.go`, `appserverdaemon/migration.go`, `appserverdaemon/update_loop.go`, `appserverdaemon/pid_shutdown_windows.go`, `app/app.go`, like-Rust tests |
| `#55498` | Noise relay handshakes emit correlated, credential-free diagnostics: executor rejections and pending-validation resets carry a classified reason and registry HTTP status in a normalized UUID stream id, capped at 100 per minute per relay connection plus one suppression marker; harness failures and completions add elapsed time and connection identifiers, WebSocket read failures are classified from typed errors, and valid stream UUIDs are forwarded to key validation as `x-codex-noise-stream-id` | `execserver/remote_relay.go`, `execserver/remote_harness.go`, like-Rust tests |
| `#56127` | EMA MCP auth status inspection is owner-checked and never waits for account refresh; one lazy read of the account-scoped IdP grant is shared across admitted EMA servers, and a missing grant or read error is cached so every server reports the same state without repeating storage work | `mcp/ema_runtime.go`, like-Rust tests |
| `#56081` | Decrypted local secret snapshots are cached for every namespace: managed, Codex auth and MCP OAuth keep at most one snapshot process-wide (gateway snapshots stay with their backend), reuse requires the same file path, ciphertext hash and keyring passphrase hash, concurrent misses decrypt once, writes invalidate a matching snapshot, and every read still inspects the file and keyring | `safety/secrets.go`, like-Rust tests |
| `#55546` | Scoped account OAuth grant leases: `LeaseAccountOAuthGrant` shares one per-home refresh gate with `RefreshChatGPTTokens`, so an identity-provider exchange and a refresh cannot rotate the grant concurrently; release returns a grant that rechecks the cached login, the login generation and the persisted OAuth provenance, complete account identity and refresh token. An unavailable keyring in Auto mode is an error for grant reads, never an older file record. `login_generation` advances separately from `owner_generation`: losing login metadata revokes the login while preserving the same-account network policy, a new login to the same account advances both, and EMA managers capture the revision and revoke on any movement | `auth/account_grant.go`, `auth/change_state.go`, `mcp/ema_runtime.go`, like-Rust tests |
| `#55646` | Auth persistence and reloads are guarded against concurrent login changes: one process-wide commit lock covers storage load/save/delete, `Resolve`, refresh persistence, logout and agent-identity writes; refreshed tokens (and agent identity records) are written only when the stored login still matches the requesting one, while older clients may drop login metadata when the refresh token matches; reload results are published only when the credential generation is unchanged, and logout invalidates pending reads even with an empty cache before republishing the store. Ordinary auth loading keeps the Auto file fallback; account-grant reads stay strict | `auth/auth.go`, `auth/oauth.go`, `auth/agent_identity_managed.go`, `appserver/runtime_router.go`, `appserver/auth_status.go`, `appserver/agent_runtime.go`, like-Rust tests |

Grounding notes:

- For `#55546`/`#55646`, Go has no `AuthManager` object: refresh and storage are
  free functions over `auth.Store`, while the cached credential and revisions
  live in `auth.AccountManager`/`auth.AuthChangeTracker` and are published by
  `RuntimeRouter`. The lease/reload guard is therefore implemented at those
  boundaries. The lease takes the caller's cached snapshot and login-generation
  callback (the app-server supplies both), which is the Go equivalent of
  reading `AuthManager.inner` and `auth_change_state_tx`. Rust's
  `validate_auth_restrictions` on the cached grant has no separate manager-level
  check in Go because the cached snapshot is produced by
  `resolveAuthWithLoginRestrictions` or admitted through
  `validateLoginAccountParams`; forced workspace/login-method restrictions are
  enforced there. Go's EMA authorization uses an account-scoped IdP grant
  (`ema-idp:` credentials) rather than exchanging the account OAuth token, so
  the lease has no EMA consumer, but the login-generation revocation applies to
  EMA exactly as upstream.
- For `#56266`, Rust's `RequirementSource` is represented by Go's fixed
  disabled-reason strings (requirements vs explicit-and-requirements); Go does
  not carry a separate source value, so retaining the reason string is the
  faithful equivalent.
- `#55978` route the logs database through `SqliteConfig.LogsHome` and keep
  the legacy read path: Go's `log_dir` is a plain path, and Rust's Unix-only
  `LocalLogsDir` shared cleanup lock has no Go equivalent because `MkdirAll` is
  idempotent and no directory-cleanup guard is used here. Legacy maintenance
  opens the old database in `mode=rw` with no busy timeout, matching Rust's
  `open_legacy_logs_for_cleanup`, while the feedback fallback stays read-only.
  `doctor` path reporting for a configured logs home stays with the TUI lane.
- `#57488` is N/A: Rust moves Guardian prompt composition from core into the
  `codex-guardian-reviewer` extension crate. Go has no crate boundary or
  extension partition for the reviewer (its prompt composition already lives
  beside the reviewer), so the refactor changes no behavior to port.
- `#55856`'s bounded stderr worker has no Rust `WorkerGuard`: Go keeps the
  writer for the process lifetime and tests stop it explicitly. The Windows
  append handle is reopened with `ReOpenFile` (append-only access) and installed
  with `SetStdHandle`, matching Rust, and `os.Stderr` follows it.
- `#55498` uses `slog` records instead of Rust's separately parented tracing
  spans: Go has no span exporter here, and the diagnostic attributes are the
  same (`noise_event`, `noise_outcome`, `noise_side`, `noise_reason`,
  `registry_status`, `environment_id`, `executor_registration_id`, `stream_id`,
  `elapsed_ms`). Go's handshake is inline inside `dialNoiseHarnessConnection`,
  so the initialization timeout is observed when the bounded read fails rather
  than by a separate readiness channel; the reason is still `timeout` with the
  elapsed time and identifiers of the failed stream. Registry failures are
  classified from Go's typed `remoteRegistryHTTPError`
  plus error categories produced by its own registry client, and only the
  status code (never response text) is reported. The validator error's raw text
  and reset reasons are never logged.
- `#56127` has no Go account-grant lease or refresh permit to avoid: Go's EMA
  status path already reads the stored IdP grant directly (no network exchange
  and no refresh coordination), so the Rust change's blocking-lease problem does
  not exist here. The portable parts are implemented — `CredentialsAvailable`
  now re-checks the captured account owner, and
  `AttachEnterpriseMCPAuthorization` shares one lazy read per (store mode,
  credential name, issuer) across admitted EMA servers, caching success,
  missing-grant and read-error results alike.
- `#56081` targets Rust's `codex-secrets` crate, and Go's `safety` local backend
  is the equivalent (no decrypted cache existed before, so the snapshot cache is
  introduced here rather than extended). Go's `mcp` OAuth and `auth` gateway
  stores write the same namespaces directly; they keep their own storage paths,
  and the `safety` namespace caches cover every consumer of the local secret
  backend. Cached plaintext is cloned before being returned, matching Rust's
  `Arc` clone, so a caller saving a mutated snapshot cannot corrupt the cache.
- `#55705` is structurally N/A: Rust moves cloud skill contents out of a
  per-generation `Mutex<CloudResourceCache>` into `SkillCatalogState`, so the
  catalog and its contents share one lock. Go has no separate cloud skill
  catalog state; the orchestrator skill cache is keyed by thread, MCP service
  identity and `ResourceCacheGeneration()` (`#46033`) and its contents live in
  that generation's cache entry. Auth changes, config mutations and plugin
  refreshes call `InvalidateResourceCaches`, which rotates the generation and
  therefore exposes a new empty contents cache, while a late read only writes
  into its own replaced entry. Go's cloud plugin catalog path likewise computes
  one cache key before discovery and reuses that exact key when publishing
  (discarding a result whose auth identity changed mid-flight), so Rust's second
  fix — using the key captured by `prepare_cloud_generation` — has no mismatch
  to correct. A regression test now pins the scope-change clearing and the
  successor-generation isolation.
- `#56124`'s reviewer-event forwarding is structurally N/A: Rust installs a
  parent observer on a private Guardian review *session* and forwards its
  `RawResponseItem`/`TurnAborted` events under the reviewer's own thread and
  turn IDs. Go's Guardian reviewer is an inline single Responses request inside
  the parent turn; it creates no reviewer session or turn, emits no private
  event stream, and has no separate reviewer identity to preserve. Emitting the
  reviewer's response items on the parent identity would corrupt the parent's
  raw-event stream, so the `guardian_recorded_items` feature key is registered
  for configuration parity only.
- `#55723` is N/A: it only avoids one clone per recipient in Rust's
  `OutgoingMessageSender` fan-out (`repeat_n`); Go's notification fan-out has
  no per-recipient payload clone to remove, so delivery behavior is unchanged.
- `#56514` is N/A: Rust instruments the spawned tool-dispatch task and its
  readiness/lock waits with `tracing` spans. Go's tool tracing is a flat
  event/attribute sink (`UnifiedExecSpanSink`, trace events) with no span
  nesting or `Instrument`-style propagation, so there is no dispatch span to
  extend; every dispatch event already records the turn and tool attributes.
- `#55627` is N/A: Go has no sibling-rollout prefetch cache
  (`prefetch_sibling_rollout_paths`/`collect_sibling_rollout_ids`), so there is
  no candidate slot to reserve for the first ancestor.
- `#56483` is N/A: Go fetches one installed-plugin snapshot per reconciliation
  with display assets included by default and materializes bundles from it;
  there is no runtime-vs-display snapshot split or enrichment state in the
  cache, and tool suggestions read the installed plugin store instead of that
  snapshot.
- `#55719` needs no code change: Go never rejected commands when a protected
  brokered shell snapshot was missing. `execserver` marks such a launch
  unsupported for masking (`credentialMaskingSupportedShell`), restores the
  unmasked credentials (`RestoreUnbrokeredEnvironment`) and continues, and a
  snapshot creation failure restores credentials and injects the
  `Credential masking is unavailable …` warning on stderr
  (`execserver/shell_snapshot.go`). The Rust commit removes a rejection path
  that Go never had; the Go warning text stays its own wording.
- Rust-test-only commits in this window are N/A: `#56247` (Rust test-module
  consolidation), `#56236`, `#56233`, `#56220`, `#56210`, `#56139`, and
  `#56274` (Rust test stabilization/configuration).
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
- `#57435` and `#57438` are structurally N/A: they split Rust's monolithic
  `ExecCommandHandler` into `ParsedExecCommand`/`PreparedExecCommand` and then
  carry that prepared value through the process manager. Go already has the
  equivalent split in reusable `BuildShellRequest`/`ShellRequest`, which carries
  the resolved command, shell, environment, permissions and approval fields
  through `ShellExecutor` and `UnifiedExecManager`; there is no behavior change
  or duplicated request struct to remove.

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
- `#57458` is structurally N/A: Rust moves `token_usage_delta` from
  `core/src/guardian/review_session.rs` into `codex-guardian-reviewer` without
  changing its clamped-to-zero semantics. Go does not accumulate a review delta;
  `appserver/guardian_review_metrics.go` reads the reviewer response's
  `AgentUsage` directly and drops negative samples through `nonNegative`, so
  there is no helper to relocate.
- `#57468` is N/A: Rust test-only mock fixes in
  `core/tests/suite/guardian_root_handoff.rs`; no production behavior changes.
- `#57452`'s `codex-rs/cli/src/doctor.rs` arm is N/A: Rust's
  `websocket_error_detail(&ApiError)` match lists `PolicyCapacityUnavailable` so
  it joins the generic `handshake error: {err}` arm. Go's doctor probe formats
  handshake failures from the HTTP response or transport error
  (`websocketHandshakeErrorDetail`) and has no per-`APIErrorKind` match to
  extend; a policy failure therefore already reports the generic handshake
  detail.
- The asynchronous Guardian V2 scorer arm of `#57451`/`#57455` is N/A: Go runs
  only the synchronous `modelGuardianReviewer`, so it has no
  `GuardianV2Config`/`async_scorer` observation path or score cache to classify.
  The synchronous review gate and the shared classifier are implemented.
- `#57476`'s crate relocation is N/A: Go has no `codex-guardian-reviewer`
  crate, so the reviewer name and basic-session detection already live in
  `appserver` (`guardianTurnStart` covers both the guardian originator and the
  `x-openai-subagent: guardian` metadata used by Go's in-process reviewer). The
  move's one live behavior that Go lacked is the prompt cache key: review
  requests previously omitted `prompt_cache_key` entirely. They now send
  `guardian:{threadID}` through `guardianPromptCacheKeyOverride`, the exact
  Rust format, so the reviewer conversation is cached per reviewed parent
  thread and cannot collide with that thread's own cache key. Go's shared
  in-process reviewer prewarm has no parent-thread scope, so only review
  requests carry the override.
- `#57478` is N/A: Go's Guardian approval request is already centralized as
  `state.Action`/`state.ActionAnnotations` with the appserver action builders
  (`commandApprovalAction`, `applyPatchApprovalAction`,
  `nativeToolGuardianAction`). `EnvironmentID` travels on the action, and
  credential-bearing fields (`Arguments`, `NativeCall`, permission payloads)
  are excluded from JSON and never formatted into logs, so there is no
  reviewer-crate boundary, crate-local environment lookup, or credential-safe
  `Debug` implementation to relocate.
- `#57474` is a Rust test-only regression over behavior already ported by
  `#57448`. The Go equivalent in
  `appserver/guardian_native_history_incremental_like_rust_test.go` drives two
  incremental reviews for the default and custom namespaces, asserting the
  historical encrypted `send_message` call and the encrypted parent reply appear
  exactly once and in order with their ID, namespace, ciphertext and
  `encrypted_function_args`, the paired evidence-only output sits immediately
  after the call, and the second review reuses the first response ID while
  re-sending the complete history plus the new entry. Go has no reviewer
  transcript-delta section, so the second request rebuilds the full transcript
  rather than appending a `TRANSCRIPT DELTA` block.
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

- For `#56284`: `go test ./model -count=1` (25.2s) and
  `go test ./appserver -run 'Originator|ThreadStart|TurnStart|ApplyPatch|Tool' -count=1`
  (30.8s) pass. Coverage pins originator-header routing, concurrent catalog
  isolation, manager reuse, separate disk cache files across restarts, shared
  API-key discovery policy, unsupported-caller fallback, originator-specific
  thread-start instructions, and Work/Chat/unsupported/repeated-Work turn
  instructions plus apply_patch declarations.
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
- For `#57439`: `go test ./model -count=1` (31.1s), `go test ./appserver -run 'Guardian' -count=1`,
  and `go test ./codexapi -count=1` pass. Coverage mirrors Rust's new cases: custom `/v1`
  and `/api/codex` URLs keep the reviewer marker, ChatGPT auth requests parent credits,
  and API-key, header-based, and non-OpenAI providers stay excluded.
- For `#57448`: `go test ./appserver -run 'NativeMultiAgent|GuardianNative|Guardian' -count=1`,
  `go test ./state -count=1` (65.1s), and the non-TUI build pass. Coverage mirrors Rust's
  native-history tests: ordering with ciphertext arguments, feature/namespace/MAv2-name
  gating, the `functions` default, whole-item omission, and paired-output budgeting.
- For `#57441`: `go test ./features -run 'GuardianNativeMultiAgentHistory|TestKnownFeature|TestSorted' -count=1` passes.
  `go test ./parity -run 'Feature' -count=1` reports only the pre-existing drift for
  `guardianv3`, `guardian_recorded_items`, `mcp_connection_sharing`, `runtime_agent_instances`
  and `skill_catalog_deltas`, which are unsynced upstream features noted above.
- For `#57425`/`#47947`: `go test ./appserver -run 'TestGuardianRoot' -count=1`,
  `go test ./appserver -run 'Guardian' -count=1`, `go test ./retainedctx ./context -count=1`,
  and the full `go test ./state -count=1` pass. The full `go test ./appserver -count=1`
  (313.6s) also passes. The regression test mirrors Rust's ID-less checkpoint: one
  capped user excerpt, both incomplete-evidence notices, and no duplicate legacy copy.
- For `#57433`: `go test ./state ./tool -count=1`, `go test ./appserver -count=1`,
  `go test ./model -run TestModelInfoGuardianReviewPolicy -count=1`, and the non-TUI build pass.
  Native review keeps accounting and metrics but emits no client assessment event; a denied review stops
  dispatch before `onStarted` or the handler, a hook rewrite is reviewed and clears encrypted replay content,
  and the proposed call is omitted from the transcript while other evidence keeps its cursor position.
- For `#57430`: `go test ./appserver -run 'TestGuardianRootAuthorizationIncludesLaterUserGoalLikeRust|TestRuntimeRouterRetainsRawOnlyInstructionLikeRust' -count=1`,
  `go test ./appserver -run 'Retained|Guardian|Goal' -count=1`, and the non-TUI build pass. Upstream's Rust
  commit is test-only. Porting it exposed that raw-only user messages (persisted goal instructions) were missing
  from retained evidence, so `retainedRecordForSessionItem` now derives text from the message content and falls
  back to the item preview for legacy items.
- For `#57451`/`#57455`: `go test ./tool -run TestIsStateChangingMultiAgentToolLikeRust -count=1`,
  `go test ./agent -run 'TestMultiAgentV2GuardianReview' -count=1`, and `go test ./turn ./tool -count=1`
  pass. Coverage pins the four state-changing tools, the read-only exclusions, the default/explicit namespace
  rules, feature-off suppression, and a denied router review stopping `team.send_message` before dispatch.
- For `#57452`: `go test ./codexapi ./model ./rollout -count=1` and
  `go test ./appserver -run 'Guardian|Policy|Analytics' -count=1` pass. Coverage mirrors Rust's HTTP/SSE/WebSocket cases
  (policy vs overload attribution with and without `Retry-After`), the exact display and telemetry strings,
  the retry-advice and Guardian retry tables, the analytics mapping without an HTTP status, and both rollout
  spellings. The precomputed schema hashes were re-anchored to the `6a6e2fb1f0` artifacts.
- For `#57462`: `go test ./tool -run 'TestCodeModeToolDescriptionOrderLikeRust' -count=1`,
  `go test ./turn -run 'TestBuildToolRegistryOrdersCodeModeToolDescriptionsLikeRust|TestBuildToolRegistryAppliesIndirectDescriptionPrefixesLikeRust' -count=1`,
  and `go test ./features -run 'CodeModeToolDescriptionFirst|TestSorted|TestKnown' -count=1` pass.
- For `#57474`: `go test ./appserver -run TestGuardianNativeEncryptedHistoryAcrossReviewsLikeRust -count=1`,
  `go test ./appserver -run 'GuardianNative|NativeMultiAgent|Guardian' -count=1`, and `go test ./state -count=1`
  pass. This also re-verifies the `#57448` transcript rules for both namespace spellings.
- For `#55633`: `go test ./agent -run 'TestMultiAgentV2ToolsPropagatePlaintextMessageSource' -count=1`
  passes for the direct-plaintext, Code Mode and ordinary-direct sources.
- For `#57483`: `go test ./rollout -run 'TestMigrateRollouts' -count=1`,
  `go test ./state -run 'TestReadThreadRolloutPaths' -count=1` and
  `go vet ./state ./rollout ./app ./agent` pass. Coverage mirrors Rust's
  selected-thread pruning: unselected indexed headers are skipped, compressed
  index paths prune the plain file, and misnamed, conflicting and unindexed
  paths still defer to the header; the state test proves the index read never
  creates the database.
- For `#56297`: `go test ./network -count=1` passes (8.4s). Coverage pins the
  `{prefix}masked{22+ alnum}` shape for projected/short/classic OpenAI keys and
  both GitHub token families, restoration of the real value, and rejection of a
  candidate that duplicates an already-issued dummy. The rejection path also
  fixed a latent bug: a rejected first candidate used to be returned after the
  attempt loop instead of leaving the broker without a dummy.
- For `#56266`: `go test ./mcp ./config -count=1` (mcp 15.7s),
  `go vet ./mcp ./config` and `go build ./appserver ./app ./agent` pass.
  Coverage pins repeated requirements filtering (requirements-only stays
  requirements, explicit becomes combined and stays combined), explicit and
  requirements denials surviving overlay transport/source replacement,
  host-owned Apps bypassing a previous requirements denial while a later custom
  replacement stays blocked, catalog requirements state carried across builds,
  and `OauthLogin` rejecting both requirements reasons.
- For `#56459`/`#56460`: `go test ./config ./features ./agentboard -count=1` and
  `go vet ./config ./features ./agentboard` pass. The config test covers the
  boolean and table forms plus profile override and unknown-field rejection;
  the board test pins the `guardian` namespace and the reviewer read-only view.
- For `#57476`: `go test ./appserver -run 'TestGuardianPromptCacheKeyScopedToParentThreadLikeRust|TestGuardianReviewRequestUsesParentScopedPromptCacheKeyLikeRust' -count=1`,
  `go test ./appserver -run 'Guardian|NativeMultiAgent' -count=1`, and
  `go build ./appserver ./model ./tool ./agent ./turn ./features ./codexapi ./rollout` pass.
  The helper test mirrors Rust's `session_tests.rs` (parent scoping, 64-byte
  limit, stable and distinct parents, non-guardian source, missing parent).
- For `#55978`: `go test ./state -count=1` (59.2s),
  `go test ./config -count=1`, `go test ./appserver -run 'Feedback|StateRuntime|LogDB|SQLite|NFS|Logs' -count=1`,
  `go test ./app -run 'NFS|Migrate|SQLite' -count=1`, `go vet ./state ./config` and
  `go build ./app ./appserver ./agent ./config ./state` pass. Coverage pins
  logs-only routing with the legacy fingerprint, lazy directory creation and an
  unusable `log_dir`, legacy feedback fallback plus its `codex-logs-legacy.log`
  attachment, legacy retention pruning that never creates a missing database,
  per-directory reclamation locks, app-server routing from `log_dir`, and the
  managed `sqlite_home` override.
- For `#55856`: `go test ./appserverdaemon -count=1`, `go vet ./appserverdaemon ./app`,
  `go build ./appserverdaemon ./app`, and `GOOS=linux`/`GOOS=darwin`
  `go build ./appserverdaemon` pass. Coverage pins oversized-record and
  full-queue drops, the 1 MiB prune with a 256 KiB `.previous` tail, skips for
  small/missing/nonregular files, the handoff pause with ownership restoration,
  live worker pruning, stale-temp cleanup, and the unmanaged stderr fallback.
- For `#55498`: `go test ./execserver -count=1` (15.4s) and
  `go vet ./execserver` pass. Coverage pins the 100-per-window slot arithmetic
  including the single suppression marker and window reset, UUID normalization
  (uppercase input, non-UUID input becomes `invalid`), the `reset_received`
  outcome, registry/network/timeout/protocol error classification, typed
  WebSocket read classification, the optional `x-codex-noise-stream-id` header
  (uppercase UUID forwarded lowercased, non-UUID and empty values omitted), an
  end-to-end malformed handshake plus HTTP 503 plus pending-validation reset
  with environment/registration correlation and no authorization, registry
  response or reset-reason leakage, and the harness failure/completion
  diagnostics including elapsed time, identifiers and stream UUID.
- For `#56127`: `go test ./mcp -count=1` (15.5s), `go vet ./mcp`, and
  `go build ./appserver ./app` pass. Coverage pins one storage read for two
  servers resolving the same grant (distinct stores still read separately), a
  missing grant cached across repeated inspections, both EMA servers receiving
  the configured credential status and request applier, owner changes no longer
  reporting the previous account's grant, and a logged-out account reporting no
  grant.
- For `#56081`: `go test ./safety -count=1` (19.3s), `go test ./auth -run Gateway -count=1`,
  and `go vet ./safety` pass. Coverage pins one decryption for concurrent reads
  through two backends in each process-wide namespace, a backend-scoped gateway
  snapshot, invalidation on writes, external ciphertext replacement, file
  removal, invalid ciphertext, key rotation, a keyring failure never serving
  cached plaintext, one snapshot per namespace across homes, and namespace
  isolation.
- For `#55705`: `go test ./appserver -run 'OrchestratorSkill' -count=1` passes.
  The regression test pins that an auth/config resource-generation change yields
  an empty contents cache and that a late read holding the replaced generation
  cannot populate its successor.
- For `#55546`: `go test ./auth -run 'AccountOAuthGrant|AuthChangeTracker|SameAuthOwner' -count=1`
  and `go test ./mcp -run 'EMA' -count=1` pass. Coverage pins the grant's
  required OAuth provenance/login owner/account identity/refresh token, cached
  owner and mode mismatches, refresh exclusion blocking the authority until
  `Release`, invalidation after token rotation, persisted login replacement,
  login-metadata loss and logout while a lease is held, plus login generation
  advancing separately from ownership and EMA revocation when it moves.
- For `#55646`: `go test ./auth -count=1` (11.1s), `go test ./mcp -count=1`
  (15.5s), `go test ./appserver -count=1` (141.1s), `go test ./model
  ./remotecontrol -count=1` and `go test ./app -run 'Login|Logout|Auth' -count=1`
  pass. Coverage pins the strict Auto grant read versus the ordinary file
  fallback, refresh persistence rejecting a concurrent login replacement while
  accepting legacy metadata loss with a matching refresh token, rotated tokens
  moving from a fallback file to a recovered keyring, guarded agent-identity
  writes, stale reload rejection after a login change, logout invalidation with
  an empty cache, and a fresh reload publishing login-generation-only changes.
- Broader non-TUI verification: `go build ./execserver ./appserver ./config ./memories ./memorylock ./state ./exec ./auth ./model ./context ./retainedctx ./features ./turn ./tool`
  and `go test ./tool ./turn ./features ./agent ./state ./model ./codexapi ./rollout -count=1` pass.
- For `#56330`: `go build ./appserver/... ./turn/... ./app/...`,
  `go test ./turn ./model ./app -count=1`, and
  `go test ./appserver -run 'AgentMessage|AgentMailbox|QueueOnly|HostMailbox|IdleLifecycle|Originator|TurnStart|Interrupt|WakeReservation|FinishingTurn|AutomaticWake' -count=1`
  pass. The full `go test ./appserver -count=1` fails only in the pre-existing
  Windows baseline tests
  `TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust`,
  `TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust`, and
  `TestRuntimeRouterFileChangeAnalyticsIncludesUserReviewSummaryLikeRust`,
  which also fail at clean HEAD `e7a4e94d`; `go vet ./appserver` reports only the
  pre-existing `thread_sample.go:220` cancel leak. Coverage pins interrupt
  rejection of an unstarted wake reservation with queued-mail preservation and
  no terminal notification, Plan-mode rejection without a stopped status,
  admission-configuration reuse, explicit-turn consumption with the feature
  disabled or a stopped reservation, finishing-turn interrupt protection with
  exactly one persisted `task_complete` under both feature settings, mail
  requeue on admission conflict, and runtime-build failure requeueing the wake's
  mail without an aborted turn.
- For `#55923`: `go build ./...`, `go test ./rollout ./model -count=1`, and
  `go test ./appserver -run 'Persisted|Resume|Revert|Settings|ServiceTier|TurnStart' -count=1`
  pass. Coverage pins cold-resume restoration of a thread-owned `flex` tier, `concise` summary, and
  `friendly` personality; explicit resume overrides winning over the snapshot while an unsupported
  `priority` tier stays persisted and is filtered from the response; ownership semantics for
  ownerless versus foreign snapshots with no TurnContext fallback; the settings checkpoint preceding
  the accepted user prompt on turn start; revert carrying the collaboration-mode model, tier, and
  personality into the record and checkpoint; and selected-versus-effective tier filtering where flex
  survives a catalog that does not advertise it. The full `go test ./appserver -count=1` still fails
  only in the pre-existing Windows baseline tests listed for `#56330`.
- For `#57210`: `go build ./...`, `go test ./mcp -count=1`, and
  `go test ./features -count=1` pass. Coverage pins discovery-mode launch mapping
  (unmarked -> probe, marked modern -> 2026-07-28, unknown declaration -> error),
  eligibility exclusions (remote, EMA, runtime credentials, `codex_apps`),
  feature gating of the effective server config and a disabled default, recovery
  from a helper that exits, rejects the late initialize, answers discovery with a
  legacy initialize result, reports only a legacy version, or never answers
  discovery (fresh process, bounded probe), modern negotiation, authentication
  failures aborting without a restart, retention of a working legacy connection
  when an upgrade fails, and the shared legacy catalog identity plus bounded
  message limit for discovery. Go sends discovery and initialize sequentially, so
  Rust's late-discovery race modes do not exist here; the recoveries they exercise
  are covered by the sequential equivalents.
- For `#57211`: `go build ./...`, `go test ./mcp -count=1`,
  `go test ./appserver -run 'MCP|Mcp' -count=1`, and `go test ./app -run 'MCP|Mcp' -count=1`
  pass. Coverage pins one helper process shared by two chats with per-chat answers, a release
  that keeps the other chat working, final-release shutdown followed by a fresh helper, a
  startup elicitation keeping the helper private, and a forced refresh retiring the connection
  for new chats while the sharing chat keeps using it. Gating reuses the `#57210` eligibility
  rules, so `codex_apps`, remote/EMA servers, runtime-credential servers, read-only servers, and
  `ApplyHTTPRequest` servers never enter the pool; legacy connections are private by construction
  because `isShareable` requires the 2026-07-28 mode. Rust shares one catalog snapshot between
  chats and refreshes joiners through a cache ticket; Go lists per cached client and keeps the
  same observable per-chat tool visibility.
- For `#57196`: `go test ./mcp -count=1` passes. Coverage pins unique input ids for concurrent
  callers, cancellation of one caller leaving the shared transport usable, and input wait time
  excluded from the request timeout. Rust's `read_resource_with_elicitation` extension is
  structural: Go serves `resources/read` through the same `CallWithOptionsContext` continuation
  loop, so continuation handling, unique ids, and the paused budget apply to resource and custom
  requests without a second implementation.
- For `#56278`: `go build ./...`, `go vet ./mcp`, `go test ./mcp -count=1`,
  `go test ./appserver -run 'MCP|Mcp' -count=1`, and `go test ./app -run 'MCP|Mcp' -count=1`
  pass. Coverage pins the trusted-origin projection (ChatGPT, gov, configured staging origin and
  subdomains accepted; lookalike hosts, other origins, non-HTTP schemes, stdio servers, and a
  missing provider rejected), experimental rather than extension advertisement, removal of forged
  caller metadata with caller keys preserved, attachment only for a session that negotiated the
  capability, one provider call per tool call across attempts, missing-authority and provider
  failures, and an HTTP session-recovery retry that attaches policy only after the recovered
  session negotiates it while keeping the caller `callId` and the read-only marker. Go resolves the
  metadata per send attempt inside the HTTP client's retry loop, mirroring Rust's per-session
  resolver; the stdio path has a single send attempt, so it resolves once before the request.
- For `#55607`: `go build ./...`, `go test ./model ./codexapi -count=1`,
  `go test ./mcp -count=1`, `go test ./appserver -run 'Auth|Login|Logout|Gateway|Provider' -count=1`,
  and `go test ./app -run 'Login|Logout|Auth|Provider' -count=1` pass. Coverage pins rejected
  partial-auth propagation, synchronous versus asynchronous provider resolution, the transient
  unavailable-managed-auth error while non-Codex providers still send unauthenticated requests,
  catalog identity remaining computable without credentials, MCP retry classification for
  transient versus terminal auth errors, and an auth failure suppressing the HTTP request. Go
  already returned MCP `ApplyHTTPRequest` errors before sending and cancels event streams without
  resolving credentials, so the Rust event-stream cleanup ordering holds by construction.
