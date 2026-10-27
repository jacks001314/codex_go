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
| `#57187` | Advisory rollout budgets with optional limit enforcement | `config/rollout_budget.go`, `config/config.go`, `runtimeutil/budget.go`, `appserver/runtime_router.go`, config/budget tests |
| `#56445` | Treat `model_not_found` provider errors as terminal failures | `codexapi/error.go`, `codexapi/response_debug.go`, `model/responses_stream.go`, `model/responses_agent.go`, `rollout/codex_error_info.go`, `appserver/turn_runtime.go`, like-Rust tests |
| `#56468` | Preserve `SystemRoot` for Windows filesystem sandbox helpers | `execserver/fs_helper.go`, helper environment test |
| `#56022` | Preserve config overrides across CLI subcommand boundaries | `cli/cli.go`, `cli/cli_test.go` |
| `#56552` `#57225` `#57226` | DELETE journaling on NFS: initialize new databases, migrate existing WAL databases under managed enforcement, ship `codex migrate-nfs-db` | `state/sqlite.go`, `state/nfs_migration.go`, `state/nfs_lock.go`, `config/api.go`, `config/config.go`, `config/requirements_file.go`, `appserver/runtime_router.go`, `appserver/websocket.go`, `appserver/message_board.go`, `cli/cli.go`, `app/migrate_nfs_db.go`, `app/app.go`, like-Rust tests |
| `#56495` | Paginated subscribed-channel listing on the local message board | `agentboard/local.go`, `agentboard/local_test.go` |
| `#55690` | Reclaim aged legacy marketplace staging directories | `plugin/marketplace_staging.go`, `plugin/marketplace_install.go`, `plugin/marketplace_staging_test.go` |
| `#57251` | Native dollar thread usage in app-server `ThreadUsage` (core surface + coverage) | `appserver/runtime_router_test.go` (type/forwarding landed with `auth/account.go`, `appserver/runtime_router.go`) |
| `#56553` | Thread-scoped `hooks/list` and `environmentId` in hook metadata | `appserver/hooks.go`, `appserver/runtime_router.go`, `appserver/hooks_runner.go`, like-Rust tests |
| `#57186` | Fail closed on unreadable deny-glob search root metadata errors | `sandbox/linuxsandbox/bwrap.go`, `sandbox/linuxsandbox/unreadable_glob_root_test.go` |
| `#57209` | Turn analytics critical-path fields (disabled/null default; capture pending) | `telemetry/turn_event.go`, `telemetry/turn_event_test.go` |
| `#57198` | `PreToolUse` hook input reports the local command's resolved working directory | `tool/hooks.go`, `tool/shell_executor.go`, `appserver/hooks_events.go`, `appserver/tool_hooks_adapter.go`, like-Rust tests |
| `#57203` | Preserve the command cwd in `PermissionRequest` and `PostToolUse` hook input | `tool/hooks.go`, `tool/shell_executor.go`, `appserver/hooks_events.go`, `appserver/tool_hooks_adapter.go`, `appserver/runtime_router.go`, `appserver/network_approval.go`, like-Rust tests |
| `#56494` | Channel upserts on message boards (`upsert_channel`; `description` on `create_channel`) | `agentboard/api.go`, `agentboard/inmemory.go`, `agentboard/local.go`, `agentboard/remote.go`, `agentboard/tools.go`, `agentboard/upsert_channel_test.go`, `agentboard/tools_test.go` |

Grounding notes:

- `#56234` (prompt telemetry ordering) is structurally N/A: Go never wires
  `RunUserPromptSubmit` into turns, so there is no pre-hook telemetry path to
  order against. Recorded in `update/plan_2026_09_11.md`.
- `#57293` GCP/Vertex picker portion is N/A: the Go build has no GCP provider.
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

`#57209` details: `codex_turn_event` now carries Rust's eight critical-path
fields (`critical_path_status` and the duration/inference/auto-review/
user-wait/tools/overhead/unknown milliseconds). Because Go does not implement
critical-path capture (`#57201` is still pending), the payload always reports
`disabled` with null durations, which is exactly what Rust emits with the
default-off `critical_path_timing` feature. Nulls are preserved rather than
zeroed so a missing measurement can never be read as a zero duration.

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
