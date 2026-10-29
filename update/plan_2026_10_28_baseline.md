# Baseline re-target 8c076942 -> 7ac6081623 (2026-10-28)

## Scope

This record advances the parity certification baseline to Rust `origin/main`
`7ac6081623928adead75caa2882e307444edad82` and closes the 455-commit gap that
the October sync windows left outside `parity/commits.json`.

- Previous certified baseline: `8c076942cb70fba8e5c4732c23f1faabc4aed484`.
- New candidate: `7ac6081623928adead75caa2882e307444edad82` (#57628).
- Go start commit: `5073f4dfdbe20bdabde8e8d9ec6742b384aee029`.
- Ledger range: `bd19459358f534ed1cae464ec13d56600aeb45f2..7ac6081623928adead75caa2882e307444edad82`,
  3155 commits (2700 previously recorded + 455 appended).

## Ledger method

Each appended commit was classified from the strongest available evidence, in
this order:

1. A Go commit whose subject names the upstream PR (`git log` match) - status
   `complete`, evidence = the Go commit hash and subject.
2. The explicit disposition recorded in the October sync plans
   (`update/plan_2026_10_19.md` through `update/plan_2026_10_28_tui.md`):
   `Implemented`/`Landed` sections and triage-table status cells - status
   `complete` (implemented/equivalent), `not_applicable`, `partial`, or
   `blocked_precondition`, evidence = the plan file, line, and disposition row.
3. Otherwise `audit_pending`, with the plan reference that still needs a code
   audit.

The ledger keeps the existing schema (`sha`, `subject`, `kind`, `domains`,
`status`, `evidence`) and the chronological order required by
`TestAlignmentCommitLedgerMatchesRustGitRange`.

## Disposition of the 455 appended commits

| Status | Count |
| --- | --- |
| `complete` | 295 |
| `not_applicable` | 149 |
| `partial` | 7 |
| `blocked_precondition` | 1 |
| `audit_pending` | 2 |

`complete` includes ports landed in the October windows and the structural
Guardian crate moves where Go already implements the behavior under a
different shape. `not_applicable` covers Rust-only Crates/CI, Bazel/V8 build
plumbing, documentation scripts, and the runtime-agent-instance subsystem that
Go does not have.

### Partial (still open)

None. `#52756` moved to `complete`; see "Voice failure classification closed".

`#57267` moved from `partial` to `blocked_precondition`: Go has no native cloud
conversation TUI. `codex cloud tui` is a task-list renderer over
`chatgptapi.CloudClient`, and `cloudclient` exposes only the low-level
ThreadService Resume/Attach RPCs, so there is no cloud session whose
thread/start, thread/resume, turn, `thread/settings/update`, bootstrap,
permission, fork, or recap behavior could follow the server-owned cloud rules.

### Apple Terminal over SSH closed

`#47417` moved from `partial` to `complete`:

- The Unix startup probe appends DA2 when `SSH_TTY` or `SSH_CONNECTION` is set
  outside a tmux/zellij multiplexer; the DA1/DA2 pair (`1;2` / `1;95;0`)
  identifies Apple Terminal, an incomplete pair stays unknown, and signatures
  inside a bracketed paste are ignored.
- The launcher probes before the first frame (onboarding included). `auto`
  keeps native scrollback for Apple Terminal over SSH; `always`, `never`,
  `--no-alt-screen`, and `tui.fullscreen_transcript = false` are unchanged, and
  the resolved ownership survives configuration reloads.

### Attached thread forks closed

`#56351` moved from `partial` to `complete`:

- `thread/fork` accepts `attachToSource` for paginated parents only and rejects
  `ephemeral`/`deferGoalContinuation` combinations; attached forks skip
  `thread/started` and persist `attached_thread` in the rollout session meta.
- `thread/list` accepts `attachedToThreadId` (mutually exclusive with
  `parentThreadId`/`ancestorThreadId`), returns attached forks including empty
  ones, and pages across them; discovery requires the state database.
- Ordinary listings, spawn-child listings, and rollout scans hide attached
  forks; the attachment survives restart, a missing state database, and a
  rebuilt corrupt database because backfill restores `attached_thread_parent_id`.
- Attached forks keep the parent history base, so resume sees the parent's
  messages, and deleting a source thread with attached children fails with
  "delete the attached thread first".
- Coverage: `appserver/attached_threads_like_rust_test.go` plus
  `go test ./rollout ./session ./state`.

### Attached thread views closed

`#57256` had been recorded as complete on the strength of a plan note; the
history-page boundary it requires is now implemented and tested:

- `state/thread_history_query.go` resolves client-visible lineage in a
  history-page mode that stops an attached fork at its parent bookmark, and
  `rollout/attached_history.go` (`AttachedHistoryStart`) skips the inherited
  abort marker so a mid-turn fork does not show the parent's interruption.
- Items, turns, search occurrences, `thread/read` turns, and
  `TurnsBackwardsCursor`-style fork boundaries (`lastTurnId` /
  `beforeTurnId`) now use that visible lineage; forking through an inherited
  turn fails with `turn not found`.
- `thread/read` for paginated threads builds turns from the thread history
  store, paginated fork resumes suppress inherited token-usage replay, and
  `thread/revert` rejects a boundary turn inherited from the parent.
- Coverage: `appserver/attached_thread_views_like_rust_test.go` plus
  `go test ./rollout ./state ./session`.

### Frozen cloud skill snapshots closed

`#55631` moved from `partial` to `complete`:

- `skillprovider/frozen_cloud.go` owns the snapshot format: `{skills:[{package,
  name, description, alias_root, main_resource, prompt_visible, resources}]}`,
  a 64 MiB file cap, 1 MiB per resource, strict JSON without unknown fields, and
  validation of alias-root/package boundaries, non-empty names, the main
  resource, and duplicate packages.
- `FrozenCloudProvider` serves `List`/`Read` only for the Cloud `codex_apps`
  authority, rejects `Search`, and reports `UsesMCPResources() = false`, so
  `cloudSkillCatalogScope` ignores MCP resource generations for the snapshot.
- The app server accepts `--cloud-skills-snapshot PATH` and loads it during
  startup, so a missing or invalid file fails the process; `in_process` startup
  keeps no snapshot.
- `RuntimeServices.CloudSkills` replaces MCP discovery for the orchestrator
  prompt catalog, `skills.list`, `skills.read`, and `skills.installations`
  without requiring an MCP service. `prompt_visible=false` entries stay
  readable and listable but leave the model catalog.
- `skills.read` resolves the authority from the matched catalog entry like
  Rust's `matching_packages`, and validates the requested resource against the
  canonical package, so rendered aliases such as `c0/work` read the snapshot
  contents.

### WebSocket mailbox-preemption drain closed

`#55969` moved from `partial` to `complete`:

- `model/responses_agent.go` reads WebSocket frames through a per-session
  reader channel, so the request loop can observe `AgentRequest.Preempt` while
  a frame is outstanding without closing the socket.
- On preemption of a Responses Lite request it sends
  `{"type":"response.interrupt","response_id":...,"mode":"discard_partial_items"}`
  once `response.created` supplied the id, then drains to the terminal event.
  Partial `output_item.added` and delta events are withheld; `output_item.done`
  is exposed only when confirmed. The result carries `Drained`, `Preempted`,
  the completed items, usage, and the response ID.
- A read error or terminal error during the drain returns the completed items
  as a follow-up instead of failing the step, matching Rust's "mail already
  requires a follow-up" branch. Non-lite models keep the #48141
  drop-connection behavior and receive no interrupt.
- The turn loop records drained items, keeps `previous_response_id`, records
  buffered tool calls as `aborted before dispatch` outputs, dispatches none of
  them, and delivers queued mail on the next request.
- Token-free WebSocket tests cover acknowledgement, buffered completion without
  acknowledgement, a producer disconnect during the drain, and the non-lite
  path. Turn tests cover retained items, continuation, aborted tool output, and
  queued mail.

### HTTP stream payload retention closed

`#57563` moved from `partial` to `complete`. Go has no inference-trace attempt
or WebSocket last-response sender, so the accumulator only retains the items its
`AgentResponse` returns to the turn. The redundant copy was the accumulated
tool-argument buffer: `functionCallArgDeltas`/`customToolInputDeltas` kept every
delta after the completed item already carried the applied value.
`releaseToolInputDeltas` now drops those buffers when `response.output_item.done`
or a terminal response output supplies the completed item, so a completed
item's payload is released while the stream is still open.

### WebSocket probe evidence and redaction closed

`#57597` moved from `partial` to `complete`:

- `codexapi/websocket_probe.go` adds the structured probe surface:
  `ResponsesWebsocketProbe` (upgrade status, allowlisted `x-request-id`,
  `x-oai-request-id`, `cf-ray`, and `x-ms-request-id` headers, reasoning/model
  flags, immediate close), `ResponsesWebsocketProbeError` (original error,
  `tls`/`unknown` stage, and the upgrade status/headers received before a
  post-upgrade read failure), and `ResponsesWebsocketClose`.
- Probe `String()` output redacts the URL, header values, and close reason;
  empty close frames report code `1005`.
- `doctor/doctor.go` probes through this type, keeps the HTTP 101 handshake
  evidence when the following read fails, classifies typed TLS failures, and
  only renders redacted categories. Existing doctor redaction and empty-close
  tests continue to pass.

### Blocked precondition

| PR | Blocker |
| --- | --- |
| `#55378` | Native Windows MXC SDK integration (`mxc-sdk`); Go reports availability `false`. |
| `#39957` | Local unified-exec launches still spawn directly, so the core-side `shell_snapshot_request` gate cannot select the executor-managed snapshot path. |

### TUI title spinner removal and initial-prompt recall (#55324, #55372)

`#55324` moved to `complete`. Rust deleted the title-generation spinner and the
`thread_title_generation_pending` UI state while keeping background naming. Go
never implemented that spinner: `automaticThreadTitlePending` only selects the
provisional name and is invisible to `isTaskRunning`, and the status surfaces
never append a progress frame to `ThreadName`/`ThreadTitle`/`SessionId` or to
the terminal title. `TestAutomaticThreadTitlePendingShowsNoProgressSpinnerLikeRust`
pins that the footer, thread-title status line, and terminal title stay free of
spinner frames while pending and that the generated title still replaces the
provisional one.

`#55372` (command-line initial prompt recall) moved to `not_applicable`. Go has
no TUI initial-prompt carrier: a non-empty `root.Prompt` routes to the one-shot
`runInteractivePrompt`/`runInteractiveRemotePrompt`, and `runInteractiveTUI` is
only reached with an empty prompt. The tea model has no initial-user-message
option, and `SessionFlowState.SubmitInitialUserMessageIfPending` is used only by
tests, so there is no composer recall entry to record.

### Agent dashboard stays browsable during removal closed (#53908)

`#53908` moved to `complete`. Archiving or deleting from `/agents` now hides the
target immediately through `agents_overview.View.HideThread` plus
`PrepareRemoval`, and shows Rust's inline
`Archiving/Deleting task…  You can still browse.` notice instead of a centered
blocking dialog. The RPC runs as a normal Bubble Tea command, so
`updateAgentsOverviewRemovalKey` can keep list movement, help, status tabs, and
search live while swallowing task switching, new tasks/worktrees, fork/resume,
rename/stop/pin/hide/lifecycle actions, dashboard exit, and ctrl+c. Failures
unhide the task; successes keep the adjacent selection across the refresh,
including when the removed task was the attached session.

### Skills refreshes and workspace commands use explicit directories (#54050)

`#54050` moved to `complete`. Closing the skills management popup now reloads
the current `sessionCWD` (falling back to the captured popup cwd only when it
is empty), and a skills inventory result whose cwd no longer matches the
session is dropped instead of overwriting the active inventory. The workspace
command boundary now mirrors Rust's explicit-directory shape:
`NewWorkspaceCommand(cwd, argv...)` sets and validates a non-empty cwd, the
`WithCWD` mutator is gone, `LocalWorkspaceCommandRunner` always sets
`cmd.Dir`, and the Git diff/probe builders use the constructor.

### Audit pending

None.

### Windows proxy CONNECT authentication closed

`#55994` moved from `blocked_precondition` to `complete` after the Go
implementation landed:

- `network/proxy_auth.go` owns the CONNECT handshake, parses
  `Proxy-Authenticate`, retries with `Proxy-Authorization`, preserves origin
  `407` responses over an established tunnel, and reports a failed CONNECT as
  `Proxy Authentication Required`.
- `network/proxy_auth_windows.go` answers `Negotiate`/`NTLM` through SSPI with
  the signed-in user and SPN `HTTP/<proxy host>`, matching Rust's libcurl
  `gssnegotiate+ntlm` helper without cgo. Rust's named-pipe bridge is N/A
  because Go owns the CONNECT socket directly.
- `network/proxy_auth_transport.go` pools one transport per `(proxy, target)`
  route and exposes `BaseTransport`, `ReplaceBaseTransport`, and
  `WithoutSystemProxy` so policy wrappers, custom dialers, and custom CA roots
  survive the wrapper.
- Wired into `NewHTTPClient(respect_system_proxy=true)`, the remote TUI
  WebSocket dial, and the remote plugin HTTP pool.
- Token-free tests cover the challenge loop, unsupported schemes, origin `407`
  passthrough, TLS proxies, the real Windows SSPI initial token, and the
  `NewHTTPClient` wiring. `go test ./network`, `-race` on the proxy tests, and
  `CGO_ENABLED=0` windows/arm64 + linux/amd64 + darwin/arm64 builds pass.

### Ledger reconciliation: four stale entries closed

The October ledger still recorded these as blocked/partial after the Go
implementation had landed. Each was re-audited against the Rust commit and its
LikeRust tests:

| PR | Evidence |
| --- | --- |
| `#48812` | `appserver/thread_idle_lifecycle.go` arms `scheduleHistoryPrewarm` at the idle boundary; `appserver/session_startup_prewarm.go` gates reuse on prefix extension plus model/instructions/service-tier settings; `model/responses_agent.go` keeps `generate:false` and skips preparation when a cached WebSocket is ready. |
| `#49036` | `appserver/guardian_conversation_history.go` exposes `user_message.search_messages`/`read_messages` through the parent's host-owned Apps catalog, rechecks app/tool policy per call, limits output to an estimated 4,000 tokens, and honours the prompt override. |
| `#52034` | `appserver/thread_idle_lifecycle.go` unloads idle multi-agent v2 children through local eviction, preserving queued mail and environment selections, with residency pins and pending-cleanup waits. |
| `#51230` | `session` cursors carry the created-at tie breaker; `app/named_session_lookup.go` proves uniqueness only with tie-breaker cursors and propagates listing failures; the resume/fork pickers keep loaded sessions and show `Could not load more sessions`. |

Closing `#48812` also required two telemetry details the Go port had missed:
the `codex.startup_prewarm.websocket_check` counter with an `input` tag, and
the `after_prewarm` tag on the generation that consumes a prepared warmup
response (`AgentRequest.PreviousResponseFromPrewarm`, set only for the first
sampling request of the turn). `codex.startup_prewarm.duration_ms` now uses the
Rust `input` attribute, and `codex.startup_prewarm.age_at_first_turn_ms`
carries only `status`, matching Rust.

### Guardian internal session closed (#39994)

`#39994` moved from `blocked_precondition` to `complete` after the Go
implementation landed:

- `appserver/internal_session.go` owns the host-owned spawner/forker, parent
  lineage without history inheritance, `ThreadReadyInput` lifecycle callbacks,
  and teardown.
- `appserver/runtime_router.go` rejects every public request path that targets
  an internal thread with `thread not found: <id>` unless the request is
  host-internal; `thread/list` and `thread/loaded/list` omit internal threads.
- `appserver/turn_runtime.go` carries `SessionID` separately from `ThreadID`
  through `turn.AgentLoopRequest`, so an internal turn sends the reused parent
  session as `session-id`, its own internal thread as `thread-id`, and
  `guardian:<parent thread>` as the prompt cache key (Rust
  `core/src/client.rs` `responses_session_id`).
- `session/source.go`, `appserver/top_level_types.go` and
  `appserver/protocol.go` serialize the source as `internal_guardian`
  (`{"internal":"guardian"}` on the wire); `codexapi/client_context.go` sends
  the guardian subagent header and parent thread id.
- `appserver/memory_startup.go` records `internal_memory_consolidation` through
  the same lifecycle, and `doctor/thread_inventory.go` categorizes
  `{"internal":"guardian"}` as `internal:guardian`.

Verified with:

```powershell
go build ./...
go test ./appserver -run 'InternalSession|ThreadReady|GuardianThreadContext|MemoryConsolidation|Compaction' -count=1
go test ./turn -count=1
go test ./doctor -run Rollout -count=1
```

### Guardian async risk scoring is not applicable (#40013)

`#40013` moved from `blocked_precondition` to `not_applicable`. Rust reuses
retained synchronous Guardian reviews inside
`ext/guardian-v2/src/async_scorer`; Go intentionally has no Guardian V2 async
risk-scoring subsystem: the synchronous reviewer returns the full structured
assessment, with no sampler, score cache, or score-reuse approval path
(`scoreProgress` only tracks `max_tool_call_lag`). The sibling async-classifier
commits `#40393`, `#40431`, and `#56556` are recorded `not_applicable` for the
same reason. Go keeps the user-visible routing: full access and user-mode Node
REPL fast approvals bypass review only through `emitGuardianV2FastDecision`,
and protected models still force the full reviewer (`#41309`).

### Bounded retained-turn analytics closed (#55847)

`#55847` moved from `partial` to `complete`. Rust bounds the analytics
reducer's retained turns at `MAX_PENDING_TURNS = 256` and drops a closed
thread's turn and review state, including reviews whose turn never completed.
Go has no single reducer, but the app-server router retains the same per-turn
analytics evidence, so the bound and the cleanup live where that evidence is
kept:

- `appserver/analytics_retained_turns.go` keeps a 256-entry FIFO of retained
  `(thread, turn)` pairs (`registerAnalyticsTurn`). On overflow it evicts the
  oldest turn's sampled tool calls, Code Mode child calls, pending correlated
  tool events, tool item model contexts, unified-exec execution context,
  review summaries and queued MCP elicitations (`discardAnalyticsTurn`),
  mirroring `AnalyticsReducer::discard_turn`.
- Every per-turn ingestion path registers its turn: sampled tool calls, Code
  Mode child calls, correlated tool events, review summaries, tool item model
  contexts, unified-exec analytics and MCP tool elicitations.
- `RuntimeRouter.markThreadUnloaded` calls `discardThreadAnalyticsTurns`, which
  drops the thread's retained-turn ordering, review summaries, item model
  contexts and unified-exec analytics, covering reviews whose turn never
  completed (Rust's `ThreadClosed` handling in `outgoing_message.rs`).
- `forgetMCPToolCallElicitationTurn` drops one turn's queued elicitations.

Verified with:

```powershell
go test ./appserver -run 'RetainedAnalyticsTurns|ClosedThreadDropsAnalytics' -count=1
go test ./appserver -run 'ToolEvent|Elicitation|Review|Unload|Mailbox|Analytics' -count=1 -skip 'TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust|TestRuntimeRouterFileChangeAnalyticsIncludesUserReviewSummaryLikeRust'
```

### In-memory shell snapshot replay landed (#40376 closed)

`#40376` moved from `blocked_precondition` to `complete`, and the exec-server
half of `#39957` landed; `#39957` itself stays blocked on the core-side routing
gate. Rust keeps executor-managed shell snapshots in memory and only
materializes a replay file for large states, bounds the cache at 16 entries,
and suppresses automatic startup files after the restore.

- `execserver/shell_snapshot.go` caches a `shellSnapshotEntry{state, path,
  fileSource}` instead of a path. States within the 512 KiB environment budget
  stay in memory and replay through chunked
  `__CODEX_SHELL_SNAPSHOT_STATE_*` variables (`shellSnapshotReplayScript`);
  larger states keep the 4 MiB file replay.
- The cache is bounded at `maxCachedShellSnapshots = 16` with oldest-first
  eviction, and an evicted file-backed entry removes its replay file.
- Restored launches switch to bash `-pc` + `set +o privileged` and zsh `-fc` +
  `setopt RCS` (`shellSnapshotStartupScript`), so automatic startup files do
  not re-run after the snapshot.
- `localEnvironmentInfo` now reports `ShellSnapshotV2 = runtime.GOOS !=
  "windows"`, matching Rust's `EnvironmentInfo::local` (`cfg!(unix)`).
- `#39957` remains blocked because `tool/unified_exec.go` only dials a remote
  exec server, so eligible local launches never set
  `exec_server_shell_snapshot`.

Verified with:

```powershell
go test ./execserver -run ShellSnapshot -count=1
go test ./execserver -count=1
go build ./...
```

### Daemon mismatch recovery closed

`#55442` moved from `partial` to `complete`:

- `app/daemon_compatibility.go` ports `compatibility_warning`. Explicit
  `-c`/`--enable`/`--disable` toggles merge into one CLI layer (a `features`
  table replaces earlier dotted keys), removed features are skipped,
  `legacyManagedConfigTomlFromFile`/`Mdm` layers and
  `configRequirements/read` feature requirements override the launch flags,
  and `experimentalFeature/list` decides the rest: a removed stage is inert and
  an unreported feature counts as disabled.
- An optional attachment downgrades only non-restartable issues to the
  `Running without the shared background server` warning; everything else is a
  `CompatibilityError` carrying the Rust reason text and `FAILURE_HINT`.
- `app/daemon_recovery.go` and `tui/onboarding/daemon_recovery.go` port
  `daemon_recovery::check`: a blocking pre-TUI menu offers run without daemon,
  restart with these settings, and cancel; Cancel is preselected, the preview
  caps at three settings plus `and N more`, restart is offered only for a
  managed daemon with restartable features, and `RestartWithFeatures` persists
  the confirmed shared settings. Compatibility is re-checked exactly once, and
  a missing terminal or cancel/esc returns the compatibility error.
- Automatic startup now calls `StartWithFeatures(nil)` (Rust
  `start_with_features(&Default::default())`), so saved daemon settings survive
  until a confirmed recovery restart, and `daemonStartupWarning` reports the
  compatibility warning ahead of the auto-start exclusion warning.
- `features.FeatureEntry` now decodes the Rust `name` wire field; the JSON
  round trip previously dropped every catalog key, which also broke the
  `/experimental` popup against a real server.

Verified with:

```powershell
go test ./app -run 'Daemon|TestReadDaemonCompatibility|TestResolveDaemonCompatibility' -count=1
go test ./tui/onboarding -run DaemonRecovery -count=1
go test ./features -count=1
go test ./tui/... ./appserverdaemon -count=1
go build ./...
```

### Voice failure classification closed

`#52756` moved from `partial` to `complete`:

- `app/remote_tui.go` tags transport-level failures (websocket and unix socket
  read/write/dial) as `remoteTransportError`, the analogue of Rust
  `TypedRequestError::Transport`. Only those failures emit the new
  `codextea.AppServerDisconnectMsg`; a JSON-RPC error never does.
- `tui/tea`'s `handleAppServerDisconnect` mirrors `App::begin_reconnect` ->
  `ChatWidget::pause_for_disconnect`: it records `app_server_disconnect` when
  the phase is starting/active or retry cleanup is pending (at most once),
  retires the local helper without a stop RPC, resets the session, and finishes
  the duration metric with a `failure` outcome. The voice state keeps its own
  thread across navigation, so a parked (background) session is covered by the
  same carrier.
- A failed realtime stop request is now reported to the TUI and records
  `app_server_request` before the session resets and the error is shown
  (`Voice conversation failed: <err>`); start failures already carried that
  cause. A stale answer or attached message after a reset retires the orphaned
  helper, matching Rust aborting startup.
- A speech delivery failure records `speech_delivery` and stops the session
  with Rust's fixed `Failed to deliver the voice response.` text instead of
  leaving the strip live.
- The bounded `local_request` cause has no Go carrier: realtime start/stop are
  issued inline from the tea runtime, so the Rust app-event submit failure
  branch cannot occur.

Verified with:

```powershell
go test ./tui/chatwidget ./tui/tea -count=1
go test ./tui/... -count=1
go test ./app -run 'Voice|Daemon|TestReadDaemonCompatibility|TestResolveDaemonCompatibility' -count=1
go test ./voicehost ./appserver -run 'Voice|Realtime' -count=1
go build ./...
```


### Daybreak API-key defaults and background-turn eligibility closed

`#53701` and `#53717` moved to `complete`:

- `features.cli_daybreak` and `features.api_key_cyber_access_programs` are
  stable and default-enabled, matching Rust.
- `exec/exec.go` resolves an implicit cyber program from the model catalog when
  `cli_daybreak` is on and no `--cyber-access-program` was given: Daybreak Blue
  (then Red) when the `daybreak` preference is on, and the advertised standard
  program when it is off and the account is ChatGPT or an API key allowed by
  `api_key_cyber_access_programs`. A nonempty list without Daybreak still
  fails; unlisted models and missing or empty metadata keep the server's
  automatic treatment.
- TUI turn selection (`tui/tea/daybreak_access_program.go`) now omits the
  override for missing or empty discovery metadata instead of failing the turn,
  while still rejecting a nonempty list without Daybreak.
- TUI background turns (`app/dynamic_tools.go`,
  `app/dynamic_tools_daybreak.go`) inherit the source thread's effective
  `daybreak` configuration through `config/read` when the server supports it,
  resolve account eligibility, read the target thread's
  `experimentalFeature/list` for API-key forwarding (so project opt-outs
  apply), keep saved metadata only for eligible accounts, and report account
  lookup failures when Daybreak is on for the OpenAI provider.

Verified with:

```powershell
go test ./exec -run 'TestExecDaybreakProgramForTurnLikeRust' -count=1
go test ./tui/tea -run 'Daybreak|CyberAccessProgram|TestModelSafetyRecourse' -count=1
go test ./app -run 'DynamicTool|Daybreak|ThreadStart' -count=1
go test ./tui ./tui/tea -count=1
go build ./...
```

### Windows runtime ACL repair avoids sharing violations (#51822 closed)

- `sandbox/windowssandbox/acl_windows.go`: runtime ACL repair never opens a
  target with `MAXIMUM_ALLOWED`. DACL reads request `READ_CONTROL`, and only an
  actual update opens `READ_CONTROL | WRITE_DAC`, so repairing an active
  `node.exe` that another process holds with `FILE_SHARE_READ` no longer fails
  with a sharing violation. Root-only grants now keep a null DACL instead of
  replacing it.
- `sandbox/windowssandbox/bin/setup_main/win/setup_runtime_bin.go`:
  `ensureRuntimeTreeReadable` revalidates only after the DACL actually changed,
  so an intentional deny ACE no longer aborts the whole repair (Rust
  `ensure_runtime_tree_readable`).
- `GrantReadExecuteACEs` and `GrantAllowMaskACEsWithInheritance` now report
  whether the DACL changed.
- Regression `TestEnsureRuntimeTreeRepairsHeldRuntimeFileAndPreservesDenies`
  holds `node.exe` open with `FILE_SHARE_READ` across two repairs and checks
  read/execute access, the absence of write/DELETE/`WRITE_DAC` grants, and
  surviving read and write deny ACEs.

Verified with:

```powershell
go test ./sandbox/windowssandbox/... -count=1
go test ./parity -count=1
go build ./...
```
### Windows sandbox security audit batch closed (#51527, #51547, #51678, #52256)

- `#51527` is implemented: the Linux deny-glob expansion invokes ripgrep with
  `--no-config` ahead of `--files --hidden --no-ignore`
  (`sandbox/linuxsandbox/bwrap.go:321`), so a user `RIPGREP_CONFIG_PATH` such as
  `--quiet` cannot shrink the list used to build deny masks; the Windows
  deny-read resolver passes the same flag, and
  `TestLinuxDenyReadGlobExpansionIgnoresRipgrepConfiguration` freezes it.
- `#51547` is implemented: `config.ValidateWindowsMXCOptOut` rejects an explicit
  `windows.sandbox = "mxc"` when `windows.allow_mxc = false` with the Rust
  message, while omitted/true settings, legacy backends, and the opt-out alone
  still load (`config/config.go:786`).
- `#51678` and `#52256` are not applicable: they restructure Rust nextest
  integration binaries and normalize prettier/rustfmt layout in Rust docs,
  login HTML assets, and sample scripts. Go has no nextest configuration, no
  Rust docs or HTML assets, and owns the `systemskills` sample bundle
  separately.

### Code-evidence audit sweep (Round 736)

Every `audit_pending` entry whose upstream PR number is already cited by the Go
implementation was re-audited. 203 entries have production or test code that
names the Rust PR and mirrors its behaviour; each was moved to `complete` with
the citing location (`<file>:<line>`) as evidence. The cited packages were
re-tested: 56 packages pass, with only the pre-existing environmental failures
noted below (`./app` `TestExecJSONEndToEnd`, `./exec` websocket handshake
stubs, `./appserver` analytics timing under parallel load).

The remaining `audit_pending` entries fall into two groups: 247 carry
symbol-coverage notes that still need per-symbol review, and the rest have no
Go citation at all (Rust-only crates, CI, docs, or behaviour that may still be
missing).

### Citation-verified audit closure (Round 759)

Re-audited the 90 `audit_pending` entries whose Rust PR number is cited by
Go code. Each was checked for a cited implementation site plus a regression
test whose body names the same PR; 18 cleared both bars and moved to
`complete` (`#41421 #42894 #42897 #43340 #43884 #43921 #44066 #44077 #44311
#46117 #46486 #46751 #46837 #48783 #49160 #50525 #51473 #51884`). The
remaining citations are comment-only or sit next to unrelated functions
(`#43120`), so they stay `audit_pending` until the real implementation and
its coverage are located. Targeted package tests pass in a clean archived
snapshot: `./mcp ./tui/bottom_pane ./network ./remotecontrol ./context
./tui/agents_overview ./config ./app ./tui/tea ./appserver`.

### Subject-keyword implementation sweep (Round 760)

For the remaining cited entries the PR comment was not next to the real
implementation, so each subject was re-searched by feature keywords. Eight
entries resolved to a dedicated Go file plus package tests and moved to
`complete`: `#43055` (`tui/tea/status_copy.go`), `#43286`
(`tui/tea/worktree_browser.go`), `#43547` (`appserver/user_verification.go`),
`#47132` (`network/proxy_mitm_runtime.go`), `#47207`
(`appserver/gateway_oauth.go`), `#47911` (`tui/tea/prompt_suggestions.go`),
`#49715` (`app/security_setup.go`), `#50039`
(`tui/tea/transcript_find_main.go`). Each evidence string names the
implementation site and the regression test that verifies it; the four
affected packages pass in a clean archived snapshot.

### Transport policy and read-only view sweep (Round 761)

Eight more cited entries resolved to a dedicated Go file plus package tests and
moved to `complete`: `#42118` (`tui/tea/hook_status.go`), `#42386`
(`appserver/protocol.go`), `#43253` and `#48132` (`tui/tea/read_only_thread.go`),
`#43622` (`tui/status/remote_connection.go`), `#47389`
(`network/policy_http.go`), `#47407` (`appserver/application_network.go`), and
`#50113` (`cloudclient/client.go`). Each evidence string names the
implementation site, the behaviour, and the regression test; the five affected
packages pass in a clean archived snapshot.

### Thread title, transcript detail, and integrity sweep (Round 762)

Eight more entries cleared the implementation-plus-regression-test bar and
moved to `complete`: `#42749` (`tui/tea/thread_title_automatic.go`), `#42889`
(`app/interactive.go`), `#42852` (`appserver/guardian_reviewer.go`), `#46734`
(`tui/tea/transcript_disclosure.go`), `#46882`
(`tui/agents_overview/overview.go`), `#49357` (`tui/tea/composer_paste.go`),
`#51831` (`sandbox/integrity/policy.go`), and `#46542`
(`plugin/plugin_measurement_references.go`). Evidence records the behaviour and
the tests that pin it; the six affected packages pass in a clean archived
snapshot.

### Trusted access, doctor PATH, and host policy sweep (Round 763)

Eight more entries moved to `complete`: `#41005` (`mcp/api.go` trusted access),
`#42324` (`doctor/doctor.go`), `#42417` (`config/api.go`), `#43558`
(`tui/history_cell/separators.go`), `#43603` (`tui/tea/size_monitor.go`),
`#46490` (`model/gateway_auth.go`), `#46503`
(`tui/tea/status_controls.go`), and `#47410` (`app/application_network.go`).
Each evidence string cites the implementation site, the behaviour, and the
package tests that pin it.

### Worktree discovery and Windows daemon sweep (Round 764)

Eight more entries moved to `complete`, concentrated in worktrees and the Windows
managed daemon: `#42196` (`worktree/manager.go`), `#43120`
(`tui/tea/worktree_browser.go`), `#43279` (`tui/session_source.go`), `#42744`
(`appserver/node_repl_evidence.go`), `#42381`
(`appserverdaemon/lifecycle.go`), `#42392`
(`appserverdaemon/update_job_windows.go`), `#42405`
(`appserverdaemon/detached_launch_windows.go`), and `#52176`
(`app/gov_cloud_guidance.go`). The Windows daemon tests run natively on this
host and pass.

### Git discovery, OAuth refresh, and catalog refresh sweep (Round 765)

Eight more entries moved to `complete`: `#42132` (`gitutil/discovery.go`),
`#42413` (`mcp/http_client.go`), `#42903` (`tui/tea/async_questions.go`),
`#46510` (`appserver/runtime_router.go`), `#46572`
(`appserver/cloud_catalog_refresh.go`), `#46680`
(`tui/bottom_pane/selection_picker_layout.go`), `#46711`
(`tui/activity_group.go`), and `#50803` (`app/remote_control.go`). Evidence
cites the behaviour and the package tests that pin it in a clean archived
snapshot.

### Credential export, attachments, and read-only MCP sweep (Round 766)

Eight more entries moved to `complete`: `#44028` (`model/credential_export.go`),
`#44068` (`network/proxy_credential_broker.go`), `#44564`
(`appserver/thread_attachments.go`), `#44691`
(`config/ignored_settings.go`), `#45255` (`app/agents_dashboard.go`), `#45454`
(`tui/tea/model.go`), `#45711` (`appserver/protocol.go`), and `#46042`
(`mcp/read_only_tools.go`). Evidence cites the behaviour and the package tests
that pin it.

### Managed provider, telemetry, and Windows sandbox sweep (Round 767)

Eight more entries moved to `complete`: `#44650` (`config/config.go`),
`#44661` (`appserver/session_telemetry.go`), `#43949`
(`appserver/thread_attachments.go`), `#45276` (`app/agents_overview_tui.go`),
`#46839` (`tui/agents_overview/overview.go`), `#44872`
(`sandbox/windows_mxc_availability.go`), `#45224`
(`sandbox/windowssandbox/desktop_windows.go`), and `#46116`
(`tui/chatwidget/tool_lifecycle.go`).

### Async question lifecycle and cloud skills snapshot (Round 768)

Three more entries moved to `complete`: `#42891` and `#44328`
(`tui/tea/async_questions.go`) and `#47074` (`app/app.go`). The async-question
tests cover notification, markdown, cross-client dismissal, and prompt-driven
clearing; the cloud-skills snapshot test pins the startup failure path.

### Plan usage history and Windows sandbox host parity (Round 769)

Closed two audits. `#44939` (Windows sandbox setup) keeps the local app-server authoritative for sandbox readiness and reads remote platform state from the app-server rather than the client OS; the three `WindowsSandbox` app tests lock the prompt behavior. `#45766` adds the gated `/usage plan` history path across `chatgptapi`, `tui/analytics`, `tui/tea`, and slash dispatch, covering 404-as-empty, snapshot validation (as-of, RFC3339, period cap, windows, duplicate IDs, non-finite values), stale-result rejection, and the disabled-gate/unavailable notices. Verified in a clean index snapshot with `go build ./...`, `go test ./chatgptapi ./tui/analytics`, `go test ./tui/tea -run 'TestUsagePlan|TestModelUsageCommand'`, `go test ./tui/chatwidget -run TestPreparedSlashArgsUsageMcpKeymapRawMatchRust`, and `go test ./app -run WindowsSandbox`.

### Retained stdin permissions and remote saved-permission parity (Round 770)

Closed two audits. `#41328` was already implemented at the app-server layer: unified-exec processes retain launch sandbox permissions, additional/runtime-internal grants, and the managed-network flag, and the write_stdin review compares them with the current turn policy, failing closed on denied-read or unfittable drift while the approval action reports effective mode and user-visible grants without internal paths. `#43330` now matches Rust end to end: remote resume/fork requests omit permission fields when inheriting, the resume response refreshes the live approval/sandbox/profile state, and a new `app/remote_permission_overrides.go` mirrors Rust's explicit-override detection (launch flags, `-c` permission keys, session-flag and profile layers) so remote resume and fork refuse overrides with Rust's messages. Verified with `go test ./appserver -run WriteStdin`, `go test ./tool -run WriteStdin`, and `go test ./app -run 'RemoteResume|RemoteFork|RemotePermission|RemoteTUI|InteractiveRemote'`; `#44746` remains open because the Go TUI still has no in-session trust modal for a destination chosen after startup.

### Onboarding, banner, recovery, routing, and event-stream audits (Round 771)

Closed five audits by matching existing Go implementations: `#40679` Bedrock onboarding (`tui/onboarding/auth.go`, `bedrock.go`, `bedrock_setup_wizard` feature), `#41742` actionable usage banners with automatic model fallback (`app/backend_banners.go`, `app/backend_banner_wiring.go`, `tui/tea/backend_banner*.go`), `#45820` daemon-restart continuation (`appserver/daemon_recovery_continue.go`), and `#46281` workspace routing applied to model requests (`model/workspace_routing.go`, `appserver/agent_runtime.go`). `#41899` gained a new regression: `appserver/mcp_event_stream_test.go` now unloads the subscribed task mid-stream and asserts a later event is still forwarded, locking the dedicated-connection design.

### History output budgets, voice settings, and removed rollback API (Round 772)

Closed three audits. `#41260` is already reflected in `historynotes`: the tool returns the backend result unchanged (the backend enforces the output budget before encryption) while still sending the truncation-policy header, and the test keeps a large encrypted payload intact. `#44622` is implemented end to end: the TUI exposes `/voice settings`, loads supported voices plus the saved preference, persists a new choice through local and remote savers, and the slash description matches upstream. `#44915` is not applicable because codex_go never exposed the deprecated `thread/rollback` method; only `thread/revert` exists.

Status after reconciliation: `1956 complete`, `382 not_applicable`,
`524 audit_pending`, `287 regression_pending`, `0 partial`,
`6 blocked_precondition`.
