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
| `#57313` | Gateway OAuth tokens default to `Authorization: Bearer` | `model/provider_info.go`, `model/gateway_auth.go`, `model/provider_config.go`, gateway tests |
| `#56309` `#57263` | Permission catalog path wording (`writable_root(s)`, `omitted_paths`, `denied_reads`) | `model/catalog.go`, `sandbox/permission_prompt.go`, `appserver/permissions_world_state.go`, catalog/permission tests |
| `#57286` | Managed `[network_proxy]` requirements with `[experimental_network]` alias | `config/requirements_file.go`, `config/cloud_config.go`, `config/ignored_settings.go`, `appserver/managed_network_constraints.go`, config tests |
| `#57304` | Surface permanent refresh-token rejection during workspace routing | `appserver/workspace_routing.go`, `appserver/turn_runtime.go`, like-Rust test |
| `#57287` | Preserve parent tool declarations in MCP-disabled prediction forks | `features/features.go`, `appserver/protocol.go`, `appserver/thread_sample_protocol.go`, `appserver/prediction_parent_tools.go`, `appserver/runtime_router.go`, `appserver/thread_sample.go`, `appserver/turn_runtime.go`, `appserver/incremental_tools.go`, `turn/runtime.go`, like-Rust tests |
| `#57270` | Load cloud config for sandbox and remote exec-server commands | `app/app.go`, sandbox/exec-server cloud-config tests |
| `#57192` | Gate capability discovery V2 on executor version | `execserver/capability_discovery_v2_compat.go`, `execserver/client.go`, `appserver/executor_capability_discovery_v2.go`, `appserver/initialize.go`, `cmd/codex/main.go`, version tests |
| `#56049` | Configured credential providers in remote credential masking | `network/proxy_credential_providers.go`, `execserver/network_policy.go`, `execserver/server.go`, `tool/unified_exec.go`, capability/masking tests |

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

## Pending in this window

- Remaining ~240 non-TUI commits in `update/_cls_core.tsv` are still being
  triaged: high-value candidates include `#57311` rollouts trace versions,
  `#57280` full MCP results, `#57276` permission
  world-state, `#57171` runtime auth, and
  `#56036`/`#56049` remote credentials. The `feedback/save` and
  `thread/memory/read` schema drift is fixed by the entries above.

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
- Plus, for `#57313`/`#56309`/`#57263`: `go test ./model ./sandbox -count=1` and
  `go test ./appserver -run 'Permission|Granular|Schema' -count=1` pass in the
  isolated worktree.
