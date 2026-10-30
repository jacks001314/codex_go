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

### Codex home size metric compression tag (Round 773)

Closed `#42043`: every `codex.app_server.codex_home.size_bytes` sample now carries `compression_enabled`. `appserver/codex_home_metrics.go` gained `codexHomeCompressionEnabled`, which reads the effective app-server config (including `--enable`/`--disable` feature defaults) and reports the `local_thread_store_compression` gate, matching Rust's `config.features.enabled(Feature::LocalThreadStoreCompression)`. `recordCodexHomeMetrics` tags both the `sessions` and `archived_sessions` samples with the startup value (not compression progress), and `appserver/runtime_router.go` resolves it once before spawning the background scan, mirroring Rust capturing the config before `spawn_blocking`. Verified with `go test ./appserver -run CodexHomeMetrics -count=1`; the three pre-existing analytics timing failures (`TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust`, `TestRuntimeRouterFileChangeAnalyticsIncludesUserReviewSummaryLikeRust`, `TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust`) reproduce unchanged in a clean `1ffdc32b` worktree.

### Rate-limit, Guardian V2, usage, and sandbox label parity (Round 774)

Closed six audits, three with new code. `#42142` adds the Plus/Team five-hour 50% rate-limit warning (`tui/chatwidget/rate_limits.go` now uses thresholds `[50,75,90,95]` gated by plan and window, with skipped thresholds never replaying). `#42144` gates `guardian_v2_enabled` turn analytics on managed requirements (`guardianV2AnalyticsEnabled`), so a model forced into auto-review no longer reports Guardian V2 as enabled. `#41980` gained regressions proving the raw `response.usage` object travels through `ResponseUsageMetadata.metadata` into `rawResponse/completed`. `#41933` is already configuration-derived in Go: `safety.PermissionProfilePolicyTag`, `sandbox.SandboxPolicyTag`, and `appserver.permissionProfilePolicyTagFromProfile` never inspect the filesystem, locked by a test with a non-existent configured writable root. `#41974` (app_server_mode on `codex.tui.start`) and `#42039` (single-mode compression over sessions and archived sessions, removed shared-compression key still accepted, representation-agnostic rollout reads) match Rust without changes. `#42137` (prewarm shell snapshots) and `#42104`'s TUI retention/cold-resume behavior remain open; `#42104`'s recent-session seed limit was raised to Rust's 20.

### sync: honor app link settings in MCP tool approvals (#42056, #42133, #42134) (Round 775)

Closed the app-link approval trio. `#42056` passes the resolved Codex Apps link into `AppToolPolicy` (link `default_tools_approval_mode` sits between per-tool and app defaults) and resolves `approvals_reviewer` from link, then connector, then `_default`, gated by managed requirements, while elicitation routing keeps the link unset as Rust does. `#42133` scopes remembered session approvals by server, connector, tool, and link, so a different link or the legacy no-selector path asks again. `#42134` adds `link_id` and `link_is_implicit` to native approval elicitation meta using the reserved `implicit_link::` prefix; the persistent-approval meta also carries `link_id`. Coverage lives in `mcp/tool_approval_link_like_rust_test.go` and `appserver/mcp_app_link_settings_like_rust_test.go`. Next up: `#42149` Git marketplace upgrades, `#42207` TUI reconnects while threads close, `#44746` startup folder trust, and the remaining `#42104`/`#42137` audits.

### sync: upgrade Git marketplaces from merged configuration (#42149) (Round 776)

Closed `#42149` end to end. Explicit upgrades now select Git marketplaces from the effective merged configuration (system/managed/project plus CLI layers) and stage the download before activation; the initiating loader is reused for a final definition check, so a changed source/ref/sparse set, a removed marketplace, or an unreadable config rolls back to the previous snapshot instead of activating it. App-server startup runs the same synchronization as a background task with automatic Git isolation and refreshes derived plugin state only when roots changed; if the initial user config is invalid it keeps Rust's defaults-loader fallback (`ConfigService.ReadDefaults` ignores user and project layers while retaining packaged defaults and managed configuration). The CLI upgrade preserves `-c` overrides through both selection and the activation reload. New coverage: `plugin/marketplace_upgrade_like_rust_test.go`, `appserver/marketplace_upgrade_config_test.go`, and the CLI override case in `app/plugin_cli_test.go`.

### sync: classify TUI closing-thread reconnect retry as architecture-gap N/A (#42207) (Round 777)

Audited `#42207` and recorded it as not applicable. The upstream change only narrows the retry predicate inside Rust's automatic app-server reconnect loop. Go's TUI has no in-place out-of-process reconnect transition — `#41916` and `#41918` were already closed on that architectural difference: remote turns open a fresh per-turn connection, explicit resume failures return to the session picker, and there is no `finish_reconnect`/unavailable-thread state where this retry could apply. Verification confirmed that a `-32600` `thread ... is closing;` response is classified as a server RPC error, not a transport failure, and never enters the voice-only disconnect path; `app/remote_tui_reconnect_like_rust_test.go` now freezes that classification. Next: `#44746` startup folder trust.

### Resolve folder trust after the startup destination (Round 778)

- `app/remote_trust.go`: `config/read` now carries the destination cwd, and local connections resolve the folder trust target through project markers, the Git root, and canonical-before-original keys (cwd decision > project root > Git root), honouring a disabled project layer's own target and skipping consent for projectless folders and enabled project layers.
- `app/remote_trust.go`: a remote cwd inside an explicitly untrusted project returns Rust's `pass the repository root explicitly with --cd` error, and explicitly untrusted folders open restricted without persisting a decision.
- `app/remote_trust.go`: `checkDirectoryTrust` covers the resolved destination plus a local daemon resume's saved folder, flags the existing-task disclosure, and re-reads the task after consent.
- `app/remote_tui.go`: consent runs before server settings are read, so a persisted decision is part of the starting configuration; agents overview worktrees use the local host lookup.
- Tests: `config/project_trust_lookup_keys_test.go`, `app/remote_trust_destination_like_rust_test.go`; `go test ./config`, `go test ./app -run 'ReadRemoteProjectTrust|EnsureRemoteProjectTrust|CheckDirectoryTrust'`, and `go test ./parity` pass.

### Audit Rust dependency-only bump (Round 779)

- Rust #44747 only bumps Rust dependency plumbing (`quinn-proto` 0.11.14 -> 0.11.15 in `Cargo.lock`/`MODULE.bazel.lock`) and allowlists the pinned `hyperium/h3` Git source in `codex-rs/deny.toml`; it changes no product behavior.
- Go's HTTP/3 CONNECT tunnel uses `github.com/quic-go/quic-go` v0.59.1 (`tcptunnel/tunnel.go`), so there is no quinn-proto/h3 dependency, Cargo/Bazel lockfile, or cargo-deny allowlist to update.

### Render task details Markdown with the task cwd (Round 780)

- `tui/agents_overview`: `View.RenderMarkdownCWD` now takes precedence for task-details previews, so the prompt and last message render at the pane width with the task's own working directory (Rust passes `Some(row.thread.cwd)` for both) and relative local file links resolve like in the thread.
- The prompt stays limited to two rendered lines with an ellipsis; the last message keeps every rendered line; a declining cwd-aware renderer falls back to the model-level renderer.
- `tui/tea`: the dashboard wires the cwd-aware renderer to `markdown.RenderWithThemeCwd`.
- Tests: `tui/agents_overview/details_markdown_cwd_like_rust_test.go`, `tui/tea` `TestWireAgentsOverviewSetsCWDMarkdownRenderer`; `go test ./tui/agents_overview ./tui/tea -run 'Details|AgentsOverview'` passes.

### Audit TUI folder-consent dispatch integration (Round 781)

- Rust #44755 wires the folder-consent screen into command-center dispatch, resume, and loaded-task opening, plus its cancellation UX (back to the command center, cleared launch prompt/images, restored composer draft, startup-hooks review after trust is saved).
- Go renders no onboarding consent screen: those paths attach to the server-owned task and the connected server already applies the restricted state, so there is no consent step to run, cancel, or re-dispatch - the same reason #54048 is not applicable.
- The decision machinery is covered: `checkDirectoryTrust` (destination + saved folder, restricted no-persist, existing-task flag) and `tui/onboarding.TrustDirectoryPrompt`'s connected/restricted/existing-task/keep-current-directory states.

### Keep projectless folders from preapproving project config (Round 782)

- Go's loader classifies a folder as projectless only without a project-root marker, Git checkout, or project-local config directory, and `thread/start` never writes an implicit `projects.<cwd>.trust_level`, so there is no implicit trust path to skip.
- New contract `config/projectless_trust_persistence_like_rust_test.go`: a projectless read adds no project layer and writes no trust, project configuration added afterwards stays unloaded, and a saved trusted decision still loads it.
- The connected trust driver still skips both the prompt and `config/batchWrite` for projectless folders.

### Pin explicit project trust lookup paths (Round 783)

- `ProjectTrustLookupFromPaths` already takes explicit `ProjectTrustPath` inputs plus an optional repository root and builds keys without filesystem access, so root canonicalization stays deferred (Rust #47924).
- Added the missing precedence case: a cwd entry without a `trust_level` still wins over a trusted repository root and reports no decision; removing it falls back to the root.

### Share theme-based thread colors with the status line preview (Round 784)

- `tui/tea`: the status line setup preview now renders with the footer's accent resolver and per-thread identity color (`statusLinePreviewStyled`), so thread-name/thread-title previews match the live status line while a thread is renamed or auto-named; `tui.status_line_use_colors=false` suppresses every span color in both places.
- `tui/thread_color.go` caches accents per resolved syntax theme, so a theme switch picks that theme's palette; `ResetThreadColorPaletteCache` reloads deterministically.
- Regression: preview/footer styled equality and identity-color fallback (`tui/tea/status_surface_preview_color_like_rust_test.go`), palette follow/reset (`tui/thread_color_test.go`), identity suppression on a disabled line (`tui/bottom_pane/status_line_style_test.go`), plus the existing picker and agents-overview color tests.
- Startup pickers already open from the theme-aware tea model, so Rust's pre-render theme load has no Go counterpart; `go test ./tui ./tui/bottom_pane ./tui/agents_overview ./tui/chatwidget ./tui/tea` passes.

### Cancel nested code-mode calls with a released context (Round 785)

- `tool/code_mode_exec.go`: the remote delegate refuses a nested call whose originating execution was released, answering Rust's `code mode nested tool call cancelled` instead of dispatching it against the resuming turn; closed cells are remembered (bounded FIFO) and a restarted cell id clears the marker.
- `appserver/guardian_reviewer.go`: denial and non-denial accounting resolve the thread's active turn, so a review dispatched by a cell from an earlier turn counts against - and can interrupt - the turn running now; an empty active turn records nothing.
- Architecture gap: Go passes MCP approval metadata inline with the approval request, so no turn-scoped registry/weak-map lifetime is needed, and it emits no legacy `McpToolCallBegin/End` events - nested lifecycle callbacks stay on the originating turn's dispatcher.
- Tests: `tool/code_mode_nested_cancel_like_rust_test.go`, `appserver/guardian_active_turn_denials_like_rust_test.go`; `go test ./tool ./turn`, `go test ./appserver -run Guardian`, and `go build ./appserver ./tool ./turn ./exec ./app` pass.

### Code-mode notifications keep their originating budgets (Round 786)

- `turn/tool_dispatcher.go`: a Code Mode cell freezes the originating model's `with_serialization_allowance` token budget when its exec call starts, bounds each notification item to it with Rust's `truncate_text(Tokens(limit))` shape, and keeps the full text on the raw `OnCodeModeNotify` callback. The budget rides on `ToolResponseItem.HistoryTruncationTokenLimit`, which `MarshalJSON` keeps out of the model request.
- `turn/runtime.go`: a yielded cell outlives the exec call that started it, so a later notification is queued as pending input for the running turn (`inject_if_running`) through the steer mailbox; previously it landed in a per-call buffer that had already been drained and was lost. A finishing or closed turn rejects the injection.
- Architecture note: Go applies the saved budget while building the notification item rather than during Rust's record-time truncation pass, and its session/rollout builders do not mint a separate rollout entry for a notify response item.
- Tests: `turn/code_mode_notify_budget_like_rust_test.go` covers the originating budget across a model switch, raw full text, late delivery after exec returned, the mailbox gate, and the model-request sidecar; `go test ./turn ./tool` and `go build ./turn ./exec ./appserver ./app` pass.

### Model discovery keeps access-program metadata across refreshes (Round 787)

- `model/catalog.go` / `model/api.go` already carried `available_access_programs` through `ModelInfo`, `ModelPreset`, and the app-server `model/list` shape: unknown cyber names are dropped while decoding, absent/null metadata stays `nil`, and an empty `cyber` array stays distinct and serializes as `availableAccessPrograms: null` versus `{"cyber":[]}`.
- New regression `model/access_programs_test.go`: two online refreshes with an unchanged catalog ETag replace the access metadata in the presets and in the persisted models cache, mirroring Rust `online_refresh_updates_access_programs_with_unchanged_etag`.
- Tests: `go test ./model -count=1`; `go test ./appserver -run 'ModelList|ModelSummary|ModelCatalog' -count=1`.

### Wire up the native Windows MXC helper entry point (Round 788)

- `cmd/internal/codexmain` now dispatches `--__codex-windows-mxc` before ordinary CLI parsing: the internal helper table moved into `dispatchInternalHelper` and the MXC branch sits after the argv0 aliases and before the file-system helper, mirroring Rust `arg0_dispatch`. The helper consumes no further arguments and returns the child exit code.
- `sandbox/mxc.RunMain` (Rust `run_main`) prints `MXC sandbox execution requires Windows` and exits 1 off Windows, and reports every other failure as `MXC launcher: <error>`; `DecodeCommandArgs` now collapses every transport/serde failure into Rust's opaque `invalid MXC launcher request` so payload values never reach stderr.
- The Windows helper reads the filtered wrapper environment through `GetEnvironmentStringsW` with strict UTF-16 validation - unpaired surrogates in names or values are rejected instead of being silently replaced by Go's lossy decode - skips the per-drive `=` entries, then resolves the command cwd, volume roots (including UNC roots) and platform read roots.
- `sandbox/mxc/request_windows.go` adds `NativeRequest`/`BuildRequest` mirroring Rust `policy::build_request`: CreateProcess quoting of the exact argv, `working_directory` from the helper cwd (separate from `sandbox_policy_cwd`), the filtered child env or nil, `registryRead`, the readwrite/readonly/denied lists from `PrepareFileSystemPolicy`, the network policies with `network_specified`/`network_mode_specified`, and Rust #44903's desktop UI policy (`disable=false` plus `base_process_ui isolation=desktop`) that keeps clipboard, input-injection and system-control restrictions.
- Architecture gap: Go has no binding to MXC's native SDK, so `launch` fails closed with `native MXC is unavailable on this executor` after the whole request is validated, matching the existing executor-side gate instead of degrading to a weaker backend.
- Tests: `sandbox/mxc/run_test.go`, `run_windows_test.go`, `run_other_test.go`, `request_windows_test.go` (Rust wrapper regression: exact argv, Windows quoting, separate policy/command cwd, explicit env forwarding), and `cmd/internal/codexmain/dispatch_mxc_test.go`; `go test ./sandbox/... ./cmd/... -count=1` and `go build ./cmd/... ./sandbox/... ./execserver/...` pass.

### Keep voice meter history through quiet samples (Round 789)

- `tui/tea/model.go`: `pushVoiceMeterSample` no longer zeroes every earlier microphone/speaker intensity when the current frame is quiet. The sample is appended like any other frame, so earlier speech stays visible until it scrolls out of the 12-frame `VoiceMeterHistoryCapacity` window (Rust #44928 removed the same release loop); this supersedes the #43695 note that quiet channels clear their meter history immediately.
- The mute path still renders no level because the strip forces the microphone meter to zero while `MicrophoneMuted` is set, so mute feedback is unchanged.
- New regression `tui/tea/voice_test.go` `TestModelVoiceMeterHistoryKeepsQuietSamplesLikeRust`: a loud microphone frame followed by a quiet microphone / speaker-only frame keeps the earlier peak and still renders activity, the two channels stay independent, and a full quiet window settles both the history and the rendered meter to silence.
- Rust's insta snapshot for this case only changed in content, so the Rust TUI snapshot manifest file counts are unaffected; `go test ./tui/tea -count=1`, `go test ./tui/chatwidget -run 'Voice|Meter' -count=1` and `go test ./tui/bottom_pane -run Voice -count=1` pass.

### Drop TUI world-writable scans that Go never had (Round 790)

- Rust #44933 deletes TUI-local Windows world-writable scanning: the startup scan in `app/startup.rs`, the permission-change scan in `app/event_dispatch.rs`, the `OpenWorldWritableWarningConfirmation`/`StartupWorldWritableScanCompleted`/`SkipNextWorldWritableScan`/`UpdateWorldWritableWarningAcknowledged` events, the warning dialog and acknowledgement state in `chatwidget/windows_sandbox_prompts.rs` and `chatwidget/settings.rs`, the permission-shortcut gate, the scan telemetry helper, and the color-probe wait on `startup_world_writable_scan_pending`.
- Go's TUI never implemented any of it: `rg -ni 'world.?writable' tui/` matches only the app-server notification routing name `ServerNotificationWindowsWorldWritableWarning`, which Rust keeps too, and `tui/terminal_default_colors*.go` probes OSC 10/11 directly with no startup-scan gate.
- `hide_world_writable_warning` still exists on both sides (Rust `config/src/types.rs`, Go `config/config.go`), and the server-side audit/warning path (`sandbox/windowssandbox`, `sandbox/windows.go` `WindowsSandboxWarning`, app-server `windows/worldWritableWarning`) is retained by Rust after this commit, so it stays in Go as well.
- Nothing to remove or add; no new regression is meaningful because the deleted behavior has no Go surface.

### Confirm connector auth failure detection without an install URL (Round 791)

- #44938 was already synced in `39c808d4`: `mcp/auth_elicitation.go` exports `IsConnectorAuthFailureFromToolResult`, backed by the shared `connectorAuthFailureMetadata` validation that `ConnectorAuthFailureFromToolResult` also uses - error result, explicit auth-failure flag, and a nonempty connector id that does not conflict with the metadata. Detection therefore no longer needs an install URL while the elicitation parser still does.
- The trusted classification reaches the real path: `mcp/tool_executor.go` checks `IsConnectorAuthFailureFromToolResult` on the raw Codex Apps result before `maybeRequestCodexAppsAuthElicitation` can rewrite it (Rust #45716 ordering).
- Regression coverage mirrors Rust's three cases - detection without an install URL, rejection of missing/mismatched connector ids, non-error results and a false auth flag, and detection for `missing_link`, `oauth_upgrade_required` and `reauthentication_required` - plus the executor-boundary test; `go test ./mcp -run ConnectorAuth -count=1` and `go test ./mcp -count=1` pass.

### Order grouped agent tasks newest first (Round 792)

- Model grouping was already present in `tui/agents_overview`: the `Ctrl+S` cycle (project -> directory -> status -> model), `GroupingModel`/`ModelName` with `Unknown` for a missing or empty model, the `group: model` footer label, the `Model:` detail line, `SetRowModel` on thread-settings changes, and new-task dispatch that inherits the selected cwd only for project grouping.
- The remaining gap was ordering: Rust sorts keyed groupings by `(key, Reverse(updated_at))`, so the newest task leads each group, while Go sorted only by key and relied on the host list order. `stableSortByGroupKey` became `sortByGroupKeyThenRecency` and now applies to project, directory and model grouping, with equal timestamps keeping host order.
- New regression `tui/agents_overview/model_grouping_recency_like_rust_test.go` mirrors Rust's `overview_model_grouping_shows_details_and_preserves_selection`: host order deliberately disagrees with recency, model grouping yields `Unknown(t-4)`, `model-a(t-3 newest, t-1)`, `model-b(t-2)`, `sameGroup` follows the model rather than host position, the selection survives the regroup, and `MoveSelection` walks the grouped order across group boundaries.
- `go test ./tui/agents_overview -count=1`, `go test ./tui/tea -run 'Agents|Agent' -count=1`, `go test ./app -run 'Agents|Agent' -count=1` and `go build ./tui/... ./app/...` pass.

### Show task tokens and usage estimates in the command center (Round 793)

- #44970 was already implemented. `tui/tea/agents_overview_usage.go` mirrors Rust `agents_overview_usage.rs`: a 60s cadence fetches only the selected task, gated on a ChatGPT account, the disabled capability, the Business/ENT26/Enterprise-CBP plan set, an in-flight request, and a fresh cached `fetchedAt` (scheduling the remaining time instead of refetching).
- `applyAgentsOverviewUsageLoaded` keeps a prior nonzero credits/USD value and its breakdown groups when a settlement reports zero, latches `usageDisabled` on the disabled outcome while clearing every cached estimate, stamps `fetchedAt` on failures so the cadence retries, and ignores results whose pending thread/request id no longer matches. `clearAgentsOverviewUsage` drops the cache, disabled flag and pending request on account changes, reconnects and missed events.
- `agentsOverviewUsageLines` prefers live token totals and otherwise sums the complete breakdown side, rendering `Tokens: 13K in · 4K out` and `Est. usage: 3.4 credits · ~$0.14` (ENT26 uses native dollars). `tui/agents_overview` renders those lines after the model row and prefers usage over the original prompt when height is tight.
- Existing tests cover fetch/render, zero-preservation, the disabled capability, plan gates, plan-change cache clearing, incomplete breakdowns and ENT26 dollars; this round adds `TestAgentsOverviewUsageDiscardsStaleResultsAndRetriesAfterFailure` to pin the reconnect/account-change stale-result discard and the retry-after-failure cadence.
- `go test ./tui/tea -run 'AgentsOverviewUsage' -count=1`, `go test ./tui/tea -run Agents -count=1`, `go test ./app -run Agents -count=1` and `go test ./tui/agents_overview -count=1` pass.

### Delay and compact recaps while previewing streaming prose (Round 794)

- #45089 was already implemented. `tui/app/recap.go` keeps `RecapDelay = 30m` and `RecapRetryDelay = 30s` in the focus-loss/turn-count deadline state machine, `tui/history_cell/recap.go` renders the compact italic hanging `  ↳ Recap:` layout with a two-column right pad, preserved source line breaks, Unicode-safe wrapping and a `BreakWords` fallback that keeps URLs intact until they overflow, and `app/recap.go` no longer overrides `token_budget.use_history_notes_extension` on temporary structured requests.
- This round adds `TestRecapDelayMatchesRustThirtyMinutes` and fixes the two mis-encoded `↳` label comments; the existing narrow-terminal, Unicode, URL-wrap and fallback-header tests already pin the layout.
- #45090 was already implemented. `RecapPrompt`/`RecapPromptPrefix` share the 8,192-token (32 KiB) byte ceiling, `RecapHistory` selects up to eight answered exchanges plus a pending correction, drops older whole exchanges before excerpting both ends, and `ParseRecap` requires a `summary` plus nullable `next_action` bounded to 700/200 characters with the matching output schema; `ThreadRecapHistoryCell.WithNextAction` renders the separately wrapped `Next:` line.
- #45135 was already implemented. `tui/streaming` keeps a disposable `prosePreview` bounded to the newest 8 KiB at a rune boundary with a leading ellipsis, never enqueues it into the stable queue, reflows it on resize, drops it on a committed newline, retains the last safe preview when a pipe reveals table structure, and holds back fences; `pushDelta` reports preview changes and `currentTailLines` appends the preview to the live tail. Existing controller tests cover visibility before completion, finalization without duplication, bounds, table holdback and resize reflow.
- Tests: `go test ./tui/app ./tui/history_cell ./tui/tea ./tui/streaming -count=1` passes.

### Attribute request metadata and hooks to the issuing step (Round 795)

- #45248 needed two gaps closed. The agent loop already refreshed `AgentStepSettings` per sampling step, but `ToolDispatcher` only used it for truncation: the hook adapter kept the turn-start model and approval policy. `AgentStepSettings` now carries `HookPermissionMode`, the loop calls `SetStepSettings(model, effort, permissionMode)` before each step's tool batch, and the dispatcher stamps a `tool.StepAttribution` onto every invocation so parallel calls cannot race a shared adapter.
- `ToolHookAdapter` prefers the invocation's captured model/permission mode for PreToolUse and PostToolUse and keeps the adapter fallback for calls without a captured step; `PermissionRequest` hooks resolve the running turn's step model and effective approval policy before dispatch. `turn.Runtime.ToolNamespacesInfo()` exposes the issuing router's finalized inventory, and `compactResponsesClientMetadataForWindow` now writes it into the remote-compaction turn-metadata document without disturbing the compaction fields.
- New regressions: `TestAgentLoopUsesCapturedStepSettingsForToolHooksLikeRust` (two steps, model/effort/approval change mid-turn), `TestToolHookAdapterReportsInvocationStepAttributionLikeRust` (hook stdin plus partial/absent fallbacks), and `TestAttachToolInventoryToCompactionMetadataLikeRust`.
- Tests: `go build ./turn ./tool ./appserver ./app`, `go test ./turn ./tool -count=1`, and `go test ./appserver -run 'Hook|Compact|StepSettings|StepCapabilities' -count=1` pass.

### Dispose unported packaged sandbox cleanup and inline scrollback (Round 796)

- #45178 is N/A by architecture. The commit only splits Rust's packaged Windows sandbox cleanup into `prepare_packaged_windows_sandbox_cleanup` plus a `PreparedWindowsSandboxCleanup` guard whose `finish` removes resources, and extracts the service's `package_lifecycle/cleanup.rs`. Go has no `windows-sandbox-service`/package-lifecycle path and no packaged-install cleanup carrier: `sandbox/windowssandbox/uninstall.go` exposes only `CleanUpLegacyWindowsSandbox`, and `uninstall_windows.go` records that the setup mutex, installation record, and account disable/wait primitives are unported.
- #45271 is N/A by architecture. Rust changes `ScrollbackStrategy::Standard` viewport growth to emit newlines inside a `DECSTBM` history region so QTermWidget/xterm.js keep scrollback. Go's TUI has no inline-history viewport or scroll-region writer (no `ScrollbackStrategy`/`grow_viewport`/`DECSTBM` carrier); transcript insertion and resize reflow live in `tui/app/resize_reflow.go`, and the bubbletea renderer owns the screen.
- Verification: `rg 'ScrollbackStrategy|grow_viewport|PreparedWindowsSandboxCleanup|prepare_packaged|package_lifecycle' --glob '*.go'` finds no carrier, and the existing `sandbox/windowssandbox` legacy cleanup and `tui/app` resize-reflow tests remain unchanged and green.

### Confirm MCP request metadata carries session and origin window (Round 797)

- #45409 was already implemented. `mcp/api.go` `withMCPToolCallIDsMeta` replaces stale `threadId`/`sessionId`/`windowId`/`itemId` values in the `tools/call` `_meta` document and passes a non-object meta through; `appserver/runtime_router.go` fills `SessionID` from the thread record and `WindowID` from the current conversation window, and `tool/code_mode_exec.go` keeps `OriginItemIDContextKey` and `OriginWindowIDContextKey` on a cell across waits and compaction.
- Existing regressions: `TestRuntimeRouterMCPRequestMetadataReportsCallIDsLikeRust` (direct MCP call metadata over a real loopback server), `TestMCPToolCallMetaCallIDsLikeRust` (merge, omitted item, session-less call, non-object passthrough), and the code-mode origin tests for resumed cells. `go test ./appserver -run MCPRequestMetadata`, `go test ./mcp -run MCPToolCallMetaCallIDs`, and `go test ./tool -run Origin` pass.

### Cancel code mode timers cleared after firing (Round 798)

- #45399 was a real Go gap. Go scheduled each `setTimeout` with `time.AfterFunc` and only decremented its `pending` counter when `time.Timer.Stop` succeeded, so a timer that had already fired but whose completion had not been consumed still invoked its callback after `clearTimeout`, and the counter drifted.
- `tool/code_mode_exec.go` now treats membership in the timer map as the source of truth: `clearTimeout` always stops and removes the timer and decrements `pending`, and the completion loop drops a queued fire whose timer is gone. That matches Rust, where `clear_timeout` removes `pending_timeouts` regardless of whether the Tokio sleep already fired, and `invoke_timeout_callback` then finds nothing. Cell completion keeps stopping every remaining timer through the existing deferred cleanup, mirroring the dropped `AbortOnDropHandle`.
- New regressions: `TestCodeModeExecClearTimeoutDropsQueuedTimerFire` (50 zero-delay `setTimeout`/`clearTimeout` rounds inside one synchronous block; fails against the old code with stray `must-not-run` output) and `TestCodeModeExecCellCompletionCancelsPendingTimers` (a 60s timer neither holds completion open nor emits output).
- Tests: `go test ./tool -count=1` passes, and the timer tests pass under `-race`.

### Reset Guardian reviewer after parent history replays (Round 799)

- #45413 was a real Go gap. Rust adds `ContextManager::reset_version` (bumped by destructive history replacement, preserved by ordinary input and compaction) and folds it into `GuardianReviewSessionReuseKey`, so a summary-free parent context reset starts a new reviewer trunk with no prior rationale.
- Go's reusable `guardianSessionRunner` kept its `previous` response chain and `priorReview` cursor across cold thread resumes and forks, both of which replay a thread's history into a fresh runtime (Rust's `HistoryReplacement::Reset`). The next review therefore reported `trunk_reused`/`had_prior_review_context=true` and carried pre-reset rationale.
- `guardianSessionRunner.ResetForParentHistoryReset` now clears `previous`/`seeded`, the prewarm flag, the prior-review cursor, the completed-review count and the last usage. `RuntimeRouter.resetGuardianAfterParentHistoryReset` is wired into a cold `thread/resume` (including resume-with-supplied-history) and into `thread/fork`; attaching to an already-loaded thread keeps the live reviewer, matching Rust's live-attach path.
- New regressions: `TestGuardianSessionRunnerSummaryFreeResetInvalidatesSession` (`trunk_new` -> `trunk_reused` -> reset -> `trunk_new`, with no previous response id and a zeroed completed-review count) and `TestRuntimeRouterColdResumeResetsGuardianSessionLikeRust` (cold resume over a real store; fails without the hook by reporting `trunk_reused`).
- Tests: `go test ./appserver -run Guardian` and `go test ./appserver -run 'ThreadResume|ThreadFork|Resume'` pass.

### Dismiss MCP status snapshot cloning as Rust-internal (Round 800)

- #45428 is a Rust-internal allocation change: the owned `McpServerStatusSnapshot` is destructured into the response so server metadata, tools, resources, templates and auth statuses move with `HashMap::remove` instead of `get().cloned()`. Output, pagination and the missing-entry defaults are identical.
- Go has no owned snapshot object. `MCPService.ListStatusCheckedWithObserver` builds a request-owned copy from the mutex-guarded `s.servers`/`s.configs` maps under `s.mu` (`cloneMCPServerStatus`/`cloneServerConfig`), and `populateStatusInventories` hands each startup worker its own clone before fanning out. Consuming the live service state in place is neither possible (the service keeps serving concurrent reads) nor safe, and dropping the copies would publish aliases of shared mutable state.
- The contract half of the change is already satisfied: a server missing from the inventory maps still reports empty `tools`/`resources`/`resourceTemplates` and the `Unsupported` auth status, and `paginateMCPStatuses` keeps the server-name filter and cursor behavior. Existing coverage: `TestListStatusAndToolCall`, `TestMCPStatusPaginationInvalidRequestErrors`, `TestMCPServerStatusDetailZeroValueMatchesToolsAndAuthOnly`, `TestMCPServerStatusWireIncludesStateAndError`.
- Tests: `go test ./mcp -run 'TestListStatusAndToolCall|TestMCPStatusPagination|TestMCPServerStatusDetailZeroValue|TestMCPServerStatusWireIncludesStateAndError' -count=1` passes.

### Report model default reasoning effort in tool analytics (Round 801)

- #45445 was mostly present already: `codex_command_execution_event` and `codex_plugin_measurement_event` carry `model_slug`/`reasoning_effort`, `RuntimeRouter.rememberToolItemModelContext` captures the invoking step at item start and keeps the first context (so a repeated start notification or a mid-run model switch cannot retarget the event), the carried context stays in a router-side map rather than the serialized protocol items, and `tool/shell_executor.go` stamps `PluginMeasurementBatch.ModelSlug`/`ReasoningEffort` for plugin metrics.
- The gap was Rust's `effective_reasoning_effort()` fallback: Go only used the explicit run-config effort, so a default-effort turn emitted `reasoning_effort: null` where Rust reports the model's default reasoning level.
- `RuntimeRouter.modelInvocationContextForRunConfig` now consults the model catalog's `default_reasoning_level` when the step selected no effort, and both the start-time capture (`modelInvocationContextForTurn`) and the completion-time fallback in `emitCommandExecutionAnalyticsEvent` resolve labels through it.
- New regression `TestCommandExecutionAnalyticsReportsModelDefaultReasoningLikeRust` (fails before the fallback with an empty effort); existing `TestCommandExecutionAnalyticsUsesTheInvokingModelLikeRust`, `TestPluginMeasurementEventCarriesTheInvokingModelLikeRust` and `TestShellExecutorPluginMeasurementsCarryTheInvokingModelLikeRust` cover the rest.
- Tests: `go test ./appserver -run 'ModelAttribution|CommandExecutionAnalytics|PluginMeasurementEventCarries' -count=1` passes. The two fileChange analytics failures visible under a broader `Analytics` filter reproduce with this change reverted, so they are pre-existing.

### Route clipboard copies through tmux and reject empty selections (Round 802)

- #45457 was partly synced already (`8521fa23`, `b6d1171a`): native copying runs first, tmux/Herdr/SSH sessions still forward after a successful native write, and OSC 52 is the fallback when the native write fails. Three gaps remained.
- `tui/clipboard.go` now rejects an empty selection with Rust's `nothing to copy: the selected content is empty` before any backend runs, and `terminalClipboardCopyWith` applies the 100,000-byte terminal payload limit to tmux copies (`tmux clipboard payload too large (N bytes; max 100000)`), keeping the OSC 52 fallback and Rust's combined `tmux clipboard: ...; OSC 52 fallback: ...` error.
- New `tui/clipboard_tmux.go` mirrors `clipboard_copy/tmux.rs`: `TmuxClipboardCopy` resolves `tmux` and `PATH` through trusted system locations, requires `TMUX_PANE`, checks `show-options -gv set-clipboard`, resolves the pane's `#{session_id}`, lists `#{client_activity} #{client_name}` for that session, picks the last most-active client (Rust's `max_by_key` tie-break) and requires a non-empty `Ms: (string)` capability before `load-buffer -w -t <client> -` with the text on stdin. `TmuxClipboardTarget` is the injectable core, replacing the unused `TmuxClipboardCopyReady` helper.
- `WriteTerminalClipboard(text, true)` now forwards through that backend (Rust's tmux branch) instead of always writing a DCS-wrapped OSC 52 sequence; the outer routing layer keeps OSC 52 as the fallback and passes the tmux session through, and the non-tmux path still wraps when `TMUX` is set, matching Rust's `osc52_copy`.
- `envutil` trusted helper discovery adds the Nix system profiles (`/run/current-system/sw/bin`, `/nix/var/nix/profiles/default/bin`, both resolving into the trusted `/nix/store` root) and the WSL PowerShell directory (`/mnt/c/Windows/System32/WindowsPowerShell/v1.0`).
- New regressions: `TestTmuxClipboardTargetAcceptsForwardingConfigurationLikeRust`, `TestTmuxClipboardTargetRejectsDisabledForwardingLikeRust`, `TestTmuxClipboardTargetRequiresNonemptyMsCapabilityLikeRust`, `TestTmuxClipboardTargetRejectsNoAttachedClientLikeRust`, `TestTmuxClipboardCopyTargetsMostActiveClientAndSendsStdinLikeRust` (exact argv sequence plus stdin payload), `TestTmuxClipboardCopyRequiresCurrentPaneLikeRust`, `TestClipboardCopyRejectsEmptySelectionWithoutTouchingClipboardsLikeRust`, `TestClipboardCopyWithOversizedTmuxPayloadMatchesRust` (exactly at the limit vs one byte over), `TestClipboardCopyWithOversizedTmuxPayloadReportsBothBackends`, `TestClipboardCopyWithTmuxFailingFallsBackToOSC52LikeRust`, and `TestTrustedExecutableDirectoriesIncludeNixAndWSLPowerShellLikeRust`.
- Rust's `chatwidget/interaction.rs` lease-retention change is not applicable: Go's clipboard writers own no X11 ownership handle, so there is no `clipboard_lease` to retain across a terminal-only copy.
- Tests: `go test ./tui/... ./envutil -count=1` and `go vet ./tui ./envutil` pass.

### Own thread persistence acquisition until resume commits (Round 803)

- #45487 was mostly covered by structure rather than by an owned acquisition: `session.LiveThreadInitGuard` already existed, but `ThreadManager.RetainLiveThread` registered the thread (and its writer) as live the moment it opened persistence, so every later failure in `thread/resume` left the writer installed for a thread the client never received. Go has no await point between acquisition and registration, so Rust's exact cancellation window cannot occur; the equivalent contract is guard-owned acquisition until the request succeeds.
- `appserver/thread_manager.go` adds `LiveThreadRetention` plus `AcquireLiveThreadRetention`: the retention owns the `LiveThreadInitGuard` while the caller initializes, `Commit` registers the thread as live (and fires the retained hook), and `Discard` releases the writer. `RetainLiveThread` keeps its previous atomic acquire-and-register body.
- `appserver/router.go` now uses the retention for both resume shapes Rust routes through the guard: a cold `thread/resume` and a supplied-history resume (Rust's inherited-context creation). Named results let the deferred cleanup commit on success and discard on any error; the supplied-history path also removes the created record and rollout it leaked on failure.
- New regressions in `appserver/thread_persistence_acquisition_like_rust_test.go`: `TestLiveThreadRetentionDiscardsWriterUntilCommitLikeRust` (a paginated acquisition holds the writer, an in-process re-acquire fails, discard releases it and leaves no live thread, and commit re-takes ownership), `TestLiveThreadRetentionIsIdempotentForLiveThreadsLikeRust`, `TestThreadResumeHistoryAcquiresPersistenceLikeRust` (a supplied-history resume retains persistence, which it previously never did), and `TestThreadResumeHistoryDiscardsPersistenceOnFailureLikeRust` (an unwritable sessions directory fails rollout creation; the writer is discarded and the record is removed instead of leaking).
- `TestThreadResumeHistoryDiscardsPersistenceOnFailureLikeRust` fails against the previous code by leaving the record behind.
- Tests: `go test ./appserver -count=1` shows only the three pre-existing analytics/fingerprint failures (`TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust`, `TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust`, `TestRuntimeRouterFileChangeAnalyticsIncludesUserReviewSummaryLikeRust`), which also fail with these changes reverted. `go test ./session -count=1` passes.

### Report the running login policy in config requirements (Round 804)

- #45495 was partly present: `config.ProjectRequirementsWithAllowedLoginMethods` already projected `allowedLoginMethods` (including `requirements: null` for the unrestricted default), and `RuntimeRouter.configRequirementsRead` already composed managed allowlists, the forced login method, and the ChatGPT workspace intersection.
- Three behaviours were missing. `configRequirements/read` reported the *cached* requirements instead of re-reading the managed file, so a requirements file written after startup never reached the response and an invalid one never failed the call; an invalid `allowed_login_methods` entry was silently dropped instead of failing the load; and an explicit empty `allowed_chatgpt_workspaces` list collapsed to `nil` and therefore read as unrestricted.
- `config/requirements_file.go` now returns `invalid allowed_login_methods value "saml": expected api or chatgpt` for unknown methods (Rust deserializes the enum) and keeps an explicitly empty workspace allowlist non-nil.
- `config.ConfigService` freezes the requirements its login policy was built from (`LoginPolicyRequirements`, refreshed by `SetRequirements`/`SetRequirementsIfDifferentFromLoaded`, not by `ReloadRequirementsFromHome`), so `configRequirements/read` can follow Rust's split: re-read the file for the response body, report the process-lifetime auth policy for the allowed methods. The handler propagates reload errors the way Rust's `map_error` does.
- Regressions added to `appserver/config_requirements_login_test.go`: the full Rust matrix (`TestRuntimeRouterConfigRequirementsReadMatchesRustLoginMatrix`: empty/absent forced workspaces stay null, unrelated requirements expose both methods, `["chatgpt","api","api"]` normalizes to `[api, chatgpt]`, forced ChatGPT without requirements, forced-narrowed managed allowlists, empty managed workspaces, disjoint/overlapping workspaces, api-only ignore of the workspace mismatch), `TestRuntimeRouterConfigRequirementsReadRejectsInvalidLoginMethodLikeRust`, and `TestRuntimeRouterConfigRequirementsReadUsesRunningLoginPolicyLikeRust` (a requirements file removed or rewritten after startup leaves `allowedLoginMethods` at `["api"]`).
- Before this change the empty-workspace and invalid-method cases failed; the running-policy case already passed because the service cached requirements.
- Tests: `go test ./config -count=1` passes; `go test ./appserver -count=1` shows only the three pre-existing analytics/fingerprint failures; `go test ./app -run 'Requirement|Login|Managed|RemoteTUI|Daemon|Workspace' -count=1` passes.

### Skip Rust-only TLS dependency bump (#45489) (Round 805)

- Upstream `e5a20948` is a dependency-only bump in `codex-rs/Cargo.lock` and `MODULE.bazel.lock` (rustls, rustls-webpki, aws-lc-rs, aws-lc-sys).
- codex_go has no Cargo/Bazel lockfiles and relies on the Go standard library TLS stack (`crypto/tls`); a repo-wide search finds no `rustls`, `aws-lc`, or `webpki` references in `go.mod`, `go.sum`, or build files.
- Marked `not_applicable`; no Go change or new test surface.

### Render inline TeX math as Unicode in the TUI (#45501) (Round 806)

- Inline and display TeX rendering was already ported, so this round closed the streaming boundary the commit pinned. Rust filters its incremental render-cache boundary so it never lands inside a display equation; Go renders the stable prefix from a source offset instead of a cached line prefix.
- `tui/markdown/math_spans.go`: added `MathDisplayTailStart(source, boundary, width)`, which reports the earliest display span start a stable boundary must retreat to. A span strictly crossing the boundary, or an unterminated standalone display still inside `maxMathBytes` (Rust's `pending_start`), holds the whole span back; inline math and oversized openers never hold.
- `tui/streaming/controller.go`: `activeTailSourceStart` now folds that boundary in, so an open display equation stays in the mutable tail and is never committed to scrollback in its verbatim form.
- Tests: Rust's wide and narrow inline snapshots, the boundary-filter cases, the file citation path between two inline formulas (`α/tmp/$x$/report.mdβ`), and streamed-vs-whole-source comparisons across `$$`/`\[` delimiters, widths 16/40, and a mid-stream resize.
- Verified with `go test ./tui/markdown ./tui/streaming ./tui ./tui/history_cell ./tui/chatwidget`.

### Verify revocable network policy primitives (#45503) (Round 807)

- Go's `network` package already carries every primitive from this commit, so the round was a regression pass rather than a port.
- Covered: `DestinationPolicy` (https/wss only, exact host, trailing-dot normalization), `NetworkPolicyController.Publish`/`Unavailable`/`Invalidate` with revision and account generations, revocable `NetworkPermit` (`Check`, `Revoked`, `RunWithNetworkPermit` cancellation), `AcquireForUnsupportedSDK` denying restricted policies, `RestrictToEndpoints` scoping, `ForCurrentAccount` binding, and `PolicyHTTPDoer`/`PolicyHTTPClient` carrying the policy into transports with an unmanaged default.
- Rust's `HttpClientFactory` has no Go counterpart (clients come from `PolicyHTTPClient`), so only its behaviour is mirrored; factory identity equality has nothing to compare.
- Verified with `go test ./network`, `go test ./app -run Network`, and `go test ./appserver -run 'NetworkPolicy|ApplicationNetwork'`.

### Verify the configurable Guardian prompt template (#45516) (Round 808)

- Already implemented end to end, so this round is a regression pass.
- `config.Config.GuardianPolicyTemplate()` reads `[auto_review] experimental_policy_template` through `autoReviewConfigValue`: the value is trimmed, and a blank value counts as unset so the catalog/bundled layers still apply.
- `appserver.guardianReviewInstructions` prefers the config override over the model catalog template and keeps the bundled `state.GuardianPolicyTemplate()` as the final fallback; `state.RenderGuardianPolicyInstructions` substitutes `{{ tenant_policy_config }}` with the resolved policy.
- Rust's `config.schema.json` has no Go counterpart, so the schema entry has nothing to mirror.
- Coverage: `config/guardian_policy_config_test.go` (deserialization, trimming, managed-vs-config precedence), `appserver/guardian_reviewer_test.go` (catalog-vs-config precedence, rendered policy text), `state/guardian_instructions_test.go` (placeholder rendering). Verified with `go test ./config -run Guardian`, `go test ./appserver -run Guardian`, `go test ./state -run Guardian`.

### Restore collaboration mode when resuming threads (#45519) (Round 809)

- The app-server half was already ported: `thread/resume` reports the restored `collaborationMode` (live snapshot, else the rollout snapshot, else the last legacy `TurnContext`), overlaid with the effective model and reasoning effort, and writes the overlaid snapshot back for the next resume.
- `app/remote_tui.go`: `remoteTUISettingsFromResume` now carries the response's `collaborationMode` into `appserver.Settings.CollaborationMode`, so a reconnecting client receives the server's mode instead of falling back to Default.
- `tui/tea`: `applyThreadSettingsValues` adopts the reported mode kind before the reasoning-effort assignment, so Plan mode is restored, the effort lands in the plan slot, and the first prompt after the resume is submitted with the restored mode. A response without a mode leaves the local selection alone, matching older servers.
- Tests: `TestResumeRestoresCollaborationModeLikeRust`, `TestResumeWithoutReportedModeKeepsLocalSelectionLikeRust`, `TestRemoteTUIResumeCarriesCollaborationModeLikeRust`.
- Known Go-specific gap: the in-process (embedded) resume path has no thread-settings snapshot to read because local turns do not yet persist one; the app-server-backed path is complete.
- Verified with `go test ./tui/tea ./tui ./tui/chatwidget` and `go test ./app -run 'RemoteTUI|ThreadSettings|Collaboration|Resume'`.

### Enable MXC TTY launches and managed networking in the exec server (#45524) (Round 810)

- Rust #45524 narrows the MXC request gate: TTY and managed networking are now accepted, only custom `argv0` and private-desktop launches are rejected, and a missing native MXC still fails closed.
- `execserver/sandbox_process_windows.go`: the MXC branch now rejects `params.Arg0 != nil || sandboxContext.WindowsSandboxPrivateDesktop` with `MXC custom argv0 and private-desktop launches are not supported` before the availability check, matching Rust's order, so every other MXC shape (pipes, TTY, managed network, network proxy) reaches `native MXC is unavailable on this executor`. The managed-network validation that used to run first was removed because Rust only validates it after the availability check, which Go never passes.
- `execserver/server.go`: `EnvironmentCapabilities` gained `windowsMxc`, always serialized like Rust. Rust gates it on `codex_mxc_sandbox::is_available()`; Go has no native MXC runner (`sandbox/mxc` always returns `NativeUnavailableMessage`), so `localEnvironmentInfo` reports false on every platform.
- `ManagedProxyRouting::DedicatedListeners` and the "dedicated listeners need no restricting SID" exemption are N/A in Go: the `network` package has no managed-proxy routing modes, and no MXC proxy route is ever created because the launcher never runs.
- Coverage: `execserver/windows_mxc_test.go` asserts the pipe, TTY, managed-network and network-proxy shapes fail closed, the new argv0/private-desktop message, and that the advertised capability serializes as `"windowsMxc":false`.
- Verified with `go test ./execserver -count=1`, `go test ./sandbox/mxc -count=1`, `go test ./tool -count=1`, and `go build ./...`.

### Stage Python runtime wheels from package directories (#45526) (Round 811)

- Rust #45526 is Python release tooling: `sdk/python/scripts/update_sdk_artifacts.py` accepts a package directory for `stage-runtime`, validates layout, rejects overlapping/symlinked/non-regular entries, and `rust-release-windows.yml` stages the runtime wheel from that directory.
- The Go repository has no `sdk/` tree, no `update_sdk_artifacts.py`, and no `stage-runtime` command; `.github/workflows` only carries `macos-platform-gates.yml`, `sqlite-platform-gates.yml`, `parity-baseline.yml`, and `release.yml`, none of which stage Python wheels.
- Recorded as not applicable: there is no artifact-staging code path in Go to change, and no regression target to compare against.

### Expose selected workspace routing in app-server account reads (#45529) (Round 812)

- The app-server half was already implemented: `account/read` carries `workspaceRouting` (workspace id, resolved HTTPS origin, `us`/`us_cr`/`NO_CONSTRAINT` override), `appserver/workspace_routing.go` discovers through `accounts/check` and caches per credential and owner generation, validates discovered origins against a required `chatgpt_base_url`, returns discovery errors that a later read retries, notifies initialized connections once routing is ready, and discards results from a superseded owner or configuration.
- One gap remained in the selection gate: Rust skips discovery entirely when `get_account_id()` is `None`, so a saved ChatGPT login without a selected workspace returns `workspaceRouting: null` and never calls `accounts/check`. Go reported `workspace routing requires a ChatGPT account id` for that credential.
- `workspaceRoutingForAccountRead` now returns nil routing for `chatgpt`/`chatgptAuthTokens` credentials without an account id, and keeps the error only for `personal-access-token` auth (the one mode Rust always gives an id).
- New regressions in `appserver/workspace_routing_selection_gate_like_rust_test.go`: `TestRuntimeRouterGetAccountWithoutSelectedWorkspaceReturnsNullRoutingLikeRust` (null routing with zero backend calls) and `TestWorkspaceRoutingRequiresPersonalAccessTokenAccountIDLikeRust`.
- The Rust outbound-notification owner guard is N/A in Go: the sink dispatch is synchronous, so there is no queue capacity wait during which the account can change.
- Verified with `go test ./appserver -count=1`, which shows only the three pre-existing analytics/fingerprint failures.

### Harden and share Windows sandbox identity helpers (#45533) (Round 813)

- Upstream hardens hand-written unsafe Windows FFI plus the Windows sandbox provisioning service. Go has neither the unsafe SID-copy code nor the service, and its existing helpers already provide the same invariants.
- Package identity: Rust adds a shared `process_package_family` bounded to 256 UTF-16 units for service client-vs-service comparison. Go has no provisioning service or package-family comparison; its query is `windowssandbox.QueryPackageName` (`GetPackageFullName`), which already bounds the allocation, requires a terminating NUL, rejects embedded NULs, and validates UTF-16.
- Token user SID: Rust bounds the `TokenUser` query and validates pointer, revision, and length before copying. Go's `getUserSIDBytes` uses x/sys `Token.GetTokenUser`, rejects nil/invalid SIDs, and `copySIDBytes` copies into owned storage; `SID.IsValid` already bounds the revision and subauthority count (so length <= 68).
- Account name: Rust's `account_name_from_sid` (SidTypeUser, non-empty domain, `DOMAIN\Name`) is service-only. Go's `LookupAccountNameForSID` resolves the built-in Users alias for group membership.
- Firewall COM: Go's `withFirewallRules` pins the OS thread, defers `ole.CoUninitialize()` immediately after a successful `CoInitializeEx`, and defers every release, which is the drop-guard behavior Rust introduced.
- Package lifecycle directory guard: service-only; Go has no package lifecycle.
- Verified with `go test ./sandbox/windowssandbox/... -count=1`.

### Honor explicit Unix socket grants in the Linux managed sandbox (#45534) (Round 814)

- The Linux helper already honoured the grant: `sandbox/linuxsandbox` parses `allowUnixSockets`/`dangerouslyAllowAllUnixSockets` out of `--managed-network`, and its proxy-routed seccomp rule admits `AF_UNIX` only when the allow-all grant is set. Nothing produced or forwarded that policy, though, so `dangerously_allow_all_unix_sockets` never reached the helper.
- `network.ProxyManagedNetworkSandboxContext` and `execserver.ManagedNetworkSandboxContext` now carry `allowUnixSockets` and `dangerouslyAllowAllUnixSockets` (always serialized like Rust, missing fields deserialize to the restrictive values). `PrepareProxyManagedNetwork` takes the effective `ProxySettings` and fills the allow list (allow entries only, sorted, `[]` when empty) and the allow-all flag; a Windows executor keeps both empty.
- `execserver.prepareExecProcess` now marshals the prepared context into `BuildCommandRunPlan`, and the helper argv receives it as `--managed-network`, including the alternate policy-CWD re-wrap branch. `tool/unified_exec.go` and `tool/shell.go` carry the fields through their mapping and clone helpers.
- Path-only grants stay denied: the sandbox helper only relaxes `AF_UNIX` for the explicit allow-all flag, and the transport never invents one.
- Tests: `network/proxy_unix_socket_context_like_rust_test.go`, `execserver/managed_network_unix_sockets_like_rust_test.go`, `execserver/managed_network_unix_sockets_linux_like_rust_test.go`, plus the existing helper parse/malformed-JSON coverage.
- Verified with `go test ./network ./execserver ./sandbox ./tool -count=1`, `GOOS=linux go vet ./execserver ./sandbox ./sandbox/linuxsandbox ./tool ./network`, and `GOOS=linux go test -c ./execserver`. The Linux test package did not compile before this round (`execserver/tty_unix_test.go` passed a `[]byte` chunk to `base64.DecodeString`); that stale test call was corrected so the Linux coverage could be built.

### Classify tool analytics events by call origin (#45535) (Round 815)

- Go already classifies tool analytics at emission time. `telemetry/command_execution_event.go` defines the `model_tool_call` and `inner_tool_call` values and exposes `tool_event_type` as a nullable field, so an unresolved call serializes as JSON `null`.
- `appserver/tool_event_origin.go` classifies strictly from exact call-ID evidence: a call present in `sampledToolCalls` for the thread/turn and not in `codeModeChildCalls` is a model call, a recorded Code Mode child call is an inner call, and an unknown or conflicting call ID stays unclassified. Evidence arriving later does not rewrite an event already emitted.
- Code Mode sampling accounts for both sides of the boundary: `appserver/code_mode_tool_call_analytics.go` records sampled exec/wait calls, while `rememberCodeModeCell`/`rememberCodeModeChildCall` record the tool calls dispatched by the cell. Command, file-change, MCP, and remaining tool-item emitters all use `toolEventTypeForCall`.
- Regression coverage: `appserver/tool_event_origin_test.go` covers exact-evidence requirements, Code Mode child calls, and null/conflict cases; `telemetry/turn_event_test.go` checks the wire payload.
- Verified with `go test ./appserver -run 'ToolEvent|SampledOutputToolCallID|ToolItem|CodeMode' -count=1` and `go test ./telemetry -run 'ToolEventType|CommandExecution|TurnEvent' -count=1`.

### Use a shared flattened image reference (#45543) (Round 816)

- The upstream commit is an internal Rust representation refactor: both image payload variants now hold a flattened shared `ImageReference::Inline { image_url }`, while the commit explicitly keeps the existing `image_url` JSON shape. A Go-side mirror of this Rust-only enum would add no observable contract.
- Go already stores the flattened form directly. `eventmap.ContentItem.Wire`/`RustJSON` emit `type`, `image_url` (or `file_id`, the later shared reference form), and `detail` in Rust's order; `appserver` round-trips the same `input_image` blocks through user input, thread history, notifications, and Responses requests.
- Image consumers preserve references without reinterpretation: `context` tool-output partitioning, `utils` output truncation, and the app-server image URL validation all read the inline reference rather than assuming a nested object.
- Existing regression coverage already freezes this contract: `eventmap/content_item_wire_test.go`, `appserver/image_reference_test.go`, `context/tool_output_parts_test.go`, and `utils/output_boundaries_like_rust_test.go`.
- Verified with `go test ./eventmap -run 'ContentItem|Image' -count=1`, `go test ./appserver -run 'ImageReference|InputImage|UserInput' -count=1`, `go test ./utils -run 'OutputBoundar|Truncate.*Image|Image' -count=1`, and `go test ./context -run 'Image|ToolOutput|TypedFragment' -count=1`.

### Honor prepared Unix socket permissions in Seatbelt (#45548) (Round 817)

- Seatbelt previously ignored the prepared managed-network context entirely: the policy used only explicit extra socket grants, so prepared allowlists were omitted and an allow-all grant had no effect.
- `sandbox/seatbelt.go` now parses the context's `allowUnixSockets` and `dangerouslyAllowAllUnixSockets`, gives the prepared policy precedence, and preserves explicit extra grants. Absolute paths are normalized, deduplicated, and sorted; relative entries are ignored; existing macOS paths resolve their top-level aliases.
- A restricted prepared list emits one `(allow system-socket (socket-domain AF_UNIX))` plus `UNIX_SOCKET_PATH_<n>` scoped bind/outbound rules. The allow-all grant emits the broad AF_UNIX bind/outbound rules without path parameters. An empty prepared list emits no AF_UNIX grant.
- `sandbox.BuildCommandRunPlan` decodes the managed-network context on macOS and passes it to the Seatbelt builder. The no-context builder remains behavior-compatible, and the daemon-socket deny still follows every socket allowance.
- Tests: `sandbox/seatbelt_test.go` covers prepared-plus-explicit precedence, relative-path rejection, empty allowlists, allow-all without unrestricted IP networking, and malformed JSON; `execserver/managed_network_unix_sockets_darwin_like_rust_test.go` covers macOS `prepareExecProcess` wiring for restricted and allow-all grants.
- Verified with `go test ./sandbox -count=1`, `go test ./execserver -run 'PrepareExecProcess|ManagedNetwork' -count=1`, `go vet ./sandbox`, and `GOOS=darwin GOARCH=arm64 go test -c` for both `./sandbox` and `./execserver`.

### opt-in registered Windows sandbox runtime (Round 818)

- Rust #45550 lets the Windows sandbox run against a service-registered Core package instead of always materializing a copied helper. Go now captures, validates, and propagates that runtime choice end to end; the remaining service-owned contracts are documented below rather than stubbed.
- New leaf package `sandbox/windowssandbox/internal/setupenv` owns the `CODEX_WINDOWS_REGISTERED_CORE` key, the process-wide once-captured `Requested()` (only the exact value `1` opts in), and the `Legacy`/`Registered` runtime enum, so `windowssandbox` and `elevated` share one decision without an import cycle.
- `app_package.go` re-exports `SetupRuntimeLegacy`/`SetupRuntimeRegistered`, adds `CurrentSetupRuntime()`, and adds `ApplyWrapperRuntimeEnv`/`ApplyWrapperRuntimeEnvForSelection`: every case-variant of the flag is scrubbed from the child environment and re-inserted as `1` only when the parent selected registered, so a nested sandbox cannot smuggle the flag in. `execserver/fs_helper.go` applies it on both Windows wrapper paths.
- Helper resolution is runtime-aware. `ResolveExeForRuntime` and the elevated `resolveHelperForRuntime` return the source executable directly in registered mode, without copying it, creating `.sandbox-bin`, or falling back to PATH; legacy keeps the previous materialization behavior. Setup roots (`GatherReadRootsForRuntime`, `GatherFullReadRootsForRuntime`, `GatherHelperReadRootsForRuntime`, `BuildPayloadRootsForRuntime`) thread the runtime through, with registered helper roots returning nil and creating no bin directory while explicit bin read roots are preserved.
- `setup_windows.go` serializes the runtime into the elevation payload (omitted when legacy), rejects helper-owned provisioning in registered mode, and derives the setup user from the process token before falling back to environment names. `setup_main` validates the payload runtime, refuses Full/Provision work for registered Core with `registered Core requires service-owned provisioning`, still allows RefreshOnly and ReadACLsOnly, and skips locking the sandbox bin directory. `identity.go` additionally requires `registeredSetupIsReady()` in registered mode, satisfied only by a real OS package identity on the current process.
- `appserver/windows_sandbox_runtime.go` and `runtime_router.go` distinguish an explicit caller cwd from the fallback cwd: a request that omits cwd now sends no workspace roots instead of granting the server directory.
- Metrics: `elevated/runner_metrics.go` adds `codex.windows_sandbox.runner_result` with `runtime`/`phase`/`outcome` labels (`success`/`nonzero_exit`/`transport_error`); `RetryRunnerSpawnOnce` records a startup result and `unified_exec/live_windows.go` records elevated command results.
- Known gap, deliberately not faked: Go has no `windows-sandbox-service` installation record, execution alias, staged runner image verification, background coalesced registration refresh, or service-side proxy reconciliation. Registered mode is protected locally by the app-contained runner plus OS package identity, and readiness remains synchronous rather than Rust's background refresh.
- Tests: `setup_runtime_like_rust_test.go` (parent runtime selection with case-variant env scrubbing, registered not materializing helpers, legacy materializing them, registered helper roots creating no directory, explicit bin read root preserved), `elevated/runner_pipe_test.go` (registered uses the app-contained runner), `elevated/runner_metrics_test.go`, `bin/setup_main/win/setup_runtime_like_rust_windows_test.go`, `setup_ownership_like_rust_windows_test.go`, and `appserver/runtime_router_test.go` (`TestRuntimeRouterWindowsSandboxSetupStartWithoutCWDHasNoWorkspaceRootsLikeRust`).
- Verified with `go build ./...`, `go test ./sandbox/... ./execserver -count=1`, `go test ./appserver -run 'WindowsSandbox' -count=1`, and `go vet ./sandbox/windowssandbox/... ./execserver`.

### attachment store session plumbing (Round 819)

- Rust #45556 replaces `AttachmentStore::persist` with `upload`/`resolve`, moves the image store into session services, and hands the thread manager's store to every spawned session. Go already had the full API surface (`UploadRequest`/`UploadResult`, `ResolveRequest.DownloadURLTTL`, metadata with digest/size/mime/image dimensions, categorized errors, redacting debug output) and the four Rust lib regressions.
- The missing contract was session plumbing: `ThreadManager` owned a configured store that no turn ever read, so every turn silently fell back to the inline default.
- `appserver/agent_runtime.go` (`responsesAgentForTurn`) now sets `agent.ImageStore = r.threads.AttachmentStore()`, the Go equivalent of Rust's `SessionSpawnArgs::image_store` from `ThreadManagerState::image_store`.
- Delegated subagent turns are turns on the same router, so they inherit the parent's store, matching Rust's `Arc<dyn AttachmentStore>` clone in `run_codex_thread_interactive`. `exec` keeps the inline default like Rust's `passthrough_image_store()`, and a host-injected `RuntimeServices.Agent` remains a Go-only override that keeps its own store.
- New regressions in `appserver/attachment_store_session_like_rust_test.go`: a turn's runner carries the thread manager's store, a child-thread turn resolves the parent's store, and a real SSE turn uploads through the store with the outbound request carrying its file id instead of inline base64. All three fail with the assignment removed.
- Verified with `go build ./...`, `go test ./attachmentstore ./eventmap ./model -count=1`, and `go test ./appserver -run 'ThreadManagerAttachmentStore' -count=1`; the full appserver run differs only by the two pre-existing unrelated analytics/fingerprint timeouts, which also fail with this change disabled.

### seed daemon installs from local CLI packages (Round 820)

- Rust #45558 seeds a missing managed daemon from the invoking CLI's complete local package. Go already had the staging path (`prepareDaemonPackage`, `validateDaemonPackage`, `requiredPackageArtifacts`, `copyPackageTree`/`packageTreeDigest`, `ensurePackageUnchanged`, `stablePackageVersion`, `followsStandaloneLatest`, `writeAutoUpdateMarker`), Windows selection with junction retargeting, and regressions for seeding, idempotent reuse, incomplete packages, platform/executable mismatch, stable-version gating, junction retargeting, and legacy migration.
- New regressions in `appserverdaemon/prepare_install_selection_like_rust_test.go`: a legacy standalone selection is left untouched (no dedicated root, no selection move), a copied release preserves an explicit pin versus the standalone latest-channel marker, and a broken `current` selection is reported for repair instead of replaced (nothing staged). Each fails against a mutated production guard.
- Remaining gap: Rust also seeds from a macOS CodexCLI.app bundle and compares identity against `CodexCLI.app/Contents/MacOS/codex`; Go has no bundle layout detection (`install.PackageLayoutFromExe`), tracked by Rust #42068 (audit_pending). The cargo-only CLI start/restart/bootstrap integration tests have no Go harness.
- Verified with `go test ./appserverdaemon -count=1`.

### macOS CLI bundle install detection (Round 821)

- Rust #42068 detects a CLI running from `CodexCLI.app/Contents/MacOS` and resolves the outer standalone package layout from the sibling `bin` directory, keeping standalone install detection, bundled resources, and path entries intact.
- `install.PackageLayoutFromExe` now has the same `MacOS` branch: it requires the `Contents` and `CodexCLI.app` containers, derives the package from `<bundle parent>/bin`, and reuses the shared bin-directory and `codex-package.json` checks. `standaloneInstallMethod` and the resources/path accessors follow from `PackageDir`.
- New regressions in `install/bundle_like_rust_test.go`: the ported Rust layout/method test (fails with the branch disabled) and a structural test rejecting `Other.app` bundles and a `MacOS` directory without `Contents`.
- Verified with `go test ./install -count=1`.

### daemon seeding from provisioned macOS bundles (Round 822)

- Follow-up to Round 820: #45558 also seeds from a provisioned macOS CLI bundle, where the running executable is `CodexCLI.app/Contents/MacOS/codex` rather than the packaged launcher. Round 821's bundle layout detection made that path reachable.
- `appserverdaemon/prepare_install.go` now selects the staged executable for the identity comparison through `stagedIdentityExecutable`: on darwin a staged `CodexCLI.app` means identity comes from `CodexCLI.app/Contents/MacOS/codex`; every other layout keeps the manifest entrypoint.
- `prepare_install_bundle_identity_test.go` pins the darwin choice plus the non-darwin and bundle-less fallbacks on any host. The darwin-only `prepare_install_darwin_bundle_test.go` ports Rust's `provisioned_macos_bundle_seeds_from_its_running_executable`: the launcher differs from the running bundle executable, the copied bundle bytes survive, and a prerelease records no auto-update marker.
- Remaining difference is test harness only: upstream spawns the built CLI through start/restart/bootstrap in cargo integration tests; Go covers those lifecycle paths in `appserverdaemon/lifecycle_test.go` and the app-level daemon tests.
- Verified with `go test ./appserverdaemon ./install -count=1`, `go build ./...`, and `GOOS=darwin GOARCH=arm64 go test -c ./appserverdaemon` for the darwin test.

### TUI standalone display math rendering (Round 823)

- Rust #45612 renders standalone display math in the TUI: `MathMarkdown` records `pending_start`/`display_ranges`, the streaming tail switches to `PreviewMode::Math` and shows the wrapped source verbatim until the closer arrives (reflowing on resize), and rejected or oversized displays stay literal. Go already had the display renderer and scanner; this round closes the streaming preview and source-preservation gaps.
- `tui/markdown/math_spans.go` exposes `MathPendingStart` (Rust `pending_start`). `tui/streaming/controller.go` adds `previewMode` mirroring `PreviewMode::{Prose,Math}`: a pending-display check cached per `(len(rawSource), width)`, `$$`/`\[` opener detection while prose preview is allowed, `safeLen` advance for math, and `wrapMathPreviewLines`, which wraps the raw source at UAX #14 line-break opportunities (uniseg) to reproduce Rust's `textwrap::wrap` output instead of re-rendering Markdown. The narrow snapshot `$$` / ` \frac{|` / `a+b+c+d+e+f|}{g+h}` matches Rust exactly. `setWidth` now re-renders before refreshing the preview like Rust.
- `tui/markdown/render.go` masks math before `revealHeadingMarkers`, so a rejected or pending display containing `#` or list lines restores its source verbatim instead of leaking an inserted heading marker (`$$\n# x` previously rendered `# # x`).
- New regressions: `tui/streaming/math_preview_like_rust_test.go` (raw source, exact narrow reflow snapshot, verbatim TeX underscores/indentation/Markdown punctuation, shell `$$` staying prose, closed display dropping the preview and matching the whole-source render) and `tui/markdown/math_display_like_rust_test.go` (narrow list/quote displays equal the code-span source render, a 600-row oversized display stays literal for both `$$` and `\[`, and pending continuations, rejected closers, PID boundaries and escaped prices match Rust).
- Verified with `go test ./tui/markdown ./tui/streaming ./tui/chatwidget ./tui ./tui/history_cell ./tui/exec_cell ./tui/tea -count=1` and `go vet ./tui/streaming ./tui/markdown`.
- Remaining difference is pre-existing and renderer-wide rather than math-specific: glamour output starts with a blank line and pads lines to the render width while Rust's ratatui lines do not; the existing streaming helpers compare against `renderSourceLines`, which carries the same shape.

### Windows sandbox registration refresh resumability (Round 824)

- Rust #45559 resumes a service-owned Windows sandbox registration refresh after the provisioning service restarts: `provisioning_client/refresh_retry.rs` retries only response-pipe disconnects (up to two restarts within the original deadline, requiring a new service PID authenticated against the running service), `RuntimeRegistration::can_resume_registration` lets a refresh resume without `ready_package` while runtime execution still requires readiness, and `provisioning/registered.rs` wires the new gate.
- Go has no provisioning service, service-owned `InstallationRecord`, `RuntimeRegistration` record, named-pipe provisioning client, SCM service-PID check, or background registration refresh: registered mode is validated locally from the app-contained runner plus OS package identity (see #45550's `partial` evidence and `identity.go` `registeredSetupIsReady`). With no client to retry and no service record to resume, there is no Go surface for this change.
- The local behavior Go does own — rejecting helper-owned provisioning for registered Core — is already covered by `sandbox/windowssandbox/bin/setup_main/win/setup_runtime_like_rust_windows_test.go`.
- Verified with `go test ./sandbox/windowssandbox ./sandbox/windowssandbox/bin/setup_main/win -count=1`.

### Guardian reviewer turn lifecycle ownership (Round 825)

- Rust #45672 consolidates Guardian reviewer lifecycle ownership in three parts: cancellation guards tie reusable reviewers and temporary forks to their lifetimes (a cancelled reusable reviewer is replaced before reuse), `ReviewerTasks` is shared between the pool and `ThreadManager` with tracked cleanup awaited on pool shutdown, and denial cleanup moves out of core task handling into Guardian's turn start/stop/abort hooks through `install_reviewer`.
- Go implements the third part: `RuntimeRouter.beginGuardianTurn`/`endGuardianTurn` wrap the existing blocking-sample hooks and clear the model reviewer's denial window for the turn (`state.CircuitBreaker.ClearTurn`). All three turn-start call sites (regular turns, standalone shell commands, manual compaction) and the single terminal path (completed and interrupted both flow through `notifyTurnCompletedOnce`) use the combined hooks, so a finished, interrupted or restarted turn cannot inherit stale denials.
- The pool half has no Go counterpart by design: Go owns one in-process model reviewer and reuses only the last successfully committed response cursor (`previous`/`seeded` in `guardianSessionRunner`), so a cancelled or dropped review never becomes the reuse base and there is no `ReviewerPool`, `ReviewerTasks`, fork snapshot or shutdown protocol to guard.
- Regression: `appserver/guardian_denial_lifecycle_like_rust_test.go` seeds `MaxConsecutiveDenialsPerTurn-1` denials, asserts a control turn reaches the limit, then shows turn start, completion and abort each clear the window, and uses a stub reviewer to prove the hook is a no-op when no accounting exists.
- Verified with `go test ./appserver -run 'Guardian|GuardianReview|GuardianTurn' -count=1`, `go test ./appserver -count=1` (only the two known unrelated RuntimeRouter analytics tests fail), and `go build ./...`.

### Guardian review sessions carry one ReviewModel struct (Round 826)

## Round 826 - #45684 Pass ReviewModel through guardian review sessions

- Rust only refactors: the six duplicated review-model fields on GuardianReviewSessionConfig/GuardianReviewSessionParams collapse into one guardian_reviewer::ReviewModel; session execution, analytics metadata and failed-review records read from it. Behaviour unchanged.
- Go already threads a single model.ApprovalReviewModel through guardianReviewPlan.Selection (guardianReviewPlanForTurn / SelectApprovalReviewModel) and reads the review request, failed-review capture, image context window and catalog-hash helper from it; no production change needed.
- Added appserver/guardian_review_model_like_rust_test.go: parent auto_review_model_override plus preferred and override models in the catalog, then asserts the entire selection struct survives, the review request uses its Model/ReasoningEffort, and the hash helper resolves the override model's CompHash.
- Verified: go test ./appserver -run 'Guardian' -count=1, go test ./model -count=1, go test ./appserver -count=1, go build ./... . Three unrelated RuntimeRouter analytics/fingerprint tests time out, including TestRuntimeRouterFileChangeAnalyticsIncludesUserReviewSummaryLikeRust which also fails on a clean worktree of the previous HEAD.

### Guardian approval routing honours cancellation before fast approvals (Round 827)

## Round 827 - #45693 Move Guardian approval routing into the reviewer extension

- Rust relocates approval routing into Guardian's ReviewRequest::decide (contributor routing, cached-approval validation, synchronous fallback, cancellation) and reports Abort when the tracked operation was cancelled before or during routing.
- Go keeps a single routing decision point in modelGuardianReviewer.Review, so no re-architecture is needed; the missing behaviour was cancellation precedence, which now returns DecisionAborted instead of a fast-path Approved when the context is already cancelled.
- Rust's cached-approval branch and contributor registry have no Go counterpart: Go's reviewer is synchronous-only with no cached-score approvals (documented in guardian_async_approval_like_rust_test.go).
- Added appserver/guardian_routing_cancellation_like_rust_test.go: cancelled context plus full access yields Abort with no fast decision and no model call; a live context approves and records exactly one fast decision.
- Verified: go test ./appserver -run 'Guardian' -count=1, go test ./appserver -count=1 (three pre-existing unrelated RuntimeRouter failures), go build ./... .

### Windows sandbox implementations separated from legacy setup modes (Round 828)

## Round 828 - #45737 Separate Windows sandbox implementations from legacy setup modes

- Rust splits WindowsSandboxSetupMode (elevated/unelevated, the setup RPC choice) from the new WindowsSandboxImplementation (elevated/unelevated/mxc) used by allowed_windows_sandbox_implementations, and documents that mxc clients skip the legacy setup/readiness APIs.
- Go already implements the split in repo commit 0793a694 (ancestor of HEAD): config.WindowsSandboxImplementation with validation, requirements parsing/projection, and ResolveAllowedWindowsSandboxSetupMode comparing the requested setup mode against the implementation allow-list.
- windowsSandboxModeFromConfigValues reports no legacy setup mode for mxc, so an mxc selection never enters windowsSandbox/setupStart; the app-server projection emits ["elevated","mxc"] and never the setup-only spellings.
- Verification only this round: go test ./config -run 'Windows' -count=1 and go test ./appserver -run 'WindowsSandbox|ConfigRequirements' -count=1 both pass, backed by config/windows_sandbox_implementation_test.go, config/windows_requirements_test.go, config/startup_warnings_test.go and appserver/windows_sandbox_mxc_test.go.

### Shared Bash/Zsh startup script exported for reuse (Round 829)

## Round 829 - #45749 Extract reusable Bash and Zsh startup scripts

- Rust exposes shell_startup_script from codex-shell-command: the shared interactive Zsh/Bash profile seeds, empty for every other shell type, with POSIX sh ENV handling left in snapshot capture.
- Go had the two bodies only as capture-private constants; shell/startup.go now exports ShellStartupScript and snapshotStartupPrefix consumes it, keeping the sh prefix inside capture.
- shell/startup_test.go pins the exact script bytes, the empty result for sh/powershell/cmd, and that interactive capture starts with the shared script while non-interactive capture does not.
- Verified: go test ./shell -count=1, go test ./execserver -run 'Snapshot' -count=1, go build ./... .

### V8 array-sort mitigation does not apply to the Sobek engine (Round 830)

## Round 830 - #45760 Disable V8 optimization paths affected by array sort bugs

- Rust turns off Maglev, Turbolev and TurboFan array builtin inlining during V8 initialization for a pinned-V8 mixed-element sort miscompile.
- Go has no V8: code-mode cells run on the pure-Go Sobek engine and the repo has no deno_core/rusty_v8 binding, v8:: API use or maglev/turbolev flags, so there is no optimizer tier to disable - not_applicable.
- codemode/array_sort_like_rust_test.go still pins the shared observable behaviour: a mutating comparator neither throws nor leaves a stale element, and [3,1,2].sort((a,b)=>a-b) emits [1,2,3].
- Verified: go test ./codemode -count=1.

### Managed proxy routing for Windows MXC not applicable in Go (Round 831)

## Round 831 - #45757 Wire Windows sandbox selection into managed proxy routing

- Rust tracks the Windows sandbox implementation separately from the legacy setup level and passes managed proxy routing for WindowsMxc into proxy startup, adds dedicated listeners, retains the policy decider across rebuilds and adds an MXC branch in the sandbox CLI.
- Go already carries SandboxTypeWindowsMxc versus SandboxTypeWindowsRestrictedToken in the sandbox plan and keeps the legacy setup mode separate; the routing half has no counterpart: no ManagedProxyRouting/dedicated-listener symbols exist, there is no debug-sandbox CLI, and the MXC launcher fails closed so no MXC proxy route is ever created - not_applicable.
- Verified: go test ./sandbox -run 'SandboxState|Windows|Mxc' -count=1, go test ./config -run 'Windows' -count=1, go test ./appserver -run 'WindowsSandbox|SandboxState|WindowsMxc' -count=1.

### TUI account analytics normalization layer added (Round 832)

## Round 832 - #45740 Add account analytics data normalization to the TUI

- New tui/analytics data layer: models.go (report/grouping/unit/history/day/value), report_data.go (typed chatgptapi projection preserving Option semantics) and normalize.go (daily histories with labels, units, freshness).
- Behaviours mirrored: missing attribution stays unavailable, signed credits survive, work_* surfaces merge into work, models under 1% of relative usage fold into Other, message totals stay authoritative with the unassigned remainder as Other, duplicate dates merge, only credit reports fill missing days, series labels win, freshness becomes Unix seconds.
- normalize_like_rust_test.go mirrors the upstream cases including task-start denominators, duplicate message dates, unsupported groupings and the typed projection path; not wired to /usage yet, matching upstream.
- Verified: go test ./tui/analytics -count=1, go vet ./tui/analytics, go build ./tui/... .

### Token history and credit formatting helpers for TUI analytics (Round 833)

## Round 833 - #45741 Add token history and credit formatting helpers for analytics

- tui/analytics/tokens.go builds daily text-token history by model or token type with date-range/model filtering, freshness, strict integer validation and reported model totals when components are missing.
- tui/analytics/data.go adds amount, credit_amount, credits(micros) and short-date formatting: tiny signed refunds stay visible, ordinary amounts align to two decimals, integer millionths avoid float rounding (i64 limits included), and Rust's exponent form is preserved.
- models.go gains AccountAnalyticsUnitTokens; tokens_like_rust_test.go mirrors the upstream token and formatting cases.
- Verified: go test ./tui/analytics -count=1, go vet ./tui/analytics, go build ./tui/... .

### #45742 Add account-bound authentication for analytics requests (Round 834)

## Round 834 - #45742 Add account-bound authentication for analytics requests

- tui/analytics/client.go adds AnalyticsSession: local ChatGPT credentials load from the auth store (environment API keys ignored), the session requires an account id plus ChatGPT user id, and reloads credentials before and after every request so an account or user switch fails with "Account changed. Press R to refresh Analytics.".
- Requests rebuild the cloud client from the reloaded snapshot with chatgptapi.NewCloudClientWithoutRedirects, so a rotated token and its account header reach the wire; a 401 gets one bounded remotecontrol.UnauthorizedRecovery plan (Go's counterpart of codex-login's UnauthorizedRecovery) and reports "Sign in again to view Analytics." when recovery fails.
- Live/AnalyticsLiveSession add lazy initialization that retries after sign-in, AccountLabel as "email <middle dot> id" (or just the id), and CreditGroups defaulting to the surface position before initialization; models.go adds AccountKind/AccountKindFor and CreditGroupings (team-like to Business, other workspace plans to Enterprise), data.go adds the seven-entry grouping order and CreditGroupIndices.
- client_like_rust_test.go mirrors the upstream cases: identity change mid-request sends no traffic, account and user changes are rejected before the request, missing and API-key-only auth are refused, reloaded credentials are used for the same identity, a 401 recovers once, session initialization retries after sign-in, and plan to credit-group mapping matches Rust.
- Verified: go test ./tui/analytics -count=1, go vet ./tui/analytics, go build ./... .

### #45762 Load analytics reports with server plans and account identity checks (Round 835)

## Round 835 - #45762 Load analytics reports with server plans and account identity checks

- tui/analytics/client.go: NewAnalyticsSession asks accounts/check for the signed-in account and uses the server plan (Account() starts at auth.PlanUnknown, so token plan claims no longer drive report selection); a mutex guards identity and client access, and a failed plan lookup reports "Couldn't load account plan. Press R to retry Analytics." with 401 mapping to "Sign in again to load Analytics.".
- Report selection now follows the verified plan: analyticsRoute picks the enterprise/business/consumer report and grouping (plugins 8/10, skills 6/10), analyticsCreditGroupingSupported gates credit breakdowns, and analyticsStartDate rejects overflow ranges; Live gains EndDate/Session/AccountLabel/CreditGroups/AttributedUsage/TokenModels/History/FilteredHistory backed by a per-session cache (analyticsCacheKeyFor keys Credits without a date range) that reuses payloads across grouping changes and evicts invalid responses so retries can succeed.
- data.go adds accountAnalyticsGroupLabels/AnalyticsGroupLabel plus AnalyticsLoadState/StartAnalyticsLoad with Poll/Cancel/State/Ready/Message, a 30s timeout, a panic guard, and "Loading…" / "No history has been reported." / "Request interrupted. Press R to retry." / "Request timed out. Press R to retry."; render.go adds AnalyticsCategories (descending absolute total, then key); models.go makes CreditGroupings(Unknown) empty.
- normalize.go prefers complete attribution within the requested range (turn-start breakdowns now include every feature) and only falls back to legacy surface/model daily data, small-model folding and credits-unit checks when attribution is incomplete; report_data.go maps workspace-credits reports onto the usage payload.
- appserver/runtime_router.go re-resolves auth after the backend usage read before exposing account-bound ordinary usage and upsell content; appserver/rate_limits_identity_like_rust_test.go covers user switch, workspace switch and same-identity token refresh (and fails without the recheck).
- Tests: client_like_rust_test.go covers server plan discovery and its failure/401/identity modes, 13 report routes and queries, cache reuse and identity-scoped eviction, model filtering, legacy fallback, invalid ranges, unknown-plan credit groupings, in-flight switches, 403/404 messages and load cancellation; normalize_like_rust_test.go covers attribution preference. Verified: go test ./tui/analytics -count=1, go test ./appserver -run "RateLimit" -count=1, go vet ./tui/analytics, go build ./... .

### #45763 Add stacked chart primitives for account analytics (Round 836)

## Round 836 - #45763 Add stacked chart primitives for account analytics

- tui/analytics/plot.go adds the stacked-bar pipeline: AnalyticsBand (nonnegative Amount, rowY, top-anchored reversed partial glyphs for refunds), AnalyticsSeriesColorName for the four palette series, AnalyticsCell/AnalyticsChart with AnalyticsChartText, AnalyticsPeak (max of summed absolute parts and the authoritative daily total), AnalyticsAxisMax (Rust's 1/1.5/2/2.5/3/4/5/7.5/10 ladder, never fractional for counts or tokens), analyticsPlotLayout (axis hidden unless the width is at least 30 and the tick label fits a third of it, cursor slot kept visible, center()), and AnalyticsPlot, which paints bars, the baseline and ticks, missing-day em dashes, reported zeros, the authoritative daily total callout with a connecting guide, and calendar ticks in Rust's order.
- render.go adds AnalyticsParts: the three plotted categories plus an un-renormalized Other slot that collects every remaining key, matching Rust's parts() for duplicate-date remainders.
- Calendar and label fitting follow Rust exactly: endpoints plus weekly ticks get priority, month/day labels fall back to the day-only form and are then dropped instead of truncated, and up to seven short-range bars show their totals with the cursor choosing space first.
- Tests in plot_like_rust_test.go mirror the upstream unit assertions: twelve axis_max cases, parts remainder and refund peak, two signed bands with reversed negative cells, missing/zero markers with one visible cursor at widths 36/8/1, oversized axes hidden for widths 8/30/35 and shown at 44, the authoritative 20.00 callout rather than 12.00, narrow 1/2/5/6-column dates, midpoint series color with reversed partial cells, tiny segments keeping their series color, and seven simultaneous daily totals.
- Difference: Go has no ratatui Buffer, so cells carry style facts (Series/Dim/Accent/Bold/Underline/Reversed) and the view resolves terminal colors; an out-of-range cursor is clamped instead of panicking.
- Verified: go test ./tui/analytics -count=1, go vet ./tui/analytics, go build ./... .

### #45764 Account analytics dashboard (view-model layer) (Round 837)

## Round 837 - #45764 Account analytics dashboard (view-model layer)

- tui/analytics/sections.go adds the stable section identities (usage/plugins/credits/activity/skills), their report mapping, per-section state (default cursor 6, detail, group), the three range groups (business usage shares the activity range; plugins and skills share tools), the account-specific card order, the "Product" grouping label for credits and unattributed usage, and the 7/30-day cursor shift.
- dashboard.go adds AnalyticsGroupOptions per section and account, AnalyticsConsumerAttribution, AnalyticsSectionTitle, and AnalyticsDashboardLines: bordered overview cards that switch to two columns at width >= 104, clamp card height to 16..24 rows from the viewport, push the "… z to maximize" hint when content overflows, and replace history with the group picker while it is open.
- panels.go adds the maximized panel (heading, "By …" grouping, business model line, picker selection range, wrapped content, chart band count); history_lines.go ports the section body: total line with "reported" for messages, approximate-usage and turn-start notes, chart or no-activity message, selected-day summary with Not reported / No activity (0) / relative units / raw expanded totals, Share percentages, 56-column breakdown table with the top three plus "N more" remainder, expanded rows, an empty-breakdown message that preserves row height, and the Updated footer.
- tool_panel.go adds readable plugin/skill labels (namespace dedupe plus PR/CLI/API/IDE/MCP/SDK/UI/TUI/OpenAI), and text_layout.go adds column joins, ellipsis truncation, and width-aware wrapping; data.go adds UnavailableAnalyticsLoad/Invalidate so closing or refreshing returns every report to Unavailable.
- Tests cover account layouts, range groups and labels, group options, attribution gating and titles, the range switch, section dates, card geometry at 120/80 columns, overflow hint, picker, panel heading/selection/bands, totals and shares, remainder and expansion, relative units, empty breakdowns, tool names, and load invalidation.
- Still open for a complete port: the `/usage` "Explore analytics" entry, the full-screen view with its key bindings, wiring to analytics.Live and the model-name map, draft preservation, and the chatwidget usage-menu changes.
- Verified: go test ./tui/analytics -count=1, go vet ./tui/analytics, go build ./... .

### #45764 Account analytics dashboard (full-screen view) (Round 838)

- chatwidget/usage.go adds `UsageMenuActionOpenAnalytics` and inserts the "Explore analytics" row between "Show usage" and the reset entry for ChatGPT accounts, matching Rust's `usage_menu_params` insertion.
- tui/tea/analytics_dashboard.go hosts the retained full-screen view: five independent section loads over an `analytics.Live` built for the current end date, account load through `Session`, `History`/`FilteredHistory` with `AnalyticsGroupingAt`, and refresh/close paths that rebuild or cancel loads without losing navigation state.
- Key handling mirrors `AnalyticsView::handle_key`: q/ctrl+c close, tab and 1-5 switch sections, z toggles overview/maximize, r flips the shared 7/30-day range and shifts cursor/detail, g opens the group picker, m cycles the business usage model filter, R refreshes, space/enter toggles day details, page up/down scroll, and esc backs out of details first.
- Rendering composes the bold title with the live account label, the `[7d]`/`[30d]` range row, numbered tabs, the maximized panel (chart height re-measured from the chart bands) or the overview grid, clamped scrolling, and a dimmed two-line footer whose keys are highlighted and whose details hint disappears when no day has values while keeping the footer height.
- model.go retains the view, routes key presses and pastes to it, renders it before the transcript, and polls pending loads on a 60ms tick; the `/usage` modal action opens it and the composer draft is never touched.
- Tests cover the menu entry and account-specific sections, range switching, the group picker, business model cycling, independent section failures, cancellation on close, small terminals, zoom, section switching and details, terminal styling, draft preservation, and retained navigation across reopen.
- Verified: go test ./tui/analytics ./tui/chatwidget ./tui/tea -count=1, go vet on the same packages, go build ./... .

### Top chats data loading (#45765, part 1) (Round 839)

- `chatgptapi` gains `GetThreadsUsage`: 1–100 distinct ids, nullable-credit wire rows, "Invalid thread usage response.", duplicate/unrequested-row rejection, and omission of unavailable estimates instead of zero; `GetThreadUsage` now delegates to it.
- `tui/analytics/chats.go` adds plan gating, 30-day recent-root discovery with cutoff/cursor/dedupe rules, and the bounded estimate loader: 200-thread chunks, two concurrent ≤100 halves, a 25s budget, 503 bisection, at most 15 requests per half, and descending-credit ranking with unavailable rows last.
- Titles come from name, then preview, then "Untitled chat", and stay hidden behind "Chat usage unavailable" until an estimate is returned.
- `StartAnalyticsLoadWithTimeout` gives the combined load a 90s budget; `AnalyticsSectionChats` is appended without renumbering existing sections and only appears for Business/EnterpriseCbpUsageBased/EnterpriseCbpAutomation plans.
- `tui/tea` now stores the account plan (not just the kind), matching Rust's `Load<PlanType>`.
- Verified: go test ./chatgptapi ./tui/analytics -count=1, go test ./tui/tea -run Analytics -count=1, go vet on the same packages, go build ./... .

### Top chats table and dashboard card (#45765, part 2) (Round 840)

- `tui/analytics/chat_panel.go` renders the Top chats table: the 30-day lifetime-credit header, the unavailable-estimate count, the lifetime-credit column with an "Est. $ spent" companion (stacked below width 70), `›` cursor markers, and a detail block that repeats truncated titles, sorts groups by descending credits, shows a model/effort/speed table or stacked lines, reports "Breakdown unavailable", and toggles zero-credit groups with `a`.
- `analyticsChatRowWindow` fits the maximized viewport (with a five-row cap for the card), reports the cursor row's line range, and ends with "Showing a–b of n chats" and the local-chats coverage note; `AnalyticsChatOverviewLines` renders "30d active · lifetime credits", five rows, and "Some estimates unavailable" when any row lacks an estimate.
- `tui/tea/analytics_dashboard.go` loads chats through the injected lister with a 90s budget, keeps the load independent from the reports, and mirrors Rust keys: `a` only with an open detail, `r` ignored on Chats, accept/move_right refused without an estimate, move_left closing details, and `↑/↓ row · accept details` instead of the 7/30d range row.
- `tea.Options.OnAnalyticsChats` lets a runtime supply the thread lister; the dashboard copies it at open time.
- Tests: table/stacked layouts, zero-credit toggle, unavailable breakdown, cursor window selection, overview five-row limit, business five-tab Top chats layout, row-hint footer, detail gating on unavailable rows, and `r` leaving the range untouched.
- Verified: go test ./tui/analytics ./tui/tea -count=1, go vet ./tui/analytics ./tui/tea ./chatgptapi, go build ./..., plus a build/test of the staged snapshot in a throwaway worktree.

### Consumer task usage data layer (#45768, part 1) (Round 841)

- `chatgptapi/task_usage.go` adds the consumer `query_v2` contract: disjoint task/thread bounds, ownership and status validation, and `TaskUsageCredits`, which preserves exact decimal strings and orders them by sign, exponent, and significant digits.
- `tui/analytics/tasks.go` adds `LoadAnalyticsTasks`: consumer-only loading, newest-first root selection with the 100-chat truncation, archived descendant discovery with chain proofs, disjoint batching, 404/503 freshness degradation, and the oldest `data_as_of` only when every batch supplied one.
- `AnalyticsTaskChat.DisplayTitle` keeps unavailable-account titles hidden, and `AnalyticsChatThread` now carries `CreatedAt` so discovery can forward root creation times.
- Tests cover decimal precision and ordering, request and response validation without stray HTTP traffic, archived paginated descendants, hidden unavailable titles, truncation with a 503 outage, overlap rejection, and every descendant chain failure.
- Part 2 will render the consumer table, `s` sort cycling, and the dashboard card.
- Verified: go test ./chatgptapi ./tui/analytics -count=1, go test ./tui/tea -run Analytics -count=1, go vet ./chatgptapi ./tui/analytics, go build ./... .

### Consumer Top chats table and dashboard card (#45768, part 2) (Round 842)

Consumer Top chats table and dashboard card (#45768, part 2).

- `tui/analytics/task_panel.go` renders the consumer ranking: metric cycling
  (`Weekly %`, `5-hour %`, `Balance credits`) clamped onto the metrics present
  in the data, descending order with missing values last, optional multi-metric
  columns when the width fits, per-row partial markers, a detail pane with the
  limit/balance figures and the `Model / effort / speed` group table, plus
  coverage, truncation, freshness and unavailable footers. The overview card
  keeps five rows and a `Showing top N of M chats` counter.
- Consumers now receive Chats as the fifth dashboard section; businesses keep
  the lifetime usage card. `AnalyticsPanelOptions` and
  `AnalyticsDashboardOptions` carry the task rows, readiness and active metric.
- `tui/tea/analytics_dashboard.go` loads consumer task usage on a 120s budget,
  invalidates it on refresh/cancel, clamps the cursor, cycles the metric with
  `s`, gates detail on available accounting, and adds the `s sort` footer hint.
  `Options.OnAnalyticsTaskThreads` supplies the descendant lister.
- Tests cover metric availability and fallback, ranking (`1e-20` below `0`,
  missing last), partial and unavailable rendering, footers, the five-row card,
  and an end-to-end consumer run with hidden titles, exact credits, `s` cycling
  `0→1→2→0`, gated detail and the top-two counter.
- Verified with `go test ./chatgptapi -count=1`, `go test ./tui/analytics
  -count=1`, `go test ./tui/tea -count=1`, `go vet ./chatgptapi
  ./tui/analytics ./tui/tea`, and `go build ./...`.

### Account profile data layer (#45769, part 1) (Round 843)

Account profile data layer (#45769, part 1).

- `chatgptapi/profile.go` adds `AccountProfile`, `ProfileIdentity`,
  `ProfileMetadata`, `ProfileStats`, `ProfileInvocation` and
  `ProfileInvocationKind`, plus `GetAccountProfile` on the existing
  `/api/codex/profiles/me` route.
- `ProfileStats` embeds `TokenUsageProfileStats`, so the summary reuses the
  token totals, streaks, longest turn and daily buckets, and adds fast-mode
  usage, most-used reasoning effort, skill counts, thread totals and the top
  plugin/skill invocations.
- Optional fields stay pointers (absence is not zero), percentages keep the
  backend decimal text through `json.Number`, and unknown invocation kinds
  decode as `Unknown` without discarding the rest of the statistics.
- JSON failures, empty bodies and a missing `stats` object all map to
  `Invalid account profile response.`; HTTP status errors pass through.
- Tests cover the empty payload, the full payload with a future invocation kind,
  a zero `total_threads` distinct from a missing one, malformed bodies and the
  request route.
- Verified with `go test ./chatgptapi -count=1` and `go vet ./chatgptapi`.

### Account Summary tab (#45769, part 2) (Round 844)

Account Summary tab (#45769, part 2).

- `tui/analytics/sections.go` appends `AnalyticsSectionSummary`: no report, the
  Usage range group, Daily/Weekly/Cumulative labels, first in every resolved
  plan, and the only section for an Unknown plan. Unresolved accounts still
  show nothing.
- `tui/analytics/summary.go` renders the identity (`display name`, `@handle`,
  short plan name), a responsive five-field token grid with compact amounts,
  streaks and longest chat, the activity chart with Daily/Weekly/Cumulative
  controls, Activity insights (fast mode, reasoning effort, skill and chat
  counts), the top five plugins/skills with run counts, and the statistics
  metadata. The overview card keeps the identity and token fields only.
- `LoadAnalyticsProfile` loads the profile independently of the reports and maps
  failures through the shared request guidance. `AccountLabel` now returns the
  email only, so the header drops the account id.
- `chatwidget.TokenActivityCalendarLines` exposes the calendar without the
  standalone view footer, and both the Summary and bar captions use the Rust
  one-decimal compact amount.
- The dashboard starts on Summary, scrolls it with the list actions, cycles the
  activity view with `g`, ignores `r`, prints `tab/1-7` and `g view` hints, and
  centers the loaded profile under the heading.
- Tests cover the section order per account kind, the rendered fields and
  layouts, missing and unbounded data, compact-amount promotion, the invocation
  cap and an end-to-end Summary tab with Weekly switching, scrolling and an
  independent report success when the profile fails.
- Verified with `go test ./chatgptapi ./tui/analytics ./tui/chatwidget
  ./tui/tea -count=1`, `go vet ./chatgptapi ./tui/analytics ./tui/tea
  ./tui/chatwidget`, and `go build ./...`.

### Account Summary tab (Round 845)

Account Summary tab (#45769, part 3).

- `tui/chatwidget/usage.go` reduces the `/usage` menu to two rows: `View
  analytics` (opens the dashboard) and `Redeem reset`. The separate `Show usage`
  and `Explore analytics` entries are gone.
- Explicit `/usage daily`, `/usage weekly` and `/usage cumulative` now open the
  retained analytics dashboard on Summary through `openAnalyticsSummary`, which
  selects the Summary section, zooms it, clears the scroll offset and grouping
  picker, and maps the view onto `AnalyticsSummaryViews`, so the chart starts on
  the requested grouping. No token-activity history cell is produced.
- `tui/analytics/plot.go` formats count and token callouts and every axis tick
  with the Rust compact amount; a 100M peak now renders `100M`/`50M`/`0`, the
  gutter stays hidden below 30 columns, and the oversized-axis snapshot matches
  Rust at widths 8, 30, 35 and 44 (a 1e30 peak still hides the axis).
- `tui/analytics/history_lines.go` switches the headline total to the compact
  amount from 1000 up, matching `chart.rs` `formatted_total`.
- Tests cover the two-row menu, the analytics entry, all three explicit views
  opening Summary with the matching grouping and no history cell, the compact
  headline thresholds, the compact axis snapshot, and the reset-row navigation.
- Verified with `go test ./chatgptapi ./tui/analytics ./tui/chatwidget
  ./tui/tea -count=1`, `go vet ./chatgptapi ./tui/analytics ./tui/tea
  ./tui/chatwidget`, and `go build ./...`.

### Analytics chart readability and navigation (Round 846)

Analytics chart readability and navigation (#45770, part 1).

- Rust #45770 makes `g` cycle the available groupings instead of opening a
  picker. `tui/tea/analytics_dashboard.go` drops `groupPicker`: `g` advances to
  the next grouping (wrapping) for Usage/Credits/Activity and the Summary views,
  then reloads the report.
- `tui/analytics/panels.go` and `tui/analytics/dashboard.go` lose the picker
  rows, the picker selection override and the `GroupOptions`/`GroupPicker`
  options; the footer no longer prints picker hints and the Summary centering no
  longer waits on a closed picker.
- `tui/analytics/summary.go` shows only the active `Daily`/`Weekly`/`Cumulative`
  label beside `Token activity` instead of the three-way bracketed row.
- Tests cover grouping advance and wraparound, the single active Summary label
  in wide and narrow layouts, the removed in-card picker, and the panel's full
  selection range.
- Verified with `go test ./tui/analytics ./tui/chatwidget ./chatgptapi
  ./tui/tea -count=1`, `go vet ./tui/analytics ./tui/tea`, and `go build ./...`.
- Remaining for #45770: seven-day daily totals, viewport-dependent breakdown
  rows and legend height, the footer update timestamp, and overflow-dependent
  scroll hints.

### Analytics chart readability and navigation (Round 847)

Analytics chart readability and navigation (#45770, part 2).

- The range row keeps a fixed two-line height, so section tabs stay on the same
  row for reports, Summary and Top chats alike.
- `tui/analytics/history_lines.go` sizes the maximized breakdown rows from the
  viewport (`max(viewport_height/4, 4)`), keeps four rows in the overview card,
  and prints the update timestamp only in the overview card (`chart.rs`).
- The maximized update timestamp moves to the right-aligned footer, wrapping to
  its own line when narrow. Scroll hints disappear when the body fits the
  viewport, and the trailing blank row is reserved only for overflowing panels.
- Seven-day daily totals were already in place and now match the Rust
  `seven_day_totals` snapshot text exactly at width 100, height 8.
- Tests cover the stable tab row at widths 64 and 120, taller legends with the
  footer timestamp, overflow-dependent scroll hints, the row budget and the
  timestamp gating.
- Verified with `go test ./tui/analytics ./tui/tea ./tui/chatwidget
  ./chatgptapi -count=1`, `go vet ./tui/analytics ./tui/tea`, and `go build ./...`.

### Analytics chart readability and navigation (Round 848)

Analytics chart readability and navigation (#45770, complete).

- Confirmed the remaining Rust pieces of `de40696e` are behavioural no-ops in
  Go: `dashboard.rs`, `panels.rs`, `sections.rs` and `analytics.rs` only remove
  the grouping picker (already dropped in Round 846), and `render.rs` keeps the
  stable range row, footer timestamp and overflow-dependent hints from Round 847.
- `TestAnalyticsExactFitHasNoScrollHintLikeRust` mirrors Rust's
  `analytics_exact_fit_content_has_no_scroll_hint`: at the exact
  `chrome + content` height the Chats viewport equals the content height and no
  scroll hint is painted.
- `TestAnalyticsPlotNarrowEqualTotalsLikeRust` reproduces Rust's
  `narrow_equal_height_totals_remain_separate_numbers` snapshot byte-for-byte at
  widths 21, 28 and 35, and the seven-day total test keeps covering the
  `seven_day_totals` snapshot behaviour at width 100, height 8.
- `TestAnalyticsUngroupedSectionsKeepConfiguredGLikeRust` keeps a configured
  `g` jump-top binding working in Chats, Plugins, Skills and Credits, so the
  grouping cycle cannot swallow the list action in ungrouped sections.
- The taller-legend test now also checks that the footer timestamp wraps onto
  its own line at widths 24, 40 and 64.
- Verified with `go test ./tui/analytics ./tui/tea ./tui/chatwidget
  ./chatgptapi -count=1`, `go vet ./tui/analytics ./tui/tea`, and `go build ./...`.

### Analytics plan history menu and Vim navigation (Round 849)

Analytics plan history menu and Vim navigation (#45772).

- The `analytics_plan_history` registry entry already carried the experimental
  name, preview copy and default-off state from #45766;
  `TestAnalyticsPlanHistoryExperimentalLikeRust` and
  `TestExperimentalFeaturesAnalyticsPlanHistoryLikeRust` now pin the metadata
  and the unchecked experimental-menu row.
- `analyticsListActionForKey` adds `h`/`l` as horizontal aliases only when the
  raw key is unbound: explicit bindings win, and remapped or removed arrows
  stay authoritative, matching Rust's `action_for(...).or_else(...)`.
- Footer shortcut keys use `analyticsFooterKeyStyle`: black on light terminal
  backgrounds, the terminal foreground otherwise (Rust
  `style::footer_hint_key_style`).
- Tests mirror Rust `navigation_tests.rs`: chart aliases in dashboard and
  maximized views for every grouped and ungrouped section, chat detail
  open/close, configured `g`/`h`/`l` `jump_top`, unbound arrows, remapped
  arrows, and modified keys.
- Verified with `go test ./features ./tui/bottom_pane ./tui/analytics ./tui/tea
  ./tui/chatwidget ./chatgptapi -count=1`, `go vet ./features
  ./tui/bottom_pane ./tui/tea ./tui/analytics`, and `go build ./...`.

### Python SDK ImageUserInput rename (Round 850)

Python SDK `ImageUserInput` rename (#45796, not applicable).

- The commit only touches `sdk/python`: the artifact script mapping, the
  generated Pydantic `UrlUserInput` class/union, and its Python test.
- codex_go does not vendor `sdk/python` (no `sdk` directory), so there is no
  generated Python binding or artifact script to update.
- The underlying protocol behaviour (file-ID image references in inputs and
  tool outputs) is already covered by #45794, which is complete.

### Windows sandbox uninstall cleanup (Round 851)

Windows sandbox uninstall cleanup (#45799).

- The legacy uninstall path now reports the no-home branch with
  `skipping sandbox directories: no pinned home`, matching Rust's
  `clean_up_legacy_windows_sandbox` delegation to
  `clean_up_packaged_windows_sandbox(None, ...)`.
- `removeLegacySandboxUsers` reports each failure line
  (`remove <name> profile and account: failed, ...`) instead of only returning
  them, while still deleting the profile before the account and keeping the
  account when profile deletion fails.
- `removeLegacySandboxGroupAfter` removes `CodexSandboxUsers` only when every
  earlier step succeeded; otherwise it reports
  `retaining sandbox group until cleanup finishes`, matching Rust's
  `errors.is_empty()` gate so a retry can still resolve the accounts.
- The remaining upstream files are the Rust-only `windows-sandbox-service`
  crate (package lifecycle, registered-runtime removal, `EVENT_CLEANUP_DETAIL`,
  retained-logon SID deferral, SCM-stop retry); codex_go ships no such service,
  so those symbols have no carrier.
- Tests cover the retained group after a failure and the success/failure
  wording. Verified with `go test ./sandbox/windowssandbox -count=1`,
  `go vet ./sandbox/windowssandbox`, and `go build ./...`.

### Bounded WSL terminal detection (Round 852)

Bounded WSL terminal detection (#45811).

- `readWindowsTermProgramWithTimeout` mirrors Rust's
  `read_windows_vscode_detection_with_timeout`: the `cmd.exe /d /s /c "set
  TERM_PROGRAM"` launch, the wait for output, and cleanup all run off the
  startup goroutine under one deadline, so a WSL interop launch that blocks
  before the child exists can no longer stall startup.
- A probe that answers after the deadline is killed and reaped instead of
  leaked, and a timeout is cached by the existing `sync.Once` seam so the worker
  is not retried on the startup path.
- `probeWindowsTermProgramWithTimeout` maps outcomes like Rust: exit 0 with
  `TERM_PROGRAM=vscode` is `vscode`, any other answered value and exit status 1
  are `other`, and launch failure, timeout, or any other exit status are
  `unknown`.
- `KeyboardEnhancementDisabledFor` now takes the three-state
  `VscodeDetection`, and `KeyboardEnhancementDisabled` mirrors Rust's
  `keyboard_enhancement_disabled`: only WSL plus a VS Code or inconclusive
  reading disables enhancement, and the environment override still wins.
- Tests mirror `windows_term_program_tests.rs` through a portable process seam:
  exit 0/vscode, exit 0/other, exit 1, exit 2, launch failure, empty value,
  timeout with kill+reap, and a launch that completes only after the deadline.
- Verified with `go test ./tui/tui -count=2`, `go vet ./tui/tui`, and
  `go build ./...`.

### Windows sandbox policy and executor hosts (Round 853)

Windows sandbox policy and per-thread executor hosts (#45813).

- `tui/windows_sandbox.go` now carries Rust's TUI `WindowsSandboxConfig`:
  `Mode` from `[windows] sandbox` or the legacy
  `elevated_windows_sandbox` / `experimental_windows_sandbox` /
  `enable_experimental_windows_sandbox` flags, `Allows`, `RequiresElevated`,
  and `Level`. A managed policy that disallows the configured mode falls back
  with elevated preferred, and an unloaded policy denies every choice instead
  of defaulting to allowed.
- `WindowsSandboxPolicyFromImplementations` converts
  `allowedWindowsSandboxImplementations`, dropping MXC because it is not a TUI
  setup mode and preserving the difference between a null list (no
  restriction) and an empty list (nothing allowed).
- `tui/app/windows_sandbox_host.go` adds `WindowsSandboxHost`
  (`local`/`mixed`/`remote`/`unknown`) and `HostFromEnvironments`; missing or
  empty environment lists classify as `unknown` since a server-local
  connection can still select remote executors.
- `ThreadSessionState.WindowsSandboxHost` is set by
  `SessionStateForThreadRead` from the new `ThreadReadSnapshot.Environments`, so
  start/resume/fork/replay paths keep the host classification with the thread.
- Tests mirror `windows_sandbox_tests.rs` (policy table, managed fallback,
  legacy flag precedence, default denial, level mapping) and
  `windows_sandbox_host_from_observed_environments`, including remote host
  restore for a replayed thread.
- Verified with `go test ./tui/ ./tui/app/ ./config/ ./tui/chatwidget
  -count=1`, `go vet ./tui/ ./tui/app/`, and `go build ./...`.

### App-server Windows sandbox state (Round 854)

App-server Windows sandbox state for onboarding (#45830, not applicable).

- The commit is removal-only: it deletes `windows_sandbox_level` from
  `AppCommand::OverrideTurnContext`, stops sending sandbox-only overrides, and
  deletes `windows_sandbox::level_from_config`.
- Go has no turn-context override carrying a Windows sandbox level: built-in
  preset changes go through `thread/settings/update` and the
  `WindowsSandboxLevelRefresh` result flag in `tui/chatwidget/settings.go` has no
  sender, so there is nothing to remove and no stale override can be ignored by
  the server.
- Go's connected TUI already treats the app server as authoritative:
  `interactiveRemoteWindowsSandboxStartupPrompt` reads effective config and
  requirements from the server and `refreshWindowsSandboxStateCmd` re-reads them
  after setup, matching the intent of deriving onboarding state from the app
  server. The local-config variant in `interactive.go` only serves embedded
  (non-app-server) sessions, which Rust does not have.
- The onboarding trust screen is only a render model in Go
  (`tui/onboarding.TrustDirectoryPrompt`): `ShowWindowsCreateSandboxHint` is set
  solely by tests and no live flow derives it from local configuration, so the
  source switch has no carrier.
- `level_from_config`'s deletion does not remove behaviour: Go's
  `WindowsSandboxLevelFromConfig` is fed app-server config values and matches the
  surviving `WindowsSandboxConfig::level()` mapping.

### Session-only model selection (Round 855)

Session-only model and reasoning selection (#45831, partial).

- `tui/bottom_pane` now carries Rust's `SelectionSecondaryAction`: a row can
  declare a single-character shortcut, an action, and a footer hint.
- `AcceptSecondarySelected` runs only that action and still refuses disabled
  rows; `HandleKey` triggers it only for non-searchable views, because a
  searchable list consumes plain characters as query input, matching Rust's
  `allow_plain_char_navigation` guard.
- `ActiveFooterHint` and the row renderer prefer the selected row's secondary
  hint over the view footer, mirroring Rust `active_footer_hint`.
- Tests mirror `secondary_action_respects_search_disabled_rows_and_dismissal`
  (secondary, primary, disabled row, searchable view) and pin the footer-hint
  override and fallback.
- Still pending: wiring the `s` shortcut onto the model and reasoning pickers,
  `AppEvent.SelectSessionModel`, the "for this session only" confirmation, and
  Plan-mode default preservation. Those live in `tui/tea/model.go`, which has
  uncommitted in-flight work from another session, so the port is deferred to
  avoid mixing changes.
- Verified with `go test ./tui/bottom_pane -count=1` and
  `go vet ./tui/bottom_pane`.

### App-server shutdown signal (Round 856)

App-server shutdown signal across loop iterations (#45849, not applicable).

- Rust recreated a `shutdown_signal()` future on every `tokio::select!`
  iteration, so an unrelated event cancelled and rebuilt the pending signal
  listener; the fix pins one boxed future and resets it only after completion.
- Go has no analogous construct. `appserver/stdio.go` calls
  `installStdioShutdownSignals` once per connection, and
  `appserver/stdio_shutdown_unix.go` keeps a single `signal.Notify` channel plus
  a goroutine for the connection lifetime, so no listener is cancelled and
  re-registered between loop iterations.
- Go also has no SIGHUP graceful-restart select loop (`graceful_signal_restart`
  in Rust); the only Unix signal path is the SIGTERM teardown watchdog from
  #44523.
- The companion upstream change is a Rust-only app-server WebSocket drain test
  fixture with gated SSE responses, which has no Go counterpart.

### Preserve executor path conventions in permission summaries (#45852) (Round 857)

Preserve executor path conventions in permission summaries (#45852).

- `sandbox/summary.go` adds `SummarizePermissionProfile(profile, cwd *utils.PathURI, workspaceRoots []*utils.PathURI)`, derived from the profile's runtime
  filesystem entries instead of a legacy `SandboxPolicy` projection: a write on
  the working directory (URI or native form) or an unscoped `project_roots`
  entry marks the workspace writable, subpath writes, writes outside the
  working directory, and `root` stay `custom permissions`, and `slash_tmp`
  counts only for POSIX working directories.
- Extra workspace roots render through `PathURI.NativePathString()`, so POSIX,
  Windows drive, and UNC roots keep their own convention (encoded spaces
  decode, case-distinct roots stay distinct).
- `exec/exec.go` converts the working directory and workspace roots to
  `PathUri` before summarizing, keeping the legacy tag only when the path
  cannot be represented; `tui/status` gains `StatusPermissionSummary` and
  `WorkspaceRootSuffix` mirroring the Rust status card helpers.
- Tests mirror the upstream cases (POSIX, Windows drive, UNC, case-distinct
  roots, opaque subpath writes, network and full-access labels).

### /daemon menu for local background server updates (#45854) (Round 858)

`/daemon` menu for local background server updates (#45854, regression closure).

- Go already implements the full surface, so this round records regression
  evidence instead of new code. `tui/tea/daemon_menu.go` opens the menu,
  refuses confirmation without a launching CLI or for a remote target, records
  `PendingUpdateAction`, and quits; `app/daemon_update_action.go` then relaunches
  the CLI in the foreground with the source's arguments and propagates
  failures.
- The view builders in `tui/chatwidget/daemon_menu.go` keep maintenance
  available when disconnected or embedded while disabling both sources for
  remote connections or a missing CLI, and disabling "Use this CLI build"
  without a local package.
- `tui/update_action.go` pins `app-server daemon update` and
  `app-server daemon update --from-cli --yes`; local daemon version notices
  point at `/daemon` (`tui/tea/server_version_notice.go`,
  `tui/history_cell/warnings.go`).
- Existing coverage in `tui/chatwidget/daemon_menu_test.go`,
  `tui/tea/daemon_menu_test.go`, and `app/daemon_update_action_test.go`
  (selected executable, arguments, handoff env, failure copy) passes.
- Rust also suppresses the generic exit summary for a daemon update; the Go
  interactive path prints no `AppExitInfo` lines at all, so there is nothing to
  suppress.

### Avoid redundant model catalog lookups in reused Guardian reviewers (#45933) (Round 859)

Avoid redundant model catalog lookups in reused Guardian reviewers (#45933, not applicable).

- Rust rebuilt the previous model's turn context (and therefore refreshed the
  model catalog) even when a Guardian reviewer reused the same model.
- Go makes the decision in `previousModelCompactionForTurn` and returns before
  building any turn context when the recorded model matches the selected one.
- Model metadata reads on that path are in-memory only: `ModelService.Info`
  delegates to `ModelsManager.GetModelInfo`, which reads `GetRemoteModels`;
  cache refresh happens exclusively through `RawModelCatalog`/`ListModels`,
  which the compaction decision never calls. An expired cache therefore issues
  no extra catalog request.

### Memory storage size and memory-version telemetry (#45956, #45960) (Round 860)

Memory storage size and memory-version telemetry (#45956, #45960, regression closure).

- `memories/workspace.go` adds `MemoryStorageBytes`, which walks the root with
  `Lstat`, sums regular-file sizes, skips `.git` entries and symbolic links, and
  saturates instead of wrapping; `memories/metrics.go` reports it as
  `codex.memory.storage_bytes` on Rust's log-spaced boundaries with the
  `memory_version` tag.
- `memories/pipeline.go` records the sample after stopping the phase-two timer
  on both the `succeeded_no_workspace_changes` path and the post-baseline-reset
  success path, so successful runs that changed nothing are covered too.
- `memories/usage.go` classifies reads into `(kind, memory version)` pairs,
  normalizing Windows separators first and mapping `memories_v2/` to v2 before
  collapsing the prefix onto `memories/`; `telemetry/memory_usage.go` emits
  `codex.memories.usage` with the `memory_version` tag beside kind, tool, and
  success.
- Existing coverage (`memories/metrics_test.go`,
  `memories/workspace_test.go`, `memories/usage_test.go`,
  `telemetry/memory_usage_test.go`) exercises nested files, UTF-8 sizes, git
  exclusion, symbolic links, both roots, mixed-root reads, and PowerShell
  spellings; `go test ./memories/... ./telemetry/` passes.

### Hidden HTTP/3 TCP tunnel command (#45900) (Round 861)

Hidden HTTP/3 TCP tunnel command (#45900, regression closure).

- `cli/cli.go` registers the hidden `codex tcp-tunnel` command and
  `cli/tcp_tunnel.go` parses its HTTP/3 CONNECT arguments.
- `tcptunnel/args.go` requires the proxy origin to match an approved HTTPS
  origin from the policy file and admits loopback targets; `tcptunnel/control.go`
  reads the bearer token and bounded, non-forwarding `x-` headers from stdin,
  acknowledges renewal with `AUTH_UPDATED`, reports `LISTENING`, and stops on
  control-pipe closure.
- `tcptunnel/tunnel.go` keeps a single listener across proxy reconnects without
  replaying TCP streams, and `tcptunnel/diagnostics.go` emits bounded JSON
  diagnostics with secret-free error codes.
- `go test ./tcptunnel/` covers parsing, admission, credential renewal,
  control-pipe closure, invalid input, header bounds, and a local HTTP/3 proxy
  exercising token replacement, transport recovery, and draining; `./cli` tests
  cover the hidden subcommand surface.

### Bound model catalog decode errors and classify request timeouts (#45928) (Round 862)

Bound model catalog decode errors and classify request timeouts (#45928, not applicable).

- Rust interpolated `serde_json` errors and the whole body into the catalog
  decode failure. Go's `decodeModelsEndpointResponse` returns the
  `encoding/json` error unchanged, which carries only an offset or the failing
  field/struct/type — never the response body or payload values.
- The error never reaches a user either: `RemoteModelsManager.fetchAndUpdateModels`
  treats any endpoint error as a best-effort catalog miss and keeps the previous
  snapshot, so no caller distinguishes a generic timeout from a request timeout.
  The five-second deadline (`modelsEndpointRefreshTimeout`) is a
  `context.WithTimeout` whose expiry aborts the request and is swallowed.

### Remove the `done` prefix from TUI completion timestamps (#46067) (Round 863)

Remove the `done` prefix from TUI completion timestamps (#46067, closure).

- Go `tui/history_cell/separators.go` already appends only the formatted
  timestamp to the completion footer: `formatCompletionTime` renders `2:32 PM`
  for the current day, `Sep 6 at 2:32 PM` within the year, and
  `Sep 6, 2000 at 2:32 PM` for older turns, so footers read
  `Worked for 2m 5s · 2:32 PM`.
- A repository-wide search finds no `done <time>` completion text in the Go TUI,
  and the separator/completion tests (including
  `TestCompletionLabelShowsShortDurationsLikeRust`) pass.

### Bound code-mode output previews across result blocks (#46073) (Round 864)

Bound code-mode output previews across result blocks (#46073, closure).

- This change's five-row head/tail split was superseded upstream by #46492's
  shared `ToolOutputPreview`; Go already implements the resulting HEAD behavior.
- `tui/tool_output_preview.go` keeps three rendered rows (`PreviewLines`), bounds
  one logical line at 16 KiB, adaptively wraps before counting rows so an
  overlong URL cannot exceed the budget, reports every partially displayed or
  later line as hidden, and appends the `+N lines (ctrl+t to view transcript)`
  marker only while it fits.
- `tui/history_cell/mcp.go` routes compact `node_repl` detail rows through the
  shared preview while `TranscriptLines`/`RawLines` keep the full text;
  `tui/history_cell/mcp_preview_test.go` pins the shared budget across multiple
  result blocks and transcript preservation.

### Document and test `?` wildcards in network proxy domain patterns (#46027) (Round 865)

Document and test `?` wildcards in network proxy domain patterns (#46027).

- Go already matched this grammar: `network/proxy_policy.go` compiles byte-level
  `?`/`*` matchers with Rust `domain_matcher.rs` semantics, and
  `CompileProxyDomainMatcher` expands `*.`/`**.` the same way (`?*.<domain>`,
  plus the apex for `**.`).
- This round ports the upstream table into
  `network/proxy_policy_question_mark_like_rust_test.go`: single and repeated
  `?`, `?` combined with `*`, `*.`/`**.` combinations, dot matching, and host
  and pattern normalization, exercised against both allow and deny matcher
  configurations.

### Tag rollout compression metrics by trigger (#46047) (Round 866)

Tag rollout compression metrics by trigger (#46047, closure).

- `rollout/compression.go` already threads `RolloutCompressionTrigger`
  (`startup` from `appserver/rollout_migration_startup.go`, `rpc` from the
  `rollout/compress` handler) through the worker into run, file, scan-error,
  temporary-file cleanup, and metadata-diagnostic metrics, tagging counters,
  durations, byte counts, and compression ratios with `trigger`, and omitting
  the tag for materialize failures the way Rust does.
- `rollout/compression_metrics_test.go` pins the `startup` and `rpc` label sets
  across run/file/scan/temp-cleanup samples, including the deliberate absence of
  `trigger` on the materialize failure sample, and `go test ./rollout/
  -run Compression` passes.

### Keep session permission profiles across settings switches (Round 867)

Keep session-defined permission profiles usable across settings switches (#45981, closure).

- Rust had to reload permission configuration with the thread's enabled
  `SessionFlags` layers because `thread/settings/update` otherwise could not see
  profiles the thread defined at start time. Go never reloads configuration for
  that method: `ThreadExtraService.UpdateSettingsWithCommit` records the selected
  profile on the thread settings and record metadata, and `prepareTurnStartParams`
  merges `threadRecordConfigOverrides(record)` into the next turn's config, so the
  session `[permissions.*]` table and its `default_permissions` stay available.
- `appserver/permission_profile_session_test.go` already ports the upstream table
  (session-only profile, profile also on disk, top-level `permissions` selection)
  and asserts the requested profile id comes back on `thread/settings/updated`;
  `appserver/permission_profile_switch_session_test.go` now pins that switching
  away to `:workspace` and back to the session-defined `audit` profile wins over
  the session default on the next turn and still resolves to the read-only
  policy. Both tests pass.
- Rust's exec-wrapper half is structural: Go does not place arg0 dispatch paths
  in configuration (`codex_linux_sandbox_exe` only appears on the sandbox state
  wire), so there is no per-thread config reload that could drop them.

### Show attach feedback when opening tasks from the overview (Round 868)

Give immediate feedback while an agents-overview task attach is in flight (#45983, closure).

- Rust had to draw a bold `Loading task…` frame by hand because its attach
  awaits server requests inline, where prompts and chat-widget replacement can
  clear the terminal. Go's attach is a bubbletea command: the dashboard closes,
  `applyAgentModalOption` raises a `Switching to <label>...` notice that the next
  render shows while the resume runs on the command goroutine, and
  `applyAgentSwitchResult` rebuilds the widget and replaces the notice with the
  attached task's label.
- `tui/tea/agents_overview_switch_notice_like_rust_test.go` pins that lifecycle:
  the notice exists before the attach command resolves, appears in the rendered
  view, and is gone after the widget rebuild. `go test ./tui/tea/
  -run 'AgentsDashboard|AgentsOverview'` passes.

### Route permission shortcuts through the shared selection flow (Round 869)

Route session-only permission shortcuts through the shared selection flow (#46013, closure).

- `applyPermissionShortcutKey` already cycles the cached permission catalog and
  hands the selection to `applyPermissionSelectionWithPersistence(item, false)`,
  which for a connected thread calls `selectServerPermissionProfile`: the
  request goes out through `onUpdateThreadPermissions`, `pendingServerProfile`
  is set, and the notice reports `Permission selection requested: <label>`
  while `State.Sandbox`/`State.ApprovalPolicy` keep the confirmed values.
  `applyThreadSettingsUpdated` then applies the server's `ThreadSettingsUpdated`
  snapshot and clears the pending profile; shortcuts never write config.
- Rejections reuse the shared messages: `Permission selection requires a newer
  app server.` for an unsupported server and `Failed to select permissions:
  <error>` otherwise.
- `tui/tea/permission_shortcut_server_flow_like_rust_test.go` pins the
  pending/confirm lifecycle for a shortcut and both rejection messages;
  `go test ./tui/tea -run Permission` passes.

### Disable executor skills per environment (Round 870)

Disable executor skills per environment (#46015, closure).

- `RuntimeServices.DisabledExecutorSkillPaths` already carries the caller-owned
  map, and `executorSkillProviderCatalogWithSnapshot` applies it: each catalog
  entry's capability root is mapped to the environment it was selected from, so
  a `SKILL.md` listed for that environment is dropped from the catalog while
  other environments keep the skill and everything stays enabled by default.
- Matching compares the executor path and the exposed locator as path URIs, so
  forward-slash, `file:` locator, and macOS `/var` aliases resolve to the same
  document; a dropped entry never reaches the prompt metadata.
- `appserver/executor_skill_disabled_test.go` pins the default catalog, direct,
  forward-slash, and `file:`-locator disablement, environment isolation, and the
  Windows/POSIX path-identity rules; those tests pass.

### Allow hosted Apps protocol-mode overrides (Round 871)

Allow a hosted Apps protocol-mode override while keeping user verification (#46019, closure).

- Go has no extension-contribution enum: `RuntimeConfigFromValuesWithAuth`
  builds the host-owned `codex_apps` registration itself and plugin overlays are
  the only extension contributions, so Rust's new `HostedApps.protocol_mode`
  field has no direct counterpart.
- The behavior it enables is already implemented: `ServerConfig.ProtocolModeOverride`
  is checked first in `effectiveMCPProtocolMode`, while
  `isHostOwnedCodexAppsRegistration` ignores the override, so native user
  verification stays limited to the controller-owned Apps server.
- `mcp/protocol_2026_hosted_apps_override_like_rust_test.go` pins the legacy
  hosted Apps default, a `V20260728` override selecting modern protocol with
  user verification still allowed, and the same override on an ordinary
  extension registration selecting modern protocol without verification;
  `go test ./mcp -run 'ProtocolMode|UserVerification'` passes.

### Expose the experimental rollout compression trigger (Round 872)

Add the experimental rollout compression trigger endpoint (#46020, closure).

- `rollout/compress` already exists in Go: `MethodRolloutCompress` is declared,
  `schema.go` marks it experimental with a `RolloutCompressResponse`, the runtime
  rejects calls from clients without `experimentalApi`, and
  `Router.handleRolloutCompress` schedules the background worker with the `rpc`
  trigger and immediately returns `{}`. It never reads
  `features.local_thread_store_compression`, so the pass runs with the startup
  flag disabled, and the worker's cold-file checks, run marker, and cooldown
  still apply.
- `appserver/rollout_compress_readback_like_rust_test.go` adds the upstream
  lossless-readback assertion: the `.jsonl.zst` payload decodes to exactly the
  original bytes and the plain path still resolves through `rollout.Load`. It
  also pins the local-store guard, rejecting a router whose store has no local
  codex home instead of scheduling a pass with nowhere to run.
- `go test ./appserver -run RolloutCompress` passes.

### Allow browser cleanup hooks on interrupt (Round 873)

# Round 873 - browser cleanup hooks on interrupt

Rust #46029 (`2b2b0fa870429d52b53c61dda73cb0608f6fabae`) allows the curated remote browser cleanup hook to fire on interrupt, not just Stop/SubagentStop, while keeping the registered-connector and empty-input guards on raw Apps tool names.

## What was already true in Go

`appserver/bundled_hooks.go` already listed `HookEventInterrupt` for `browser@openai-curated-remote` and `appserver/runtime_router.go` already scheduled executor cleanup hooks with `HookEventInterrupt` on the interrupt path, filtering via `interruptHooksForCWD`.

## Gap closed

- New `appserver/bundled_hooks_interrupt_like_rust_test.go` mirrors the Rust table: registered interrupted app allowed; colliding connector rejected; manifest-provided arguments rejected.
- Extended `TestExecutorPluginHookSourcesAdmitAppTargetsLikeRust` with `Interrupt` rows for the registered target, a colliding connector, and manifest arguments, so intake-and-routing is pinned for the interrupt event.

Verification: `go test ./appserver -run 'ExecutorPluginHookSources|AllowlistedBundled|Interrupt|CleanupHook'` passes; `gofmt -l` clean on both touched test files.

### Force macOS managed preferences (Round 874)

# Round 874 - forced macOS managed preferences

Rust #46032 (`73bf1812722f9b6fdbfac4997b2d96d7b64ff7c3`) hardens the CoreFoundation managed-preference loader: `CFPreferencesAppValueIsForced` must hold before and after reading `config_toml_base64` / `requirements_toml_base64`, and non-`CFString` values are rejected as `InvalidData`.

## Why this is not applicable

Go has no macOS managed-preference loader to harden. `config/local_layers.go` records the structural difference explicitly: no disk-backed MDM/enterprise-managed layer and no macOS managed admin requirements; the exec-server stack is user + trusted project + file-backed legacy managed config. `config/api.go` keeps `LayerSourceMDM` / `LayerSourceLegacyManagedConfigFromMDM` only as ordering and display source names supplied by the host, never read from CoreFoundation.

Adding a darwin+cgo `CFPreferences` reader would create a call site with no callers in the Go composition and could not be exercised on the supported build targets, so the commit is recorded as `not_applicable` rather than drifting into a parallel unused layer.

### Repair expired Windows sandbox passwords (Round 875)

# Round 875 - repair expired Windows sandbox passwords

Rust #46043 (`51c30ad800a16502dc18ac0019af253670cb9c64`) treats `UF_PASSWORD_EXPIRED` as incomplete setup: full setup is required before logon, refresh-only passes must refuse to proceed, and setup must verify the rotated passwords are no longer expired.

## What Go already covered

The orchestrator already repairs rejected credentials: `storedCredentialsNeedRepair` logs on with both stored accounts, classifies `ERROR_PASSWORD_EXPIRED`/`ERROR_PASSWORD_MUST_CHANGE` (1330/1907) as a credential mismatch, runs one elevated full setup, and reloads credentials. `EnsureLocalUser` already resets only the password through `USER_INFO_1003`, preserving account flags.

## Gap closed

- New `ReadLocalUserFlags` (`NetUserGetInfo` level 1, `NERR_UserNotFound` -> not found) in the setup helper, mirroring Rust `local_user_flags`.
- `sandboxAccountNeedsFullSetup` treats missing, disabled, and password-expired accounts alike.
- Refresh-only setup now fails with `registration refresh cannot repair or replace a sandbox account` before ACL work when either account needs full setup.
- Provisioning and full setup verify neither account is still flagged expired, failing with `Windows sandbox account password is still expired after setup`.
- Tests cover either account expiring, preserved unrelated flags, a single failed repair read, missing accounts, and unreadable accounts ignored after provisioning.

The service-side `registered.rs` portion is not applicable: Go has no Windows sandbox service runtime registration; its refresh-only payload is the equivalent surface and now carries the same gate.

Verification: `go test ./sandbox/windowssandbox/... -count=1`; `go build ./...`; linux/darwin cross-build of the sandbox tree.

### Attach Code Mode metadata to compaction prompts (Round 876)

# Round 876 - Code Mode metadata in compaction prompts

Rust #46044 (`20f4d12f76fc0dbe77bb4052e8acc1615bef5f5d`) attaches recorded Code Mode tool metadata to local and remote compaction prompts, keeps captured Direct records, and keeps observations omitted from shortened compaction retries available for the next normal sampling request.

## What Go already had

`ExecutedToolCallRecorder.AttachToCompactionPrompt` already mirrored this, and `appserver/compact_remote.go` already routed the remote v2 compaction prompt through it. `attachPendingToPrompt(items, false)` keeps the pending/retained state and does not prune Direct observations for a compaction request, and `restoreDirectCallObservations` re-attaches recorded Direct calls.

The per-scope metadata budget from the original commit was superseded upstream by Rust #49401 (no aggregate prompt cap), so Go matches current HEAD by not bounding at compaction time.

## Gap closed

Two regression contracts pin the remaining semantics:

- A compaction prompt with capture enabled keeps a captured Direct record and its completeness marker instead of stripping or rebuilding it.
- A shortened compaction attempt that cannot see the exec output neither grows metadata nor drops observations: the next normal sampling request still attaches the pending inventory, and after commit plus a second shortened retry the retained inventory and completeness marker are re-emitted.

Verification: `go test ./turn ./appserver -run 'Compaction|ExecutedToolCall|CodeMode' -count=1`.

### Keep streamed Mermaid diagrams mutable (Round 877)

# Round 877 - streamed Mermaid diagrams in the TUI

Rust #46054 (`f915e0de0794db24c0ff1419933f81028887069e`) renders completed mermaid fences as diagrams and keeps a streamed diagram mutable while its top-level block is last, so a closing fence, resize or render-mode change can update it before it enters scrollback.

## What Go already had

`tui/markdown/mermaid.go` already rendered completed mermaid fences through the Go `mermaid` package with theme node/edge colors, kept source for unclosed, invalid, unsupported and oversized diagrams, and preserved the original source through `collectSourceCodeBlocks`. Table, bare-list-marker and display-math holdbacks already fed `activeTailSourceStart`.

## Gap closed

- New `markdown.MermaidFenceTailStart` mirrors Rust's `StreamingMarkdownRender::mermaid_start`: the final top-level block's opening line is reported when that block contains a mermaid fence, including fences inside list items and blockquotes, and including unclosed fences.
- `streamCore.activeTailSourceStart` now treats that offset as one more mutable-tail candidate (earliest wins, and the display-math boundary is computed from the combined start), so the diagram stays out of scrollback until another top-level block starts.
- Tests cover the offset probe (standalone, unclosed, list, blockquote, info string, released by a later block, non-mermaid, prose) and the streaming lifecycle: mutable through the closing fence and two resizes, released by the next block, original source retained, and the diagram re-rendered at both widths instead of leaking fence markers.

Verification: `go test ./tui/... -count=1`; `go build ./...`.

### Attribute analytics events to realtime voice sessions (Round 878)

# Round 878 - attribute analytics events to realtime voice sessions

Rust #46058 (`8ace915aced81ed841e34fa069b2e489c324731c`) adds `voice_session_id` to turn, app-use, skill-invocation and MCP tool-call analytics events, and tracks realtime handoff markers so a turn started after the voice session closed stays attributed while later text turns do not.

## Gap closed

**Event params.** `CodexTurnEventParams`, `CodexAppUsedEventParams`, `SkillInvocationEventParams` and `CodexMCPToolCallEventParams` now carry `voice_session_id` (null when absent), matching Rust's event shapes.

**Reducer state.** `appserver/voice_session_analytics.go` adds the per-thread attribution Rust keeps in its reducer: the open voice session, the running turn, and a FIFO of queued handoffs. `turn/started` pops one queued handoff (including a queued empty value, which wins over the still-open session, as Rust's `pop_front().unwrap_or_else` does) or falls back to the open session; `thread/realtime/started` stamps the running turn; `thread/realtime/closed` forgets the open session but keeps the queue; `thread/archived` and `thread/closed` drop all three. `turn/completed` only clears the active-turn marker, because Go emits the turn event after `turn/completed`.

**Handoff markers.** `thread/realtime/itemAdded` with `type == "handoff_request"` records the thread attribution only - the item's transcript never enters analytics state.

**Retention.** The turn-to-voice map is bounded at 256 entries in insertion order, mirroring Rust's `MAX_PENDING_TURNS` eviction of retained turn states.

**Emitters.** Turn, app-used, skill-invocation and MCP tool-call emitters read the stored attribution for their turn.

## Verification

`appserver/voice_session_turn_attribution_like_rust_test.go` covers attribution after the realtime session closes, active-turn steering that does not tag the next text turn, handoff markers that exclude transcripts, a handoff with no open session staying unattributed, archive cleanup, bounded retention, and serialization of all four events.

Commands: `go test ./appserver -run 'Voice|Realtime|TurnEvent|Mcp|MCP|Skill' -count=1`; `go test ./telemetry -count=1`; `go build ./...`.

### Consolidate Guardian tests at shared boundaries (Round 879)

# Round 879 - consolidate Guardian tests at shared boundaries

Rust #46064 (`08663cc91b0bdc4a2f6f3660da089c61d21a7652`) is a test-consolidation commit: approval routing moves to the `guardian-reviewer` policy boundary, the reviewer-configuration checks merge into one isolation test, and `guardian-context` gains ordinary developer messages to its role-filtering coverage.

## Gap closed

Porting the new assertion exposed a real difference. Rust's `guardian-context/src/transcript.rs::collect_transcript` matches the message role and keeps a `developer` message only when its text starts with `MANUAL_APPROVAL_DEVELOPER_PREFIX`; every other developer message hits the catch-all `continue`. Go kept ordinary developer messages - they entered the transcript as `developer: ...` evidence, so parent developer context could reach the reviewer.

`appserver/guardian_reviewer.go` now applies the same rule through `guardianTranscriptDropsDeveloperMessage`, called from `guardianTranscriptExcludedItem`: a developer message survives only as the manual-approval marker, while inherited approvals stay excluded as before.

## Tests consolidated

- `TestAutoReviewEnabledForTurnRoutesApprovalPolicyToGuardian` now covers the full matrix Rust #46064 pins: `unless-trusted`, `on-request`, `granular` and `never` against both `user` and `auto_review`, so only `on-request`/`granular` with `auto_review` route to Guardian.
- `TestGuardianReviewRequestIgnoresParentCustomizationsLikeRust` merges the reviewer-configuration isolation checks: a parent config with `developer_instructions` and a legacy `notify` hook produces the same Guardian request - empty top-level instructions, the rendered Guardian policy fragment, and neither parent string anywhere in the request.
- `TestGuardianTranscriptFiltersRolesAndPreservesManualApprovalLikeRust` adds an ordinary developer message next to the inherited and own approvals, asserting the ordinary message is filtered while the inherited approval stays hidden and the child's own approval still arrives with developer attribution.

## Verification

The transcript test was confirmed to fail with the filter removed. Commands: `go test ./appserver -run 'Guardian|AutoReview' -count=1`; `go build ./...`.

### Inline code and file paths follow the syntax theme (Round 880)

**Upstream:** 172f8a290111 "Use syntax theme colors for inline code and file paths (#46069)".

- Rust resolves `MarkdownStyles::code` from the active syntect theme (two-face/bat bundle) via `foreground_style_for_scopes_with_theme(["markup.inline.raw.string.markdown", "markup.raw.inline.markdown"])`, falling back to `Style::new().cyan()`.
- Go previously had no bat `.tmTheme` data, so Chroma's `GenericOutput` (or nothing) resolved; inline code and local file paths rendered cyan-less or with the wrong tone.
- Added `tui/bat_theme_scopes.go`: the per-theme resolved foregrounds generated from two-face 0.5.1 by running syntect's `Highlighter::style_mod_for_stack`, covering all 32 bundled themes (catppuccin styles the inline-code scope, nord/ansi/gruvbox style the raw-inline scope, dracula/github and friends style neither).
- `tui/theme_scope_foreground.go` consults that table before the Chroma approximation and treats an unstyled scope as authoritative (next scope, then the caller's fallback), mirroring Rust's scope-list resolution.
- `tui/markdown/render.go` projects the resolved colour onto glamour's Code style, which also covers the local file-path labels the renderer emits as code spans, and uses ANSI palette index 6 for Rust's cyan fallback (glamour ignores the colour name).
- Verified with `go test ./tui/... -count=1` (all pass) and `go build ./...`.

### Subagent spawn uses captured step settings (Round 881)

**Upstream:** 800d183e2d0a "Use captured step settings when spawning subagents (#46075)".

- The Go parent controller already mirrored this behaviour (`capturedSpawnSettings` feeds the child's model, effective reasoning effort and reasoning summary, and `resolveSpawnModelOverrides` validates effort-only overrides against the captured model), but only the V2 backend and explicit overrides were covered.
- Added `appserver/agent_controller_step_settings_like_rust_test.go`:
  - V1 spawn after a mid-turn settings update inherits the captured model, effort and summary instead of the thread record's older values.
  - Effort-only overrides are validated against the captured model: an effort the captured model supports is accepted, and one only the turn's initial model supports is rejected with the captured model named in the error.
- Verified with `go test ./appserver -run TestRuntimeAgentController -count=1`.

### Worktree launches skip the shared daemon (Round 882)

**Upstream:** 787823cf9577 "Add --no-daemon to bypass the shared background server (#46088)".

- The Go launch path already mirrored the flag: `--no-daemon` parsing on the root and through `resume`/`fork`, the hidden `agents` spelling, `queue`, the `--remote`/`agents`/`queue` rejections, the agents-overview hint, `daemonStartupExclusion` (no-daemon, oss, workload identity, CODEX_EXEC_SERVER_URL, profile, strict config, bypass hook trust, unreplayable overrides) and the no-start/no-probe daemon target.
- Missing piece: Rust's exclusion order lists `--worktree` immediately after `--no-daemon`; Go treated worktree launches as eligible for shared-server reuse. Added the reason and coverage for the exclusion, the `--no-daemon` precedence, and the daemon-reuse refusal.
- Verified with `go test ./app -run 'Daemon|NoDaemon|Worktree|Interactive' -count=1`.

### V2 spawn description relaxation has no Go counterpart (Round 883)

**Upstream:** 16f49ccd7f72 "Relax delegation guidance in the v2 spawn_agent description (#46264)".

- Rust removed the "Only call this tool for a concrete, bounded subtask that can run independently alongside useful local work; otherwise continue locally." sentence from the default V2 description.
- Go's V2 `spawn_agent` spec (`agent/tools_v2.go`) uses a condensed description that never carried that sentence, so there is nothing to remove; the surrounding guidance the commit keeps is already covered by `agent/multi_agent_v2_spawn_guidance_like_rust_test.go` (inherited-model guidance, usage hints, catalog description overrides).
- Recorded as not_applicable rather than porting the much longer Rust paragraph.

### Cover the expanded Unicode math symbol set (Round 884)

**Upstream:** 608825d51179 "Expand Unicode math rendering with accents, symbols, and delimiters (#46266)".

- Audit: the Go renderer already implemented every part of this change — the extended symbol table (physics, relations, sets, logic, arrows, integrals, `\hbar` and Greek variants), `\hat`/`\bar`/`\tilde`/`\vec`/`\dot`/`\ddot` on single visible graphemes with following scripts, named delimiters both bare and through `\left`/`\right` (including `\left<`/`\right>`), and the grapheme-count base rule — but only the `\hat H` case was tested.
- Added `tui/markdown/math_symbols_delimiters_like_rust_test.go` mirroring Rust's snapshots: every symbol and delimiter family, the Schrödinger display layout, and the invisible / multi-grapheme accent rejection cases.
- Verified with `go test ./tui/markdown -count=1`.

**Also audited:** 841b5490b251 #46112 (filesystem sandbox policy context when the cwd disappears) is now recorded as `partial`: Go's exec-server filesystem helper still launches with `cmd.Dir` set to the policy cwd, so a helper launch after the selected directory is removed still fails, and the artifact carries no `policyContext`/legacy-directory fallback fields.

### Dim conversation recaps in the transcript (Round 885)

**Upstream:** 3cd255a4ee8e "Dim conversation recaps in the TUI (#46332)".

- Rust now dims every rendered recap line (keeping italics) and removes the cyan from the `Next:` label, which stays bold.
- Go rendered recap cells with no styling at all — no dim, italic, or label emphasis. Added `ThreadRecapHistoryCell.StyledDisplayLines` (`tui/history_cell/recap.go`) which paints faint+italic over every line, bolds the `Recap:`/`Next:` label, never emits a colour, and keeps `DisplayLines`/`RawLines` plain for copy and wrapping.
- `tui/tea/recap.go` applies the styled projection for the transcript via a small `styledRecapCell` wrapper, so `/recap` and automatic recaps both render dimmed; `tui/tea/recap_test.go` now strips ANSI before matching the label.
- Added `tui/history_cell/recap_styled_like_rust_test.go` covering the dim/reset on every line, the bold labels, the absence of cyan, and that styling never changes the wrapped text.
- Verified with `go test ./tui/history_cell ./tui/tea ./tui/markdown -count=1`.

### Environment-bound filesystem access has Go equivalents (Round 886)

**Upstream:** 3d3ae4965ab3 "Add filesystem accessors bound to environment permissions (#46268)" and 47fc8d661e42 "Route skill discovery and loading through `EnvironmentAccess` (#46293)".

- #46268 adds the `EnvironmentAccess` trait, `FileSystemEnvironmentAccessor` (a borrowed filesystem plus a captured `FileSystemSandboxContext`), the unrestricted constructor and an opaque cache key, plus `Environment::filesystem_ref()`. It is Rust-internal API hygiene: callers stop being able to pick their own sandbox for a shared filesystem handle.
- Go has no such handle to protect. `tool.EnvironmentFileSystem` (`tool/environment_filesystem.go:42`) takes the sandbox per call, and the per-environment permission binding already lives in `appserver/executor_skill_sandbox.go:18` (`executorSkillSandboxContextsForTurn`), which builds one `*execserver.FileSystemSandboxContext` per environment and refuses restricted reads without one (`requireExecutorSkillSandboxContext`, `appserver/executor_skill_sandbox.go:261`).
- The opaque identity key has a direct Go counterpart in `executorSkillSandboxContextKey` (`appserver/executor_skill_sandbox.go:219`), used as the skill-root cache identity at `appserver/turn_runtime.go:10789`.
- #46293 only rewires discovery/loading/namespace callers onto the accessor with `unrestricted`, so access semantics do not change. Go already makes the same decision per call: `readExecutorSkillEntryResource` (`appserver/executor_skill_provider.go:310`) uses the captured sandbox through `readLocalEnvironmentSkillTextWithSandbox`/`readRemoteEnvironmentSkillTextWithSandbox` when a context exists and the unrestricted `os.ReadFile` otherwise.
- Both recorded as not_applicable; no code change and no new test, because neither commit carries a behavioural delta.

### Keep code-mode overhead merges structured (Round 887)

**Upstream:** 0fd1cd8d99b6 "Add opt-in overhead timing to code-mode responses (#46288)".

- Go already had the feature: `features.code_mode.experimental_show_cell_overhead` is validated as a known field (`config/config.go:1485`), read by `Config.CodeModeExperimentalShowCellOverhead` (`config/config.go:2260`), and threaded into `codeModeResponseHeader` (`tool/code_mode_exec.go:1993`) from both `exec` (`exec/exec.go:568`) and app-server turns (`appserver/runtime_router.go:16484`).
- Header formats match Rust exactly: one decimal by default, three decimals with `(code-mode X seconds; overhead Y seconds)` when the option is on and host timing exists, the default form when it is off or host timing is absent, and negative rounding differences preserved. Host-duration plumbing is pinned by `tool/code_mode_host_timing_test.go`; the toggle by `TestCodeModeExperimentalShowCellOverheadLikeRust`; the header shapes by `TestCodeModeResponseHeaderMatchesRustFormat`.
- Real gap found: `structuredFeatureName` (`config/config.go:3768`) did not list `code_mode`, so Rust's `is_structured_feature_path` addition was missing. A legacy `features.code_mode = true` merged with a nested code-mode table — including `-c features.code_mode=true` alongside `-c features.code_mode.experimental_show_cell_overhead=true` — dropped the enable.
- Fixed by adding `code_mode` to the structured feature list and switching `config/override.go` from the multi-agent-only check to the shared structured-feature check, matching Rust's `apply_toml_override`. Added `config/code_mode_overrides_like_rust_test.go` covering both override orders, both merge directions, `features` and `profiles.work.features`, and the resolved feature settings.
- Verified with `go test ./config -count=1` and `go build ./...`.

### Synchronous Guardian reviews keep their selected effort (Round 888)

**Upstream:** 93321c88d8e4 "Preserve selected reasoning effort for synchronous Guardian reviews (#46292)".

- Go's reviewer path already used its selected effort (`model.AgentRequest.ReasoningEffort = plan.Selection.ReasoningEffort`, `appserver/guardian_reviewer.go:1175`), and reviewer prompts are rendered transcripts, so inherited `configuration_update` items never reach a reviewer request.
- The real gap was the single client-level gate: `reasoningEffortOverrideExempt` (`appserver/reasoning_effort_override.go:138`) exempted only memory consolidation and ephemeral `thread_title`, so a turn on a guardian-sourced thread still filtered, pinned, and injected effort updates when managed requirements enabled `reasoning_effort_override`.
- Rust #46292 disables the override in `ModelClient` for `guardian::is_basic_session_source`, covering both `SubAgent(Other("guardian"))` and `Internal(Guardian)`. Added `guardianReviewerSessionSource` (`appserver/reasoning_effort_override.go:126`) and an exemption keyed on the recorded session source, so request filtering, effort pinning, and update injection share one decision while the parent thread keeps its pin.
- Added `appserver/guardian_reasoning_effort_like_rust_test.go`: both source forms, no trusted update recorded or injected, selected `low`/`high` efforts honored with no pin retained, the parent's pin and update still applied, and inherited updates dropped from the request copy without touching saved history.
- Verified with `go test ./appserver -run 'TestSyncGuardianKeepsSelectedReasoningEffortLikeRust|TestReasoningEffort|Guardian' -count=1` (the new test fails when the exemption is disabled) and `go build ./...`.

### Catalog descriptions cover every multi-agent V2 tool (Round 889)

**Upstream:** 3e581ebca854 "Support catalog descriptions for all multi-agent V2 tools (#46297)".

- Go already resolved catalog overrides for all six V2 tools: `MultiAgentToolOverridesFromCatalog` (`turn/tools.go:262`) walks `agent.MultiAgentV2ToolNames` (`agent/tools.go:346`), and `applyMultiAgentToolOverride` (`agent/tools_v2.go:296`) replaces text for every tool except `spawn_agent`, whose `Spec` substitutes the catalog description first and then appends inherited-model guidance and the usage hint (`agent/tools_v2.go:204`).
- Rules match Rust: missing or null keeps bundled text, an empty string suppresses the static text without unregistering the tool, parameter schemas stay bundled, and resolution does not depend on the configured namespace.
- The existing tests covered `send_message`, `list_agents` and `spawn_agent` only. Added `agent/multi_agent_v2_catalog_descriptions_like_rust_test.go`, which compares a baseline registry with an overridden one registered under a custom namespace and checks all six tools: catalog text for `send_message`/`followup_task`/`wait_agent`/`interrupt_agent`, empty-string suppression for `list_agents`, bundled text plus unchanged input schemas for tools without an entry, and `spawn_agent`'s override-before-guidance ordering with the usage hint last.
- Verified with `go test ./agent -count=1`.

### Active-turn id lookups skip the snapshot clone (Round 890)

**Upstream:** 55db7e8c8870 "Avoid cloning turn items for app-server active turn lookups (#46305)".

- Rust exposed `ThreadState::active_turn_id` so interrupt validation, elicitation turn-id fallback, and teardown logging stop building turn snapshots. Go's `ThreadManager.ActiveTurn` (`appserver/thread_manager.go:722`) clones the turn via `cloneActiveRuntimeTurn` -> `cloneTurnStartParams`, so id-only callers paid the same cost.
- Added `ThreadManager.ActiveTurnID` (`appserver/thread_manager.go:734`) and used it for the manual-compaction abort (`appserver/runtime_router.go:9182`), Guardian denial accounting (`appserver/guardian_reviewer.go:2287`), the review-turn probe (`appserver/turn_runtime.go:3858`), and as the pre-clone id check inside `interruptRuntimeTurn`. The snapshot is still materialized only when the id matches, so wake-reservation and turn-finishing behavior is unchanged.
- Two Rust call sites have no Go counterpart: Go has no elicitation turn-id fallback (tool-call elicitations always carry the turn) and no `had_active_turn` teardown log.
- Added `appserver/active_turn_id_lookup_like_rust_test.go`: nil and idle managers, whitespace-tolerant lookup, agreement with the snapshot, continued snapshot isolation from the stored turn, and Guardian lookup before and after `ConsumeTurn`.
- Verified with `go test ./appserver -run 'Interrupt|Compaction|Guardian|ActiveTurn|Wake' -count=1`, `go test ./turn -count=1`, and `go build ./...`.

### Defer network policy validation until after composition (Round 891)

**Upstream:** ad70cbdd966b "Defer environment network policy validation until after composition (#46331)".

- Rust stopped validating the selected environment `[network]` table at projection time: `project_environment_profile_network` now only drops listener addresses and rejects unsupported controller fields, while domain patterns and socket paths are checked by `validate_environment_network_policy` against the composed policy, so values that feature settings or managed requirements replace are never rejected first.
- Go had the same premature rejection: `ProxyConfigFromConfigValues` (`network/proxy_config_values.go:9`) composed `features.network_proxy` and the selected `permissions.<profile>.network` chain and then ran `ResolveProxyRuntime`, failing on invalid domain globs or socket allow paths before managed requirements could replace them. Parsing now keeps the selected values untouched and only keeps the parse-level checks (`ValidateProxyMITMHookConfig`, credential-broker-requires-mitm); listener addresses are still resolved when the proxy starts.
- Final validation gained the missing half: `ValidateProxyDomainPatterns` (`network/proxy_config.go:323`) compiles the allow, deny and review patterns; `NewProxySpecForPlatformWithLocalBinding` (`network/proxy_constraints.go:160`) calls it after `applyProxyRequirements` composed the managed values, and `ResolveProxyRuntimeForPlatform` reuses it.
- Tests mirror Rust's projection cases: `network/network_policy_deferred_validation_like_rust_test.go` carries a bracket-only domain pattern, `relative.sock`, `~/allowed.sock` and a NUL socket through projection, fails when the entry is retained, and passes when requirements replace it; `TestComposedProxyPolicyRejectsMalformedDomainPatterns` pins composed-policy rejection of malformed allow/review patterns; `appserver/managed_network_deferred_validation_like_rust_test.go` drives the production managed-network builder with replacing requirements.
- Verified with `go test ./network -count=1`, `go test ./appserver -run 'ManagedNetwork|NetworkProxy|NetworkPolicy|NetworkGrant' -count=1`, `go test ./config -run Network -count=1`, `go build ./...`, and a negative check that restoring the eager `ResolveProxyRuntime` fails both new tests.

### Close plugin metadata, turn analytics and backoff items (Round 892)

**Upstream:** 0c9be8a836a6 (#46309), a1efb59c4a17 (#46323), ff73d63e64e2 (#46330).

- `0c9be8a836a6` plugin caches across display metadata refreshes was already implemented in `862faaa5`: `InstalledPluginMetadataEqual` (`plugin/remote_metadata.go:23`) and `RemoteCatalogMetadataEqual` (`:63`) compare only identity, version, enablement, policy and availability (catalog comparison ignores plugin display order, marketplace order stays significant), `PluginService.ReplaceInstalledRemotePlugins` (`plugin/api.go:1996`) publishes the fresh payload but reports only behavioral changes, and `appserver/plugin_remote_installed.go:141` gates `effectivePluginsChanged` on that result so a display-only refresh keeps live MCP sessions and cached skills. Re-verified `go test ./plugin -run Metadata -count=1`, `go test ./appserver -run TestRemoteInstalledPluginDisplayRefreshPreservesDerivedCachesLikeRust -count=1`, and the negative check that forcing `effectivePluginsChanged` fails the app-server test.
- `a1efb59c4a17` active plugin inventory in turn analytics was already implemented: `activePluginIDsForTurnAnalytics` (`appserver/turn_analytics_plugins.go:65`) and `activePluginTelemetryIDs` (`:91`) select remote ids over validated package keys and return null on invalid, over-long (>128 bytes) or oversized (>512) inventories, while `turnAnalyticsPluginInventory` (`:27`) memoizes the first observation; `telemetry/turn_event.go:93` serializes `active_plugin_ids_at_turn_start`. Re-verified with `go test ./appserver -run 'TestTurnAnalyticsPluginInventory|TestRuntimeRouterTurnAnalyticsPluginInventory|TestActivePluginTelemetryIDs' -count=1`, `go test ./telemetry -run TurnEvent -count=1`, and a negative check that widening the bounds to 513/129 fails the bounds test.
- `ff73d63e64e2` is a Rust-only crate move (backoff from `codex_core::util` to `codex_async_utils`, re-exported, delays and jitter unchanged) so cloud-config can drop its runtime dependency on core: not applicable, because Go has no crate boundary and the same 200 ms doubling with 0.9..1.1 jitter already lives in `responsesRetryDelay` (`model/responses_agent.go:4749`, annotated as mirroring `codex_async_utils::backoff`).

### Tighten error classification decoding and close test-suite refactor (Round 893)

**Upstream:** 3d5b66c655c1 (#46482), 2f522c5dc0a0 (#46488).

- `3d5b66c655c1` saved-record error classifications: `normalizeCodexErrorInfoClassification` (`rollout/codex_error_info.go:67`) already preserved known tags, including `bio_policy`, and mapped unknown tags to `other` so terminal records stay readable. Rust deserializes `CodexErrorInfo` through an externally tagged enum, so this round tightened the shapes Go used to classify as `other`: empty and multi-key objects, unit tags carrying a payload (including `other`), payload tags written as a bare string, and non-string/non-object JSON are now unreadable. Tests: `rollout/codex_error_info_test.go` gained a record-level multi-key case and `TestCodexErrorInfoRejectsNonEnumShapesLikeRust`.
- `3d5b66c655c1` gateway OAuth provider settings were already implemented: `GatewayOAuthConfig` (`model/provider_info.go:197`) with header and cookie delivery, reserved-header/prefix rejection and `Validate` (`:282`), parsing in `model/provider_config.go:405`, and secondary-credential composition in `model/gateway_auth.go:121`, covered by `model/gateway_oauth_test.go` and `model/gateway_auth_test.go`.
- `2f522c5dc0a0` is a Rust test-layout refactor (monolithic `analytics_client_tests.rs` split into per-event suites): not applicable, because the Go analytics surface is already covered per topic in `telemetry/*_test.go` and `appserver/turn_analytics_plugins_test.go`.
- Verified with `go test ./rollout -count=1`, `go test ./model -run Gateway -count=1`, `go build ./...`, and a negative check that reverting the multi-key rule fails the new shape test.

### Group exploration reads and annotate exit codes (Round 894)

**Upstream:** 71406edbd58e (#46487), 4f3867123f4a (#46492).

- `71406edbd58e` exploration grouping: `ShouldFlush` (`tui/exec_cell/model.go:122`) already kept adjacent read/list/search calls grouped after a failed call, and `AppendReasoning` (`:141`) plus `TranscriptLinesWithReasoning` (`tui/exec_cell/render.go:219`, wired at `tui/tea/model.go:5191` and `:7700`) already kept reasoning inside the group in transcript order while raw and compact history omit it. The missing content was the compact exploring renderer: `exploringDisplayLines` (`tui/exec_cell/render.go:238`) now collapses adjacent successful reads into one `Read a, b` summary with unique names, stops that group before a failed or non-read call so unsuccessful reads stay separate, and annotates each call's last line with `(exit N)` or `(command exit N)` for compound commands (`exploringNonzeroExit`/`exploringReadsOnly`, `:323`/`:332`). The header keeps the later neutral `· N nonzero exit(s)` count.
- Residual, recorded rather than claimed: Go's exploring rows are emitted unstyled, so Rust's red/dim annotation colors and bold header are not represented by this renderer.
- Tests: `tui/exec_cell/exploration_grouping_like_rust_test.go` covers grouping, the failure split, search exit 1 and compound `command exit`, alongside the existing group-stays-open and nonzero-count tests.
- `4f3867123f4a` tool output previews were already implemented: `tui/tool_output_preview.go` mirrors Rust `tool_output.rs` (three rows, 16 KiB input bound, partial logical line counts as hidden, hard-wrapped URLs, hidden marker with the `ctrl+t` hint), and `tui/history_cell/mcp.go:131`/`:163` applies one preview budget across all MCP blocks while transcript and raw output keep the full text, covered by `tui/tool_output_preview_test.go` and `tui/history_cell/mcp_preview_test.go`.
- Verified with `go test ./tui/exec_cell -count=1`, `go test ./tui ./tui/exec_cell ./tui/history_cell -count=1`, `go build ./...`, and a negative check that disabling read grouping fails the new test.

### Worktree sessions reuse an existing local daemon (Round 895)

Closed #46498 (e49739355235) worktree daemon eligibility.

- `app/daemon_startup.go:79` `daemonStartupExclusion` no longer returns `--worktree`; `--no-daemon` still wins. A worktree launch may now attach to an existing shared server.
- New `daemonAutoStartRequested` (`app/daemon_startup.go:312`) mirrors Rust `startup_orchestration` `auto_start_daemon`: `--worktree` never starts the shared server and keeps embedded fallback. `app/tui_telemetry.go:195` and `app/interactive.go:1098` use the same gate.
- Boolean `features.worktrees` overrides were already replayable via `daemonOverridesAreReplayable`; `worktrees` is an audited feature key and not a shared-server feature, so `features.worktrees=false` stays eligible.
- Tests: `app/daemon_startup_test.go` `TestDaemonStartupExclusionLikeRust`, `TestLocalDaemonEndpointForLaunchLikeRust`, `TestDaemonAutoStartSkipsWorktreeLikeRust`; `app/tui_telemetry_like_rust_test.go` `TestInteractiveDaemonSelectionReasonLikeRust`.
- Verified: `go test ./app -run 'Daemon|Worktree|Telemetry|InteractiveDaemon' -count=1` ok; `go build ./...` ok; `go test ./app -count=1` only the known unrelated `TestExecJSONEndToEnd` failure; negative check re-adding the `--worktree` exclusion fails the new tests.

### Deny mutating fcntls in restricted Seatbelt policies (Round 896)

Closed #46500 (04e4d2b40ffd): restricted Seatbelt policies deny mutating fcntls.

- `sandbox/seatbelt.go:181` appends `(deny system-fcntl (fcntl-command 80 110))` whenever the policy lacks full disk write access (`F_MAKECOMPRESSED` = 80, `F_TRANSFEREXTENTS` = 110).
- The denial is the final rule, so no earlier `file-write*` or `file-ioctl` allowance reopens the mutation path; a full-access policy keeps both operations allowed, matching Rust.
- Test: `sandbox/seatbelt_test.go:160` `TestBuildSeatbeltPolicyDeniesMutatingFcntlsLikeRust`.
- Residual: Rust also runs real Seatbelt children on macOS and asserts `EPERM` plus unchanged contents/metadata; only the generated policy text is verified here.
- Verified: `go test ./sandbox -count=1` ok; negative check removing the deny fails the new test.

### Usage diagnostics on the sampling span (Round 897)

Closed #46501 (f5b941c9107a) regression coverage for the usage diagnostics on the sampling span.

- `appserver/usage_tags.go:29` `UsageTagsForTurn` carries the full 28-key scalar allowlist plus one `feature.<key>` tag per registry feature (excluding the `Collab`/`SpawnCsv` equivalents), with absent values rendered as `unset`.
- `appserver/turn_runtime.go:8326` feeds `cfg.FeatureSettings()` per turn; `turn/telemetry_span.go:53` records the sorted document as `tags_json` on the `run_sampling_request` span.
- `appserver/usage_tags_test.go` now merges a second turn into the first tag map, proving `unset` overwrites the previous override.
- Recorded nuance: the feedback-crate half (scraping `tags_json` from tracing and raising `MAX_FEEDBACK_TAGS` 64 → 512) has no Go counterpart because Go feedback uploads take caller-supplied tags, as noted in `update/plan_2026_09_19.md:2009`.
- Verified: `go test ./appserver -run UsageTags -count=1` ok; `go test ./turn -run 'UsageTags|SamplingSpan' -count=1` ok.

### Restrict the Windows native palette fallback to ConsoleWindowClass (Round 899)

Closed #46504 (907b751eabc0) by adding the last missing piece: the Windows native palette fallback is now restricted to the native console window class.

- Already present and audited: six bundled `tmTheme` assets (`ada`, `babbage`, `curie`, `cushman`, `dali`, `davinci`) with picker registration, per-theme chroma styles and `codex.accent`; theme-aware accent at truecolor/256 colours; terminal-default diff backgrounds with matching gutter clears.
- New: `tui/terminal_default_colors_windows.go:53` `consoleWindowClassIsNative` reads the console window class (`GetConsoleWindow` + `GetClassNameW`), and `platformTerminalDefaultColors` (`:66`) refuses the console color table unless the class is exactly `ConsoleWindowClass`, so a ConPTY `PseudoConsoleWindow` palette is never used when OSC probing fails.
- Tests: `tui/terminal_default_colors_windows_test.go:88`, `:108`, `:131`.
- Verified: `go test ./tui -count=1` ok; negative check removing the class gate fails `TestNativePaletteFallbackHonorsConsoleClassLikeRust`.

### Refresh Multi-Agent V2 catalog schemas on mid-turn model changes (Round 900)

Closed #46505 (cc7591646ef1) by making the Multi-Agent V2 catalog parameter schemas follow a mid-turn model change.

- `agent/tools.go:352` `MultiAgentToolOverrideStore` keeps the turn's overrides behind a mutex; `agent/tools_v2.go:127` `overrideFor` reads the live store, so replacing it updates the tool schema without re-registering executors.
- `turn/runtime.go:88` `RuntimeOptions.MultiAgentOverridesForModel` runs inside `ToolsForStep`, so each later sampling step refreshes before the tool list is rebuilt.
- `appserver/runtime_router.go:16872` creates the store for every Multi-Agent V2 turn and records it on the active turn (`appserver/turn_runtime.go:110`); `multiAgentOverrideRefresher` (`appserver/runtime_router.go:15989`, wired at `:16127`) resolves the step model's catalog and replaces the store.
- Existing behavior retained: object-root/subset validation, encrypted-property annotations, bundled fallback for invalid or missing overrides.
- Tests: `agent/tools_v2_overrides_test.go` `TestMultiAgentV2OverrideStoreRefreshesForModelChangeLikeRust`; `turn/agent_loop_test.go` `TestRuntimeRefreshesMultiAgentCatalogOverridesForStepModelLikeRust`.
- Verified: `go test ./agent ./turn -count=1` ok; `go test ./appserver -run 'TurnStepSettings|StepCapabilities|MultiAgent' -count=1` ok; `go build ./...` ok; negative check removing the step refresh fails the new turn test.

### Worktree launches auto-start the shared daemon (Round 901)

- `app/daemon_startup.go`: `daemonAutoStartRequested` no longer excludes `--worktree` (only `--no-daemon` opts out), matching Rust #46529's "resolve worktree configuration before daemon selection" ordering; the endpoint and selection-reason comments now describe the worktree auto-start path.
- `app/daemon_startup_test.go`: `TestDaemonAutoStartAllowsWorktreeLikeRust` replaces the old skip test. A worktree launch requests startup, calls the managed-server start with the worktree root, returns the started control socket, and reports selection reason `auto_start`; `--no-daemon --worktree` stays embedded and never starts a server. Existing exclusion/endpoint cases were re-labelled to #46529.
- `app/tui_telemetry_like_rust_test.go`: worktree selection cases now expect `auto_start` when auto-start is enabled and `existing_daemon` when only a running server is reused.
- `app/thread_config_overrides_like_rust_test.go`: `suppress_unstable_features_warning` travels with the thread configuration overrides beside audited feature toggles (Rust `config_request_overrides_from_config`), and a launch without the switch forwards nothing for it.
- Already aligned through later upstream state: the audited boolean feature allowlist plus `suppress_unstable_features_warning` in `daemonConfigExclusion`, shared-service compatibility checks in `app/daemon_compatibility.go`, and the agents overview resolving the shared server without the feature check.
- Verification: `go test ./app -run 'Daemon|Worktree|Telemetry|SelectionReason|SuppressUnstable' -count=1` and `go test ./appserverdaemon -count=1` pass; full `go test ./app` only reports the pre-existing `TestExecJSONEndToEnd` websocket failure.

### Share ChatGPT cookies across WebSocket transports (Round 902)

- `network/chatgpt_cookies.go`: `WebsocketHandshakeCookies` decorates a handshake header set with the process-global ChatGPT infrastructure cookies (Rust `websocket-client` `connect_with_route`, #46506). An explicit caller `Cookie` header wins, the caller's map is never mutated, and out-of-scope targets (`ws`, non-ChatGPT hosts) keep their headers untouched.
- `model/responses_agent.go`: the prewarm handshake and `runWebSocket` both route through that helper, and `runWebSocket` now feeds handshake responses - including rejected upgrades - into `StoreChatGPTResponseCookies`.
- `realtime/wire.go`: `dialRealtimeTransport` attaches the shared cookies before dialing and stores allowlisted response cookies from successful or rejected upgrades.
- `network/chatgpt_cookies_test.go`: `TestWebsocketHandshakeCookiesMatchRust` pins precedence, immutable inputs, nil headers, and the `ws`/non-ChatGPT scope exclusions.
- `model/responses_websocket_cookie_like_rust_test.go` and `realtime/realtime_cookie_like_rust_test.go`: local TLS servers routed behind a `*.chatgpt.com` hostname prove that `__oailb` rides the handshake, that a 403 upgrade refreshes it, and that the next connector sends the refreshed value.
- Residual: Go has no per-header sensitive marking equivalent to `HeaderValue::set_sensitive`.
- Verification: `go test ./network ./realtime -count=1` and `go test ./model -count=1` pass.

### Rename the local agent control handle (Round 903)

- Rust #46513 renames the session-local agent control handle to `LocalAgentControl` across `codex-rs/core/src/agent/control.rs` and its submodules; no behavior changes.
- Go has no `AgentControl` identifier to disambiguate. The equivalent local handle is `agent.ToolController` (`agent/tools.go`) implemented by `appserver.runtimeAgentController` (`appserver/agent_controller.go`), which is already distinct from `model.LocalAgentRunner` and the remote agent-controller paths.
- No Go code change or regression coverage applies; recorded as `not_applicable`.

### Share process-group termination with the macOS member fallback (Round 904)

- New `processgroup` package mirrors Rust `codex_utils_pty::process_group`. `Terminate` and `Kill` signal the whole group on Unix; on macOS a denied (`EPERM`) group signal is retried against the group's members, enumerated through libproc `proc_listpgrppids` via `purego`, skipping members that exited or moved to another group and signalling the leader last.
- Shared helpers replace the per-caller `syscall.Kill(-pid, ...)` calls: `mcp/stdio_process_unix.go` (SIGTERM, escalating to SIGKILL only when a live group accepted the term), `appserver/command_exec_process_unix.go`, `appserver/hook_process_tree_unix.go`, `gitutil/gitutil_unix.go`, and `tool/shell_snapshot_process_unix.go` (where ESRCH now simply means the group is gone).
- `processgroup/processgroup_test.go` covers member retry, leader-last ordering, ESRCH and other-group skips, first-error precedence, invalid IDs, and non-permission errors.
- Residual: without cgo, a `proc_listpgrppids` failure reports a generic error rather than the platform errno.
- Verification: `go test ./processgroup ./mcp ./tool ./gitutil -count=1`, `go test ./appserver -run 'Command|Hook|Process|Exec|Kill' -count=1`, and `GOOS=darwin`/`GOOS=linux` builds of every touched package succeed.

### Drop the runningboard allowance from Seatbelt defaults (Round 905)

- Rust #46532 removes the `com.apple.runningboard` allow line from `codex-rs/sandboxing/src/seatbelt_read_only_platform_defaults.sbpl`.
- Go has no Seatbelt platform-defaults asset: `sandbox/seatbelt.go` builds every policy as `(version 1)` plus `(allow default)` and then adds explicit deny rules, so it never enumerates macOS system services and never granted `com.apple.runningboard`.
- Nothing to remove or retest; recorded as `not_applicable`, consistent with the earlier record for the same profile file (`75388bf3`).

### Default new TUI threads to no reasoning summaries (Round 906)

- Rust #46533 defaults a new TUI thread's `model_reasoning_summary` to `none` and stops enabling summaries when only `features.concurrent_reasoning_summaries` is set.
- Go's TUI already follows the later #50811 rule that Rust HEAD keeps: `model_reasoning_summary` is forwarded only as a winning launch choice and is never derived from a default or from the concurrent-summary feature (`app/interactive.go`, `app/remote_tui.go`), so a fresh thread leaves the value to the server while explicit settings still travel.
- `app/embedded_reasoning_test.go`: `TestNewTUIThreadReasoningDefaultsLikeRust` pins the empty default, concurrent-only feature forwarding without a summary override (embedded and remote thread config), and an explicit `auto` value.
- Verification: `go test ./app -run 'Reasoning|LaunchSetting|NewTUIThread' -count=1` passes.

### Allow namespace mounts in Linux socket checks (Round 907)

- `sandbox/linuxsandbox/daemon_mounts.go`: every mount destination is now validated, and a root is treated as a filesystem path only when it parses as one, so namespace mounts with `mnt:[inode]` or `net:[inode]` roots are checked by destination alone instead of aborting sandbox startup (Rust #46535).
- `mountinfoPath` replaces the lenient unescape helper: only the four standard escapes are accepted and the decoded destination must be absolute, so a relative destination or a malformed escape rejects startup (Rust `mount_path`). Destination ancestry, nested mounts, socket aliases, and the WSLg exclusion are unchanged.
- `sandbox/linuxsandbox/daemon_mounts_test.go`: `TestValidateDaemonSocketMountIsolationNamespaceMountsLikeRust` accepts unrelated mount/network namespaces and rejects a nested namespace mount, a relative destination, and an invalid destination escape. `TestValidateDaemonSocketMountIsolationNamespaceMountKeepsAliasChecksLikeRust` proves a real socket alias is still rejected next to an unrelated namespace mount.
- Verification: `gofmt` clean, `GOOS=linux go vet ./sandbox/linuxsandbox` and `GOOS=linux go test -c ./sandbox/linuxsandbox` succeed; the same logic re-run without the linux build tag on POSIX path semantics passes every case on this Windows host.

### Bound doctor filesystem path diagnostics (Round 908)

- `doctor/filesystem_paths.go` already reports `sandbox.filesystem_paths` with literal grants, merged access modes, resolution outcomes, budgets, and provenance, excluding deny rules and denied paths; the hidden probe helper short-circuits before configuration loading, and `FeedbackDoctorReportTimeout` is already 35 seconds.
- Added the missing regression seams - `filesystemProbeRunner`, `filesystemProbeSupported`, `filesystemProbeClock` - so probe outcomes and budgets are testable on every platform without spawning helpers or sleeping.
- `doctor/filesystem_paths_test.go`: timeout and denied paths warn with the mount/host remedy, a missing path reports without a warning, a resolved path stays clean, the 32-path ceiling reports one unchecked path, and a slow probe that consumes the eight-second budget leaves the rest unchecked.
- `app/doctor_probe_helper_like_rust_test.go`: `doctor --probe-filesystem-path` exits silently with the probe status (2 on POSIX, 5 on Windows) even with an unparsable `config.toml`, proving the helper never loads configuration.
- Verification: `go test ./doctor -count=1` and `go test ./app -run Doctor -count=1` pass.

### Advertise control socket message limits (Round 909)

- Upstream (`98a8d4ea9cab`, #46548) advertises `x-codex-websocket-max-unfragmented-message-bytes` on accepted control socket upgrades so clients can reject oversized single-frame requests before the socket closes, using the same `WebSocketConfig` for the connection.
- `appserver/unix_socket.go` now sets that header via `advertiseControlSocketMessageLimit` on the managed `/daemon/shutdown` upgrade and on every other path, and bounds the connection with the same value through `applyControlSocketReadLimit` (`DefaultWebSocketMaxMessageSize`, 128 MiB).
- A refused shutdown upgrade keeps the bare `403 unmanaged server` response with no advertised limit, matching Rust inserting the header only after the access check.
- Regression: `appserver/unix_socket_control_socket_like_rust_test.go` adds `TestControlSocketAdvertisesUnfragmentedMessageLimitLikeRust`, `TestManagedShutdownSocketAdvertisesUnfragmentedMessageLimitLikeRust`, and `TestRejectedShutdownSocketOmitsUnfragmentedMessageLimitLikeRust`.
- Verified with `go build ./appserver`, `GOOS=darwin go test -c ./appserver`, and `go test ./appserver -run 'UnixSocket|ControlSocket|Socket|Websocket' -count=1`.

### Return LiveAgent records from agent listing (Round 910)

- Upstream (`c6f5d9e9b5d9`, #46553) makes `LocalAgentControl::list_agents` return shared `LiveAgent` records (thread id, metadata, status) and moves the `ListedAgent` conversion into the `list_agents` tool handler, keeping the response fields and adding a thread-id fallback for agents without a path.
- `agent/tools_v2.go` adds `LiveAgent` and changes `V2ToolController.ListAgents` to return `[]LiveAgent`; `ListedAgentsFromLive` now formats `agent_name` from the record path, falls back to the thread id, converts the status, and sorts by name.
- The exec, appserver (`runtimeAgentController`), role-aware, and memory controllers return live records; the root entry carries the root thread id with the `/root` path.
- Regression: `agent/list_agents_live_agent_like_rust_test.go` adds `TestListedAgentsFromLiveFallsBackToThreadIDLikeRust` and `TestListAgentsHandlerFormatsLiveRecordsLikeRust`; the exec/appserver controller tests now convert through the shared helper.
- Verified with `go build ./...`, `go test ./agent -count=1`, `go test ./exec -run 'ListAgents|MultiAgent|AgentController'`, and `go test ./appserver -run 'ListAgents|AgentController|PrefixBoundary'`.

### Always use private desktops for legacy Windows sandboxes (Round 911)

- Upstream (`a633ebc1245f`, #46554) removes the private-desktop opt-out from elevated and unelevated Windows sandbox launches, deletes `windows.sandbox_private_desktop` plus its managed requirement and API fields, and warns about the obsolete setting.
- The Go sandbox layer no longer carries the opt-out: `CaptureRequest`, `PipeSpawnRequest`, `ProcessSpawnRequest`, `conpty.SpawnRequest`, `elevated.SpawnRequest` and their IPC wire form dropped `UsePrivateDesktop`, and the process/ConPTY launchers always prepare a private desktop. The wrapper always passes `--windows-sandbox-private-desktop`.
- `execserver` dropped its `windowsSandboxPrivateDesktop` sandbox-context field; its MXC gate now only rejects a custom `argv0` with `MXC custom argv0 is not supported`, matching the upstream message. `tool` `ShellOptions`/`ShellRequest` and the exec-server `FileSystemSandboxContext` no longer expose an opt-out, and `appserver`'s `windowsSandboxPrivateDesktopForTurn` helper is gone.
- Regression: `sandbox/windowssandbox/wrapper_private_desktop_like_rust_test.go` adds `TestWindowsSandboxWrapperAlwaysPassesPrivateDesktopLikeRust`; `execserver` MXC, `tool` shell/runner tests updated; the obsolete-setting warning stays covered by `config/windows_requirements_test.go`.
- Residual: Rust also requires a caller-owned private desktop name so the desktop outlives the short-lived wrapper (`--windows-sandbox-private-desktop-name`); Go still has the wrapper child create its own desktop and accepts the marker flag as a no-op.
- Verified with `go build ./...`, `go vet ./app ./exec ./agent ./execserver ./tool ./sandbox/windowssandbox`, `go test ./sandbox/...`, `go test ./tool`, `go test ./execserver`, and `go test ./appserver -run 'Sandbox|Windows|ExecutorSkill'`.

### Fix active-turn environment selection lookup (Round 912)

- Upstream (`cd2f9ca69239`, #46557) makes `active_turn_environment_selections` read `initial_environments` so hosts authorize steering against the environment snapshot captured at turn start.
- Go already reads that snapshot: `RuntimeRouter.threadEnvironmentSelections` returns `activeTurnParams(threadID)`, and `activeTurnParams` clones the params stored by `registerActiveTurnRuntimeTurn`, so a selection saved for the next turn cannot rewrite the running turn; once the turn ends the lookup falls back to the persisted `runtime_environments` metadata.
- Regression: `appserver/active_turn_environment_selections_like_rust_test.go` adds `TestThreadEnvironmentSelectionsUseTurnStartSnapshotLikeRust`, which registers a turn with `env-initial`, mutates the caller's params to `env-next`, asserts the turn keeps `env-initial`, and asserts the persisted `env-saved` selection applies after the turn ends. It complements the existing MCP snapshot test.
- Verified with `go test ./appserver -run TestThreadEnvironmentSelectionsUseTurnStartSnapshotLikeRust` and `go test ./appserver -run 'EnvironmentSelections|EnvironmentPolicy|EnvironmentCapability'`.

### Fix standalone network proxy policy initialization (Round 913)

- Upstream (`29a57861e31a`, #46578) passes `Platform::native()` to `build_config_state` in the standalone network proxy so its policy is validated for the host executor OS before listeners start.
- `network.StaticNetworkProxyConfig.Validate` now runs `ValidateProxyUnixSocketAllowlistPathsForPlatform` with `utils.NativePlatform()` and requires `network.mitm` when `credential_broker` is enabled, matching the other `build_config_state` preconditions before `codex-network-proxy` loads credentials or opens sockets.
- Regression: `network/proxy_static_config_platform_like_rust_test.go` adds `TestStaticProxyConfigValidatesNativePlatformPolicyLikeRust` covering a rejected relative socket path, an accepted absolute path, and the credential-broker MITM requirement.
- Verified with `go build ./network/... ./cmd/...`, `go test ./network -count=1`, and `go vet ./network ./cmd/codex-network-proxy`.

### Preserve Windows package identity for sandboxed descendants (Round 914)

- Upstream (`fd59b42a09df`, #46575) lets the caller's OS-assigned package identity decide whether sandboxed descendants keep desktop app context, drops the `codex-command-runner.exe` and staged-runner verification, and rejects a registered-core caller that has no identity.
- Go already implements this: `CurrentProcessHasPackageIdentity` queries `GetPackageFullName` for the current process, and both the regular and ConPTY launch paths derive `ProcThreadAttributeCountForLaunch` plus `PreserveDesktopAppContext` from it.
- The policy is now factored into `PackageIdentityDecision` so the registered-core rejection is testable, and `app_package_decision_like_rust_test.go` adds `TestPackageIdentityDecisionLikeRust` covering identity present (with and without registered core), an unpackaged caller, and the rejection branch.
- Verified with `go build ./...`, `GOOS=darwin go build ./sandbox/windowssandbox`, and `go test ./sandbox/windowssandbox/... -count=1`.

### Extend server version notices to prerelease and local clients (Round 915)

- Upstream (`a5290028a293`, #46673) extends server version notices to prerelease and local clients: stable clients compare against older releases including prereleases, prerelease clients order within their release line and report other lines as different, local clients (`0.0.0` or build metadata) report mismatches, malformed versions stay silent.
- Go already implements this in `tui/update_versions.go` (`ServerVersionNoticeKindFor`), `tui/status/remote_connection.go` ("older than"/"different from" plus dedup key), and `tui/tea/server_version_notice.go` (`<` / not-equal overview indicator with `/daemon` guidance, honouring `tui.show_server_version_notice`).
- Regression coverage exists: `tui/update_versions_test.go` (`TestStableClientsOnlyWarnForOlderReleasesLikeRust`, `TestPrereleaseClientsCompareWithinTheSameReleaseLineLikeRust`, `TestPrereleaseAndLocalClientsWarnForMismatchesLikeRust`, `TestUnknownOrMalformedVersionsDoNotProduceNoticesLikeRust`) and `tui/status/remote_connection_notice_test.go` (`TestServerVersionNoticeUsesClientReleasePolicyLikeRust`, `TestPendingServerVersionNoticeDedupAndServiceIdentity`).
- Updated the stale `PendingServerVersionNotice` doc comment to describe version differences rather than older stable releases.
- Verified with `go test ./tui -run Version` and `go test ./tui/status -run ServerVersion`.

### Cover the shared local child-process spawn contract (Round 916)

- Upstream (`d086e2752dad`, #46659) only relocates the local child-process implementation into `codex-utils-pty`: it exposes `Child` plus `spawn_child`, which sets `kill_on_drop(true)` and pipes stdin, stdout, and stderr, and the MCP stdio transport switches to it.
- Go has no separate pty crate, so `mcp` owns the equivalent contract: `startProcess` wires `StdinPipe`, `StdoutPipe`, and a `stdioOutputBuffer` for stderr before starting the child through `startMCPStdioProcess` (`mcp/stdio_process_unix.go` process group with SIGTERM then SIGKILL escalation, `mcp/stdio_process_windows.go` suspended spawn plus kill-on-job-close Job Object, `mcp/stdio_process_other.go` direct-child fallback). `closeLocked` terminates the child, reaps it, and releases containment handles.
- Added `mcp/stdio_child_spawn_like_rust_test.go` (`TestStdioChildSpawnPipesStreamsAndKillsOnCloseLikeRust`): it drives a real re-executed stdio helper, proves the piped stdout response and captured stderr marker, and proves that closing the transport kills the recorded child pid.
- Verified with `go test ./mcp -run TestStdioChildSpawnPipesStreamsAndKillsOnCloseLikeRust` and the full `go test ./mcp`.

### Raise the default terminal probe budget to 250 ms (Round 917)

- Upstream (`7c1e485c871d`, #46855) raised `terminal_probe::DEFAULT_TIMEOUT` from 100 ms to 250 ms; the same budget covers every terminal probe and startup probe group.
- Go now pins `DefaultTerminalProbeTimeout` (`tui/terminal_probe.go`) and `terminalColorProbeTimeout` (`tui/terminal_default_colors.go`) to 250 ms, and resolves an unset budget through `defaultTerminalColorProbeTimeout`, which both platform color probes use.
- The TUI startup probe in `tui/tea/model.go` now passes `codextui.DefaultTerminalProbeTimeout` instead of a hardcoded 100 ms, so the raised window reaches the real query path.
- Added `tui/terminal_probe_timeout_like_rust_test.go` (`TestTerminalProbeTimeoutMatchesRustDefault`, `TestTerminalColorProbeUsesDefaultBudgetWhenUnset`) and updated `TestTerminalColorProbeQueryMatchesRust` to expect 250 ms.
- Verified with `go build ./tui/...` and `go test ./tui -run TerminalProbe` plus `go test ./tui -run TerminalColor`.

### Select read-only permissions for temporary structured threads (Round 918)

- Upstream (`15312c326c9b`, #46844) found that managed permission profiles take precedence over the legacy sandbox override, so a managed `:workspace` default could start a temporary structured (recap) thread writable. It now sets `default_permissions` to the built-in `:read-only` profile when no custom profile is supplied, and rejects a start whose effective sandbox is not read-only.
- `app/recap.go` now resolves the custom profile once, inserts `default_permissions: ":read-only"` into the temporary thread config when no custom profile is used, keeps `permissions` for custom profiles only, and validates the `ThreadStartResponse` before running the structured turn: the sandbox policy must be read-only (an omitted sandbox defaults to read-only), or the active permission profile must match the requested custom profile.
- Added `app/recap_permissions_like_rust_test.go` against a fake app server that serves a managed `:workspace` default: `TestTemporaryStructuredThreadRequestsReadOnlyPermissionsLikeRust`, `TestTemporaryStructuredThreadRejectsWritableSandboxLikeRust`, `TestTemporaryStructuredThreadPreservesCustomProfileLikeRust`, `TestTemporaryStructuredThreadRejectsLostCustomProfileLikeRust`.
- Verified with `go test ./app -run TemporaryStructured` and `go test ./app -run Recap`.

### Keep agents overview selection adjacent when hiding tasks (Round 919)

- Upstream (`576110286845`, #45978) keeps the agents overview selection adjacent when the selected task is hidden (next visible task, else the previous one at the end of the list) and keeps the rename-cancellation behavior so an unsubmitted draft cannot transfer to a neighboring task.
- Go already chose the adjacent surviving task through `SelectionAfterRemoval` (`tui/agents_overview/overview.go`, Rust #50505), and the search filter is honored. The remaining gap was the rename path: `HideSelected` moved the selection while renaming and left `State.Renaming` and `State.Input` set on a now-hidden target.
- `HideSelected` now returns early while renaming: it hides the row, cancels `Renaming`/`RenameTarget`/`Input`/`InputCursor` when the rename target is the hidden row, and refits without selecting a successor.
- Added `tui/agents_overview/hide_selection_like_rust_test.go` (`TestHideSelectedKeepsSelectionAdjacentLikeRust`, `TestHideSelectedFollowsSearchFilterLikeRust`, `TestHideSelectedWhileRenamingCancelsDraftLikeRust`, `TestHideSelectedHidesEveryVisibleTaskLikeRust`).
- Verified with `go test ./tui/agents_overview`.

### Skip compaction tasks in managed daemon recovery snapshots (Round 920)

- Upstream (`4d2807023aae`, #45807) records regular, uncancelled turns in the managed daemon recovery snapshot and resumes them after a restart, while completed, cancelled, review, and compaction tasks never become resume candidates.
- Go already had the on-disk format (`daemonrecovery` `WriteSnapshot`/`ReadSnapshot` with the `codex-interrupted-v1:` metadata entry), the producer (`appserver/daemon_recovery_snapshot.go` `DaemonRecoverySnapshot` plus the daemon-scoped `DaemonRecoverySink`), and the restore/continue consumers.
- The remaining gap was the task-kind filter: only review turns were excluded, so a `thread/compact/start` task (originator `compact`, started in `handleThreadCompactStartRuntime`) was captured as an interrupted turn and would be continued automatically after a restart. Added `turnStartCompactionRuntime` in `appserver/turn_runtime.go` and made `daemonRecoveryInterruptedTurn` skip it next to review turns, mirroring Rust's `TaskKind::Regular` gate.
- Added `appserver/daemon_recovery_turns_like_rust_test.go` (`TestDaemonRecoverySnapshotTurnStatesLikeRust`) covering running (recorded), completed (consumed), cancelled (`cancelActiveRuntimeTurn`), and compacting (skipped) turns.
- Verified with `go test ./appserver -run DaemonRecovery` and `go build ./...`.

### Cover Mermaid edge reconstruction and semantic spans (Round 921)

- Upstream (`7c709f0ffd5c`, #45817) adds the bounded `codex-mermaid` renderer: `render` and `render_spans`, source/diagram/canvas/display-width limits, and errors without partial diagrams.
- Go already carries the same bounds (`mermaid/mermaid.go`: `maxSource`, `maxNodes`, `maxEdges`, `maxLabel`, `maxCells`; `ErrUnsupported`/`ErrLimit`/`ErrTooWide`) and both entry points, with per-family coverage.
- Missing were the semantic-span and exhaustive edge-reconstruction regressions. Added `mermaid/edge_reconstruction_like_rust_test.go`:
  - `TestReconstructEveryEdgeFromRenderedPathsLikeRust` renders all 512 directed three-node graphs in `TD`, `BT`, `LR`, and `RL`, transposes horizontal layouts, checks node order (`A,B,C` forward, `C,B,A` reverse), and reconstructs every edge from the glyphs alone, including self-loops and crossing lanes.
  - `TestSemanticSpansDistinguishLabelsFromEndpointGlyphsLikeRust` checks that a sequence message label and an endpoint glyph sharing a character keep `text` and `edge` roles and that box borders are `node` spans.
- Verified with `go test ./mermaid`.

### Nonfatal clock failures: clear the environment date like Rust (Round 922)

- Upstream `5bf132cd5273` (#45825) adds opt-in nonfatal clock reads. The Go tree
  already had the feature flag, the `CurrentTimeUnavailable` fragment, the
  per-turn/per-window deduplication, the generic clock and sleep tool failures,
  and the fork filter from the #46006 round; the missing behaviour was the
  environment date removal marker.
- `session/world_state.go` now stores the `environments` section snapshot.
  `appserver/environment_world_state.go` mirrors `EnvironmentsState::render_diff`:
  a value the model already saw and that is gone now renders as
  `<current_date status="unavailable" />` exactly once, a repeated comparison is
  a no-op, and a recovery renders the date normally again.
- `appserver/turn_runtime.go` renders the marker for the turn path and, matching
  Rust `render_full`, records the empty snapshot for the compaction window
  without a removal marker. The same renderer now covers Rust's
  `shell_version_removed` arm (#41232), previously omitted in Go.
- New tests in `appserver/environment_clock_removal_like_rust_test.go` cover the
  date removal/recovery sequence, the compaction window, the snapshot
  bookkeeping, and a sleep-tool clock failure that still completes its item and
  returns `failed to read current time` to the next inference.
- Verified with `go build ./...`, `go test ./appserver -run Environment|Clock|Compact`,
  and `go test ./session ./turn ./tool ./context ./compact`.

### Guardian sender evidence stays reviewer-only (Round 923)

- Upstream `e269f2164cbb` (#46179) gives Guardian reviewers the sender's recent
  user messages for a delegated task. The Go tree already implemented the whole
  surface: bounded snapshot in `retainedctx`, capture on accepted
  `codex_app`/`codex_tui`/`cloud_threads` deliveries, reviewer-only rendering,
  replay into retained context, and fork stripping.
- Added `appserver/retained_sender_test.go`
  `TestRuntimeRouterKeepsSenderSnapshotsOutOfTheWorkerPromptLikeRust`, the one
  Rust assertion that had no Go counterpart: the snapshot stays with the delivery
  and reaches the reviewer section, but never enters the worker's model-visible
  text.
- Upstream `77c1feb00ed6` (#46107) only boxes the app-server request-handler
  future to shrink queued-future stack size. Go runs request handlers on
  goroutines with growable stacks and keeps no such future queue, so the commit
  has no counterpart and is recorded as `not_applicable`.
- Verified with `go test ./appserver -run Sender|Retained|Guardian` and
  `go test ./state ./retainedctx`.

### Daemon socket isolation accepts private tmp mounts (Round 924)

- Upstream `1bd1bfa7ca4c` (#46125) found that systemd `PrivateTmp` style
  bind mounts leave hidden entries in `/proc/self/mountinfo`, so the Linux
  sandbox rejected socket layouts that actually isolate the daemon correctly.
- `sandbox/linuxsandbox/daemon_mount_identity_linux.go` now opens the rendezvous
  directory and reads its mount ID from the descriptor's `fdinfo` entry, with a
  `statx(AT_EMPTY_PATH, STATX_MNT_ID)` fallback for kernels that omit it, plus
  the `major:minor` mount device; both feed the mount check.
- `sandbox/linuxsandbox/daemon_mounts.go` selects the containing mount by that
  ID and walks its ancestry towards the namespace root, so a mount hidden by a
  newer overmount is rejected while the visible one is accepted. Aliases are
  only derived between mounts of the socket's own device, matching Rust. Private
  tmpfs/bind `/tmp` layouts and stacked mounts now pass, while covered mounts,
  exposed aliases, socket aliases and nested mounts still fail startup. With no
  mount ID the conservative rule stays: every candidate mount must agree on one
  backing location and no alias is assumed hidden.
- `sandbox/linuxsandbox/linux.go` passes the WSLg distro mask only when that
  mask is actually in effect, mirroring Rust `bwrap.rs:426`, so an unmasked
  `/mnt/wslg/distro` alias still rejects instead of being silently allowed.
- Ported the Rust `daemon_mounts_tests.rs` table in
  `sandbox/linuxsandbox/daemon_mounts_private_tmp_like_rust_test.go` (private
  tmpfs, stacked mounts, covered open mount, ancestor overmount, PrivateTmp
  bind with exposed aliases, masked WSLg alias, and the eight-case
  reject-only-compromising-layouts table run with and without a mount ID) and
  updated the existing cases in `sandbox/linuxsandbox/daemon_mounts_test.go`.
- Verified with `GOOS=linux go build ./sandbox/...` and
  `GOOS=linux go test -c ./sandbox/linuxsandbox`; the mount-validation tests
  also run on Windows through an untagged copy rewritten to POSIX `path`
  semantics. Residual: the `fdinfo`/`statx` acquisition itself is only
  compile-checked here because no Linux host or WSL is available.

### Sites bundled fallback uses the runtime compatibility guard (Round 925)

- Upstream `108e6a6dbeed` (#46108) deletes the persisted bundled-plugin
  exclusions, the Sites migration check and the initial migration wait, and
  replaces them with one runtime guard: while the remote global catalog is
  active, a cached remote Sites install suppresses `sites@openai-bundled` even
  when that remote entry is disabled, and a missing remote bundle keeps the
  bundled fallback.
- The removal half is already absent in Go: there is no exclusion state file,
  no Sites migration bookkeeping and no migration wait on the local
  plugin-config reload path.
- The guard half already exists as the runtime routing layer:
  `pluginEligibleForRuntimeLocked` and `routePluginDetails` feed
  `enabledPluginDetailsSnapshot`, which backs `EnabledCapabilities`,
  `EnabledHookSources`, `EnabledSkillRoots` and the MCP contributions. A cached
  `sites@openai-curated-remote` entry therefore suppresses the bundled plugin
  for every loaded surface, enabled or not, and only while the remote catalog
  is active.
- Added `plugin/sites_runtime_guard_like_rust_test.go`, the Go counterpart of
  Rust's expanded agent-turn Sites matrix: enabled remote supersedes bundled,
  disabled remote does not reactivate bundled, missing remote bundle preserves
  bundled, plus an inactive-remote-catalog case that keeps the bundled plugin.
  Each case is asserted over the skill-root and hook-source surfaces.
- Verified with `go build ./...` and `go test ./plugin`.
- Residual: `PluginService.Install` still rejects a plugin that is ineligible
  for the active auth route (the generic routing guard, not the deleted Sites
  exclusion guard), so installing `sites@openai-bundled` beside a cached remote
  install returns an error instead of a silent no-op install.

### Filesystem helpers launch from the root when the cwd disappears (Round 926)

- Upstream `841b5490b251` (#46112) found that deleting the selected working
  directory stopped filesystem helpers from launching even though the requests
  use absolute paths. The helper is now started from the filesystem root while
  the policy cwd keeps anchoring permission rules.
- `execserver/fs_helper.go` gained `fsHelperLaunchRoot`, which returns the root
  of the policy cwd's volume (`/` on POSIX, `C:\` or the UNC share root on
  Windows). `runSandboxedFSOperation` launches the piped helper there instead of
  in the policy directory, and the sandbox plan still receives the policy cwd,
  so relative permission entries and the Windows payload's `command_cwd` are
  resolved exactly as before.
- `sandbox/run.go` now carries an optional `CommandCWD` on
  `CommandRunRequest`/`CommandRunPlan`, resolved by `planCommandCWD`. The Linux
  wrapper passes it as `--command-cwd` while `--sandbox-policy-cwd` stays the
  policy directory, so bwrap no longer chdirs into a removed path and relative
  rules stay anchored to the policy cwd.
- Regression coverage: `sandbox/run_plan_command_cwd_like_rust_test.go` pins the
  default and explicit command-cwd resolution, and
  `execserver/fs_helper_launch_root_like_rust_test.go` checks the launch-root
  mapping plus an end-to-end read after the policy directory is deleted. The
  read fails with `The directory name is invalid` when `cmd.Dir` is the removed
  path, which is the bug this round fixes.
- Verified with `go build ./...`, `GOOS=linux go build ./sandbox/...
  ./execserver/...`, `go test ./sandbox` and
  `go test ./execserver -run "FS|FileSystem|Sandbox|FSOperation"`.
- Residual: Go keeps the legacy inline `cwd` on every filesystem RPC, resolving
  an omitted directory at executor ingress, instead of adding Rust's separate
  `policyContext` wire field; Windows relative deny globs stay anchored through
  the payload's explicit `command_cwd` rather than Rust's pre-binding pass.

### TUI daemon update handoffs report their observation (Round 927)

- Upstream `b0659c53865d` (#46126) adds daemon startup and update
  observations, records `codex.tui.start` once after the first connection
  attempt, reports TUI update handoffs as `handoff_requested`, and keeps daemon
  settings tags to enabled state and explicit presence.
- Go already carried most of the surface: `codex.daemon.start` for TUI
  auto-start, `codex.daemon.update` for `daemon update` with the CLI initiation
  source, the `CODEX_DAEMON_TELEMETRY_HANDOFF` marker (removed again when a
  long-lived daemon child is spawned), bounded daemon settings tags, and
  `codex.tui.start` with `daemon_selection_reason` and `daemon_auto_start`.
- The missing piece was the TUI update handoff observation.
  `recordTUIDaemonUpdateHandoff` now records one `codex.daemon.update` counter
  from the launch provider, with the launch-time settings tags plus
  `initiation_source=tui_handoff`, `update_target=this_cli|public_stable` and
  `outcome=handoff_requested`. It is emitted before the CLI relaunch, so a
  failed relaunch still reports a requested handoff, and the relaunched child
  stays silent through the existing marker.
- `interactiveTUITelemetry` now keeps the launch sink and its settings tags, so
  the handoff observation reuses the same consent-aware provider that recorded
  `codex.tui.start` instead of building a second one.
- Added `app/tui_daemon_handoff_telemetry_like_rust_test.go` covering both
  targets, the bounded settings tags, the absence of launch-only tags, silence
  when no provider exists, telemetry-before-relaunch ordering, and no
  observation for non-daemon update actions.
- Verified with `go build ./...` and
  `go test ./app -run "Telemetry|DaemonUpdate|DaemonStart|DaemonAutoStart|Worktree"`.
- Residual: Go records `codex.tui.start` when the TUI actually starts with the
  already-resolved mode, so a fatal pre-TUI startup error emits no launch
  observation; Rust's `Launch` guard records `app_server_mode=unconfirmed` in
  that case.

### Windows sandbox provisioning error chains (Round 928)

- Rust #46237 renders Windows sandbox provisioning responses and failure logs with the alternate anyhow chain (`{error:#}`) and prunes the legacy installation registry key.
- Go has no Windows sandbox service IPC (`response_error_message`, `MAX_RESPONSE_MESSAGE_BYTES`) and no legacy installation record, so those halves stay not applicable.
- The cause-loss gap was real: `sandbox/windowssandbox/setup_error.go:125` adds `SetupErrorChain`, which mirrors `{error:#}` and surfaces causes that `Error()` hides.
- `sandbox/windowssandbox/bin/setup_main/win/run_windows.go:80` logs and reports provisioning failures through it, and the provisioning `setupFailuref` sites now render `%s` chains (user provisioning, SID resolution, firewall, marker, locks).
- New `sandbox/windowssandbox/setup_error_chain_like_rust_test.go` covers nested chains, the hidden `SandboxAccountCredentialMismatch` cause, dedupe and nil.

### URI-native filesystem policy matching (Round 929)

- Rust #41001 makes filesystem policy matching URI-native; Go already compares policy paths through `PathUri.Overlaps` with fail-closed ambiguity (`sandbox/policy_transforms.go:458`).
- The new parity matrix mirrors the upstream cases: convention-aware descendant joins, encoded `StartsWith` segments, Windows case variants, opaque fail-closed overlaps and validated lexical depths.
- The matrix exposed a real gap: `utils/pathuri.go:372` accepted a fully qualified `C:\workspace\docs` as a descendant join, while Rust rejects drive-qualified and colon-bearing segments.
- `JoinDescendant` now rejects any Windows segment carrying `:` in addition to `\`-rooted and drive-relative inputs.
- Residual: protected metadata names still use host `filepath.Join` plus `PathWithin` (`sandbox/integrity/policy.go:300`), so a foreign executor convention is not applied there.

### Enterprise ID-JAG authorization_details rejection (Round 930)

- Rust #40722 adds the enterprise ID-JAG exchange; Go already ports the two-step exchange, destination policy, claim binding and credential redaction (`mcp/ema_exchange.go`, `mcp/ema_claims.go`, `mcp/ema_auth_policy.go`).
- The audit missed the rmcp profile rule that rejects a non-empty `authorization_details` list: Go decoded the three carriers into structs that silently dropped the field.
- `mcp/ema_claims.go:230` now rejects non-empty `authorization_details` in the signed ID-JAG claims and in the IdP response, and `mcp/ema_claims.go:297` does the same for the resource token response; an absent or empty list still validates.
- New `mcp/ema_authorization_details_like_rust_test.go` covers absent, empty and non-empty lists for all three carriers.

### Persistent-turn clock reminders (Round 931)

- Rust #40942 defaults the current-time reminder and the interruptible `clock.sleep` tool on for persistent-reasoning turns, while explicit settings and managed policy win.
- Go already synthesized the clock tools (`decideClockToolEnablement`), but never materialized the reminder config, so persistent turns got `clock.curr_time` without any `<current_time_reminder>` delivery.
- `appserver/turn_runtime.go:428` adds `resolvedClockReminderConfig`: an explicit `[features].current_time_reminder` wins, otherwise a persistent-effort turn gets the Rust default (interval 1, system clock, any-inference, `sleep_tool` on).
- `appserver/turn_runtime.go:401` now also refuses the default when a managed requirement disables the feature, matching Rust `features.enable(...).is_ok()`.
- Both reminder paths (`turn_runtime.go:10021` and `:10132`) use the resolved config; `appserver/current_time_reminder_persistent_like_rust_test.go` covers persistent, ordinary, explicit, explicitly disabled, managed-disabled, configured-effort and review turns plus delivery.

### Structured turn tool outputs (Round 932)

- Rust #41002 adds an optional `toolOutput` to `turn/start` whose `output` is a `FunctionCallOutputBody`: plain text or structured content items.
- Go already accepted `toolOutput` and validated name, input exclusivity and the text limit, but the body was a plain string, so structured outputs with inline images failed to decode.
- `turn/api.go:124` adds `TurnToolOutputBody` with string-or-items wire encoding; `turn/api.go:475` now validates the structured form (text-char budget over `input_text` items, remote image URLs rejected, `file_id` references allowed) and `turn/api.go:162` counts only text items.
- `appserver/turn_runtime.go:12850` forwards the structured body unchanged to the `function_call_output` item, so inline images reach the model request.
- `turn/tool_output_body_like_rust_test.go` and `appserver/turn_tool_output_structured_like_rust_test.go` cover both wire forms, round trip, remote image rejection, file references and oversized structured output.

### Confirmation policies for actor MCP tools (Round 933)

- Rust #41072 forwards Browser Use and Computer Use confirmation-policy Markdown in `openai/confirmation_policies` metadata for `node_repl`/`cua_repl` calls, empty when the issuing model has none, and omits other servers and Guardian sessions.
- Go already implements the whole path: `mcp/tool_executor.go:23` defines the metadata key, `:719` builds the verbatim object, `turn/tools.go:1197` maps the catalog documents, `model/catalog.go:2043` clears them on instruction overrides, and `appserver/runtime_router.go:16543` uses the turn's model snapshot.
- Coverage already existed for verbatim text, partial documents, empty and nil policies, Browser connector eligibility, other connectors, non-`node_repl` servers and Guardian suppression (`mcp/tool_executor_test.go`, `turn/tools_test.go`, `model/catalog_test.go`).
- New `appserver/guardian_confirmation_policies_like_rust_test.go` pins the Guardian turn marker (originator and `x-openai-subagent`) that suppresses the metadata.
- Guardian exclusion now uses `guardianReviewTurn` (`appserver/runtime_router.go:207`), which combines the turn marker with the recorded Guardian session source (`session.GuardianReviewSource` / `internal_guardian`), matching `is_basic_session_source`.
- Residual: approval waits reuse the executor built for the issuing step instead of re-reading a model snapshot.

### Session-only model selection (Round 934)

Session-only model and reasoning selection (#45831, complete).

- Final model rows, single-effort rows, the reasoning submenu, and the explicit Max/Ultra submenu now advertise the `s` secondary shortcut with the upstream footer `enter default/apply · s session · esc back`.
- The shortcut is suppressed for Reserve models and whenever any configured `list` action already claims `s`, matching Rust's keymap conflict guard.
- A secondary selection produces a `session_model` picker decision whose `ResolveModelSelection` plan updates the active thread settings and leaves `Persist` empty. The embedded TUI therefore performs no config write, while the remote path sends `thread/settings/update` without `config/batchWrite`.
- Plan mode changes only `PlanModeReasoningEffort` for non-Ultra choices; Ultra updates both the current and Plan efforts. The picker appends the upstream `for this session only` confirmation without entering the Plan-scope submenu.
- Added `tui/tea/session_model_selection_like_rust_test.go` and `app/session_model_selection_like_rust_test.go` covering direct, reasoning, advanced, conflict, Plan, Ultra, local, and remote behavior.
- Ultra choices reproduce Rust's `ultra_reasoning_concurrency_warning`: with 8 or more configured V2 concurrent threads, the session-only path appends the warning including the `max_threads - 1` subagent count. The limit is wired from config into both embedded and remote TUI options.
- Verified with `go build ./...`, `go test ./tui/...`, and the targeted app model-selection tests.

### Ledger reconciliation (Round 935)

- #41360 is implemented by the startup Codex home size histogram in `appserver/codex_home_metrics.go`; the background scan honors cancellation and the later #43790 session-directory scope plus #42043 compression tag.
- #41461 is covered by the model-catalog `send_user_message_async` description path and its `request_user_input_async` successor; explicit empty values are preserved.
- #40504 is superseded by #42667 and therefore `not_applicable`; Go has no plan-type Trusted Access URL routing.
- #42667 is implemented by `tui.DaybreakNotice`, the cyber refusal history cell, the tea notice selection, chatwidget runtime, and safety recourse. Targeted appserver, tool, turn, TUI, and history-cell tests pass.

Status after reconciliation: `2104 complete`, `410 not_applicable`,
`423 audit_pending`, `207 regression_pending`, `2 partial`,
`6 blocked_precondition`.

### Read-only summary history fallback (Round 936)

Read-only conversations now recover from turn summaries when history paging fails (#45980, complete).

- `remoteTUIReadThreadForViewing` keeps the full `thread/read` snapshot as the preferred read-only load. When it fails and the thread reports paginated history, it requests `thread/turns/list` with no cursor, `limit: 100`, `sortDirection: desc`, and `itemsView: summary`, then reverses the page into chronological order.
- Legacy threads still surface the original history error and never request turn summaries.
- The recovered snapshot carries Rust's notice: `Showing up to 100 recent prompts and final replies. Intermediate messages and tool activity are unavailable.` `SessionResumeResponse` and `AgentThreadSwitchResponse` gained `HistoryNotice`, which the tea resume and agent-switch paths append to the transcript.
- When both history loads fail, the command center returns `Couldn't load this conversation. Please try again.` and keeps the current conversation; the session picker still reports the underlying error.
- Added `TestInteractiveRemoteReadOnlySummaryHistoryFallbackLikeRust` (limit/view/order/notice on both attach paths), `TestInteractiveRemoteReadOnlyHistoryFailureLikeRust` (legacy no-fallback and both-fail preservation), and `TestReadOnlySummaryHistoryNoticeLikeRust` (tea transcript notice, including the quiet complete-snapshot case).
- Verified with `go build ./...`, `go test ./app -run 'Remote|SessionPicker|ReadOnly|Agent' -count=1`, `go test ./tui/... -count=1`, and `go vet ./app ./tui/tea`.

### Executor path URIs in profile workspace roots (Round 937)

Profile workspace roots keep executor path conventions and spelling (#45863, complete).

- Go already resolves configured profile roots through the owning `ConfigPathContext` and carries the resulting convention-aware roots into materialization, status summaries, and exec requests; permission profiles travel as JSON and thread settings persist only runtime roots, so profile roots are never projected onto the controller host.
- `permissionProfileBuilder.effectiveWorkspaceRootsFor` deduplicated runtime and profile roots by `PathURI.IdentityKey`, which folded a case-only Windows or UNC spelling change. It now keys on `PathURI.String`, preserving spelling while still normalizing separators.
- The legacy turn-context `workspace_roots` field is already omitted by rollout recording, so unrepresentable profile roots cannot drop the compiled permission profile.
- `execserver` converts roots only at the native boundary and rejects non-URI or foreign-convention roots (`TestFilesystemWireSandboxPathsRequirePathURIsLikeRust`).
- Added `TestEffectiveWorkspaceRootsPreserveWindowsSpellingLikeRust` for `C:\Work\Profile` versus `C:\work\profile` and the UNC equivalents.
- Verified with `go test ./config -count=1`, targeted `appserver` workspace-root tests, `go test ./execserver -run 'TestFilesystemWireSandboxPathsRequirePathURIsLikeRust|TestFilesystemWirePathsRequirePathURIsLikeRust' -count=1`, and `go test ./sandbox/... -count=1`.

### Keybinding validation errors (Round 938)

Invalid `tui.keymap` bindings keep their specific validation reason (#49910, complete).

- Go's `parseKeybindingsConfigValue` already branches on the value type (single string, `[]string`, `[]any`), so `NormalizeKeybindingSpec` errors reach the caller intact; `KeymapConfigFromConfigValues` adds the `tui.keymap.<context>.<action>` key. There is no untagged-enum fallback that could replace the reason with a generic failure.
- Added `TestKeymapConfigPreservesKeybindingValidationErrorsLikeRust` for unknown keys in single and list form, an invalid chord stroke, and an overly long chord, pinning both the reason and `tui.keymap.editor.insert_newline`.
- Go's `codex mcp list` uses a standalone MCP store and does not construct the TUI keymap, so invalid bindings surface when the TUI loads its keymap instead of on every CLI command; the error-preservation behavior is otherwise aligned.
- Verified with `go test ./tui -count=1` and `go vet ./tui`.

### Multiline ANSI warning payloads (Round 939)

Multiline ANSI input no longer risks payload-heavy warning logs (#49416, complete).

- Rust replaced the rendered line contents in `ansi_escape_line`'s multiline warning with `input_bytes` and `line_count`.
- Go has no `ansi_escape_line` warning path: hook rendering parses multiline ANSI through `historycell.ParseANSISpans` and `StyledLinesFromSpans`, which emit no log entries, so the leaking vector does not exist.
- Existing `TestHookSystemMessageRendersANSIStyles` covers multiline styled warnings without raw escape leakage, and `TestHookSystemMessagePreservesBlankAndCombiningLines` covers trailing newlines and blank lines.
- Verified with the targeted `tui/history_cell` tests; no code change was required.

### Usage and credit links (Round 940)

Usage and credit links now target `https://chatgpt.com/settings/usage` (#49874, complete).

- `model/usage_limit.go` uses the new URL in the Plus and Pro usage-limit messages, and the model tests assert it.
- `app/backend_banners.go` sets `backendBannerUsageURL` to the new URL; the credits purchase action still appends `?credits_modal=true`.
- `tui/state.go` renders the new URL in the status card usage row, with the status and auth tests updated.
- No Go code still references the old `chatgpt.com/codex/settings/usage` path.
- Verified with `go test ./model -count=1`, `go test ./app -run 'Banner|Backend' -count=1`, and `go test ./tui -run 'Status|Usage|Auth' -count=1`.


### Interrupt lifecycle callbacks (Round 941)

Interrupt callbacks now finish before interrupted terminal events (#49475, complete).

- Go's `Interrupt` hooks are the local analogue of Rust's `on_turn_abort` lifecycle contributor. The explicit `turn/interrupt` path already ran them synchronously before scheduling `turn/completed`.
- The Guardian conditional interruption path, the review-interruption path, and router shutdown cancelled turns without running the interrupt hook. They now call `runInterruptHook` before persisting the abort marker and publishing the interrupted lifecycle.
- Added `TestInterruptCallbackCompletesBeforeTurnAbortedLikeRust`, which holds the Interrupt hook open and verifies that explicit interrupt, conditional Guardian interrupt, and shutdown emit no `turn/completed` until the hook completes. It also asserts the hook runs exactly once for each path.
- Verified with `go build ./...` and the targeted appserver lifecycle and Guardian tests. The full `go test ./appserver -count=1` run completed without abort-lifecycle failures but still hit two unrelated 2-second timeout tests (`TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust` and `TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust`), both of which reproduce in isolation. `go vet ./appserver` still reports the pre-existing context-cancel warning in `appserver/thread_sample.go:220`, also unrelated.

### Authoritative resume history (Round 942)

A cold resume now restores history committed by the previous writer (#49599, complete).

- `handleThreadResume` reads the snapshot before it can acquire the single-writer lock, so a prior process could append history in that window and have it dropped when the resume restored the stale snapshot.
- `LiveThreadRetention.ReadRecord` now reads through the writer acquired by the resume, and `refreshedResumeRecord` re-reads the stored record after acquisition before settings, turn snapshots, and the response are built. A missing store record keeps the rollout-path fallback, and explicit `path` resumes keep their prior semantics.
- Added `TestResumeRefreshesStaleHistoryUnderWriterLikeRust` (late append survives, pre-acquisition snapshot is not mutated) and `TestResumeRefreshKeepsRolloutFallbackLikeRust` (missing store record keeps the rollout snapshot).
- Verified with `go test ./appserver -run 'TestResume|TestThreadResume' -count=1`. The full `go test ./appserver -count=1` run still hits three unrelated 2-second timeout tests that fail identically on the unmodified tree: `TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust`, `TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust`, and `TestRuntimeRouterFileChangeAnalyticsIncludesUserReviewSummaryLikeRust`. `go vet ./appserver` still reports the pre-existing context-cancel warning in `appserver/thread_sample.go:220`.

### Command-center session retention (Round 943)

The agent command center now keeps discovered sessions for the TUI's lifetime (#42104, complete).

- Go already seeded up to 20 recent root sessions beside the loaded list, retried with `updated_at` when a server rejects `recency_at`, kept loaded rows when the recent listing failed, and merged the shared pinned section.
- The remaining gap was membership retention: `ApplyRefresh` rebuilt the dashboard only from the latest listing, so a session that left the loaded/recent window disappeared even though this TUI had discovered it. Rust retains `agents_overview.threads` until an archive/delete removes it.
- `tui/tea/agents_overview.go` now merges each refresh into `agentsOverviewKnownRows`/`agentsOverviewKnownOrder`: fresh rows keep server order and update metadata, previously discovered rows follow as `notLoaded`/Finished, and archived/deleted tombstones are never resurrected.
- Added `TestAgentsOverviewRetainsDiscoveredSessionsLikeRust` (retention after the recent window shrinks, metadata refresh, tombstone removal).
- Verified with `go test ./tui/tea ./tui/agents_overview -count=1`.

### Retained shell-snapshot prewarm (Round 944)

The speculative shell-snapshot capture is now retained and cancellable (#42137, partial).

- Go's active local path uses the v1 session snapshot. `tool.SnapshotBuilder.PrewarmSnapshot` now starts the speculative capture once per session key; later turns reuse it without another capture or telemetry sample, and a failed prewarm stays retryable by real commands.
- `SnapshotBuilder.Close` marks the builder closed and cancels an in-flight capture before removing files, so unloading a thread no longer waits for the capture timeout. `appserver/shell_snapshot.go` skips capture telemetry for a shutdown-cancelled attempt.
- Rust's v2 prewarm is not ported for local execution: Go has no in-process v2 snapshot cache, and v2 requests go only to remote exec-servers, which capture lazily. Rust also captures each turn environment under its permission/sandbox and skips disallowed login shells or network policies; Go's v1 session capture stays unsandboxed.
- Added `TestSnapshotBuilderPrewarmRunsOnceLikeRust`, `TestSnapshotBuilderCloseCancelsPrewarmLikeRust`, and `TestShellSnapshotPrewarmRunsOncePerSessionLikeRust`.
- Verified with `go test ./tool ./appserver -run 'Snapshot|ShellSnapshot' -count=1`, the full `./tool` package, and `go test -race ./tool -run 'TestSnapshotBuilder' -count=1`.

### Rollout I/O scheduling audits (Round 945)

Three remaining L4c entries are Rust runtime-scheduling details with no Go counterpart (#49444, #49693, #49708; not applicable).

- `#49444` replaces a byte-wise reverse newline search with `memchr::memrchr`; it changes no parsing or output. Go has no `reverse_jsonl_scanner`: session-index lookups read the index with `os.ReadFile`, and projection trailing newlines use `bytes.LastIndexByte`.
- `#49693` collapses rollout open/metadata/seek/read/projection into one `spawn_blocking` call. Go's `rollout.ReadProjectionStepsCancellable` already performs those steps synchronously in one function on the calling goroutine, with cancellation covered by `projection_cancellation_test.go` and `projection_54639_test.go`.
- `#49708` moves session-index file I/O to blocking workers and keeps an owned Tokio mutex guard through cancellation. Go runs the same synchronous I/O under one process mutex plus a cross-process flock, so ordering cannot be released mid-write; covered by `session_index_lock_test.go` and `session_index_test.go`.
- No code change was required. Verified with `go test ./rollout -run 'Projection|SessionIndex' -count=1`.

### MCP package names and identity audits (Round 946)

Four early MCP/remote-executor ledger entries were re-audited; one real MCP package-name gap was fixed.

- `#41700` (complete): `app/mcp.go` now accepts `:`, `@`, `/` and `.` in `codex mcp add/remove` names, so `npm:@modelcontextprotocol/server-sequential.thinking` round-trips through `config.toml` and `list/get/remove`. `mcp/oauth.go` also keeps repeated `local:` prefixes isolated when matching legacy file credentials on load and delete, so `local:foo` cannot satisfy or be deleted as `local:local:foo`. Existing callable-name sanitization already maps the package name to `mcp__npm__scope_package_name`. Tests: `TestMCPAddRemovePackageStyleServerNameLikeRust`, `TestMCPOAuthFileCredentialsKeepRepeatedLocalPrefixesIsolatedLikeRust`.
- `#40739` (complete): the enterprise IdP identity-resolution surface is present through the later `#55685` port — account ID-token binding, public-client authentication advertisement, resource-origin/path validation, reauthentication classification, per-home `ema-idp:` store keys and the credential-lock reread.
- `#42123` (complete): superseded by the later `#50695` local-link label policy in `tui/markdown/local_links.go`; descriptive labels render as `label (target)` and path-like/empty labels keep the canonical destination behavior.
- `#40710` (not applicable): Go has no lazy persistent remote exec-server client or `Environment::refresh_connection`. `EnvironmentManager` opens a fresh client per info/status/tool path and closes it, so an executor replacement is observed by the next lookup and there is no stale session to retire or resume.
- Verified with `go test ./app -run 'TestMCPAddRemovePackageStyleServerNameLikeRust|TestMCPAddListGetRemove' -count=1`, `go test ./mcp -run 'TestMCPOAuthFileCredentialsKeepRepeatedLocalPrefixesIsolatedLikeRust|TestComputeMCPOAuthStoreKeyLikeRust' -count=1`, and `go test ./tui/markdown -run 'LocalFileLink' -count=1`.

### Symlinked session roots for paginated forks (Round 947)

Paginated `thread/fork` lineage references are now validated against the canonical managed roots (#42135, complete).

- Rust's `PlainForReference` lineage resolution canonicalizes each referenced rollout and accepts it only when it stays under the canonical `codex_home/sessions` or `codex_home/archived_sessions` root, so a resumed rollout beneath a symlinked managed root can fork while a nested link that escapes the root is rejected.
- Go previously had no managed-root check in `materializePaginatedLineageForReference`, so a nested link could escape Codex home. `state.PreparePaginatedFork` now takes the Codex home (the router passes `session.Store.Root()`), and `validateManagedRolloutReference` applies the same canonical containment rule with the InvalidRequest message `rollout path ... must be in Codex home directory`.
- `state.canonicalizePath` resolves Windows junctions the way Rust's `std::fs::canonicalize` does; `filepath.EvalSymlinks` does not follow them, which would leave the escape check ineffective there.
- Tests: `TestPreparePaginatedForkAcceptsRolloutUnderSymlinkedSessionsRoot`, `TestPreparePaginatedForkRejectsReferenceEscapingSymlinkedSessionsRoot`, and `TestThreadForkAcceptsRolloutUnderSymlinkedSessionsRootLikeRust`. They use directory symlinks where permitted and fall back to Windows junctions, so all three execute on this host.
- Verified with `go build ./...`, `go test ./state -count=1`, and `go test ./appserver -run 'Fork|History|Resume' -count=1`.

### Shared compression and voice/tooling audits (Round 948)

Eight ledger entries were re-audited; shared-lineage compression is covered by the later design and the remaining entries are Rust build/test infrastructure.

- `#41357` (complete): the Rust baseline removed `RolloutCompressionMode`, and `local_thread_store_shared_compression` is `Stage::Removed`; compression now includes shared lineages, matching `rollout/compression.go`. Go reads compressed shared rollouts through `FirstSessionMeta`, `ExistingRolloutPath`, `ReadRolloutPrefix`, `MaterializeRolloutForReference`, and `RolloutByteLength`, covered by `read_rollout_prefix_like_rust_test.go` and the fork-lineage tests.
- `#41890` (complete): `third_party/voice/prepare_opus.py` is Go's codec build recipe — pinned Ninja, CMake build into a per-platform prefix, logged commands, and a `built.json` receipt; `voicehost/opus.go` loads the result through purego.
- `#41884`, `#41894`, `#42208`, `#42209` (not applicable): these add Rust's Bazel GStreamer/libffi source pinning, Cygwin/MSVC libffi build fixes, and ELF/PE GStreamer runtime preparers. The Go helper ships and loads only a packaged `libopus` (`voicehost/opus.go`, `voicehost/runtime_package.go`), so there is no GStreamer closure or libffi toolchain to prepare.
- `#41915`, `#41925` (not applicable): Cargo/Bazel schema-generator relocation and a Rust formatter test; no Go product behavior.
- Verified with `go test ./rollout -run 'ReadRolloutPrefix|Compression' -count=1`, `go test ./state -run 'PreparePaginatedFork' -count=1`, and `go test ./parity -run 'TestAlignment' -count=1`.

### Rust build and test infrastructure (Round 949)

Fifteen ledger entries are Rust-only build, Bazel, Cargo, or test-layout changes with no Go counterpart (not applicable).

- `#41226`, `#42069`, `#42161`, `#42316` are test moves/removals and Rust module refactors (Guardian tests, tool JSON Schema modules, exec-server startup futures) that change no public API or observable behavior. Go has its own file layout and test suite.
- `#43083`, `#43109`, `#43111`, `#43114`, `#43117`, `#43125`, `#43126`, `#43144` add Bazel targets, toolchains, and link steps for Rust's GStreamer-based voice runtime. The Go repository has no Bazel workspace; `third_party/voice/prepare_opus.py` handles the only native asset Go needs (libopus) and `voicehost/opus.go` loads it through purego.
- `#43533`, `#46993` are Bazel musl jemalloc flags and Bazel lockfile diagnostics. Go does not link jemalloc and has no Bazel build path.
- `#43325` sorts keys in the Rust Cargo/Bazel schema generator; Go does not run that generator and `parity/config_schema_surface_test.go` compares schema properties rather than key order.
- No code change was required. Verified with `go test ./parity -run 'TestAlignment' -count=1`.

### Release and dependency audits (Round 950)

Five Rust release/dependency entries have no Go counterpart (not applicable).

- `#42288` patches rules_rs to fetch pinned zlib packages from Ubuntu snapshots; Go has no Bazel workspace.
- `#42383` and `#49096` bump rmcp and h2 in Cargo/Bazel lockfiles; Go implements MCP and HTTP with its own packages.
- `#43604` removes `base_instructions` from Rust's bundled `models.json` and filters it in the Rust release workflow; Go has no bundled catalog asset and reads the runtime catalog payload.
- `#44062` fixes Rust's Bazel voice build and release packaging; Go's `release.yml` uses `third_party/voice/prepare_opus.py` instead of Bazel.
- No code change was required. Verified with `go test ./parity -run 'TestAlignment' -count=1`.

### Distinct invalid_prompt classification (Round 951)

- Rust #47353 adds a distinct `InvalidPrompt` API/core error and keeps it across HTTP 400, wrapped WebSocket failures and SSE `response.failed`; the classification is terminal and app-server v2 maps it to `other` (#47353, complete).
- `codexapi/error.go:21` adds `ErrorInvalidPrompt` (`invalidPrompt`), whose display is the provider message, matching `CodexErrorDetails::InvalidPrompt`; response-debug telemetry reuses the `invalid request` label.
- `model/responses_agent.go:4194` recognizes the HTTP 400 code and `:1647` recognizes the wrapped WebSocket event, both preserving the server message and using `Invalid request.` only when it is missing or blank; typed provider codes take precedence over the context-window message heuristic.
- `model/responses_stream.go:2799` recognizes SSE `response.failed`; a missing message gets `Invalid request.`, while a present empty or blank message is preserved verbatim. `:571` classifies the error as non-retryable.
- `appserver/turn_runtime.go:4797` serializes the turn error as app-server v2 `other`, keeps analytics `codexErrorKind` as `invalid_prompt`, and Guardian retry fails closed.
- `rollout/codex_error_info.go` recognizes the saved `invalid_prompt` and `invalidPrompt` classifications instead of rewriting them to `other`.
- Tests cover typed HTTP/SSE/wrapped-WebSocket mapping and fallback boundaries, typed-code precedence, single-request HTTP/SSE behavior, real WebSocket termination, saved-rollout classification, analytics/protocol conversion, response-debug labels and Guardian retry.
- Verified with `go build ./...`, `go test ./codexapi ./model ./rollout -count=1`, and focused appserver analytics/Guardian tests.

### Recap tail spacing in the owned transcript (Round 952)

- Rust #47355 reserves a blank row after a trailing conversation recap without adding it to selectable content, and omits it when a completed or live message follows (#47355, complete).
- `tui/history_cell/recap.go:28` introduces the `RecapCell` marker, `tui/state.go:59` records it on transcript messages, and `tui/tea/model.go:9176` preserves it through `historyMessageFromCell`.
- `tui/tea/model.go:10589` adds the layout-only tail row only for the owned transcript with a viewport taller than one row; inline/raw output, active streaming, running turns, and a following completed message keep the normal separator. `:10605` skips transcript-only reasoning and retained startup tips when identifying the visible tail.
- `tui/tea/recap_tail_spacing_like_rust_test.go` covers the trailing recap, completed and live follow-ups, inline mode, and a single-row viewport; existing recap command tests continue to cover generated recaps.
- Verified with `go build ./...`, `go test ./tui/tea ./tui ./tui/history_cell -count=1`.

### Status-aware copyable highlighting in `codex doctor` (Round 953)

- Rust #47358 colors recognized paths and URLs red for failed checks and cyan otherwise, and widens Windows path recognition to drive-absolute, UNC, Win32, DOS device and NT forms (#47358, complete).
- `doctor/color.go:192` ports `looks_copyable`: URLs, `~/`, `./`, `../` and `/` paths plus, on Windows only, `C:\logs`/`D:/goals`, `\\server\share\logs`, `\\?\C:\logs`, `\\?\UNC\...`, `\\.\pipe\codex` and `\??\C:\logs`; `\DosDevices\C:\logs` and `\rooted` stay plain.
- `doctor/color.go:53` (`highlightActions`) and `:67` (`styleDetailText`) alternate backticked code spans with plain tokens: code spans keep their spaces and are colored as one unit, plain tokens strip and reattach trailing `,.:;)` punctuation and whitespace, failed checks use red 196 for copyable items while `--flags` and non-failing copyable items use cyan 117. Detail values additionally dim falsy tokens (240), color `ok` green (10) and byte units.
- `doctor/render.go:136` styles row descriptions, `:363`/`:367` detail values, `:185` PATH entries, `:307` database details and `:171` remedies.
- `doctor/doctor.go:195` derives `Options.Color` from a terminal stdout with color support; `--no-color`, `NO_COLOR`, `TERM=dumb`, JSON mode and non-file writers keep the previous byte-identical plain output, and the terminal diagnostic summary shares the same predicate.
- Tests: `TestDoctorCopyableItemsUseCheckStatusLikeRust`, `TestDoctorCopyableHighlightLeavesPlainTextWhenColorDisabledLikeRust`, `TestDoctorCopyableHighlightPreservesPunctuationAndWhitespaceLikeRust`, `TestDoctorLooksCopyableWindowsFormsLikeRust`, `TestDoctorColorEnabledMatchesTerminalSignalsLikeRust`.
- Verified with `go build ./...`, `go test ./doctor ./app -count=1`, `go test ./parity -run 'TestAlignment|TestRustErrorCodeSurfaceAgainstGo' -count=1`.
- Residual: Go's doctor rows still use plain `OK`/`!!`/`XX` markers and unstyled group titles and labels; that structural difference predates this commit, so only copyable-item coloring is aligned here.

### Logon-session-scoped Windows sandbox default DACL (Round 954)

- Rust #47361 restricts the sandbox runner token's default DACL to the runner's logon SID and adds an `OWNER RIGHTS` entry with `READ_CONTROL`, so shared capabilities and account ownership cannot reach another logon's processes, threads or IPC objects (#47361, complete).
- `sandbox/windowssandbox/token_windows.go:340` builds the DACL from exactly two ACEs: the token logon SID with `GENERIC_ALL`, and OWNER RIGHTS (`S-1-3-4` via `windows.WinCreatorOwnerRightsSid`) with `READ_CONTROL`; Everyone and capability SIDs stay in the WRITE_RESTRICTED restricting set but no longer appear in the default DACL, and the owner entry suppresses the shared account's implicit `WRITE_DAC`.
- `sandbox/windowssandbox/token_windows.go:129` applies the DACL to every readonly and workspace-write runner token; the existing deferred cleanup still closes the new handle when the DACL or `SeChangeNotifyPrivilege` setup fails.
- `sandbox/windowssandbox/token_default_dacl_like_rust_test.go:24` creates a workspace-write token with capability `S-1-5-21-10-20-30-40`, reads `TokenDefaultDacl`, and asserts exactly logon SID = `GENERIC_ALL` and `S-1-3-4` = `READ_CONTROL` with no Everyone or capability ACE, skipping when the host rejects WRITE_RESTRICTED tokens.
- Verified on Windows with `go build ./...` and `go test ./sandbox/... -count=1`.
- Residual: the test validates DACL contents instead of replaying Rust's `AccessCheck` simulation for a second logon, because `x/sys/windows` exposes no `AccessCheck` binding.

### Agent metadata restore read overlap (Round 955, not applicable)

- Rust #47369 overlaps up to eight per-descendant `read_stored_thread` calls while restoring V2 agent identities (#47369, not applicable).
- Go has no per-descendant read to overlap: `RuntimeRouter.runtimeAgentRegistry` (`appserver/agent_controller.go:1309`) takes one `Store.AllRecords` snapshot (`session/store.go:1481`) and registers each descendant's stored agent path, nickname and role in record order, so path and nickname assignment is already graph-ordered and no thread is read twice.
- No other Go path loops over descendants issuing storage reads when a V2 root is resumed.
- Residual difference outside this commit: `Store.loadAllLocked` (`session/store.go:1850`) reads record files serially and drops the whole snapshot when one record is unreadable, so a single corrupt descendant prevents every restore instead of Rust's warn-and-continue; that is thread-store error granularity, not read scheduling.
- Verified with `go build ./...` and `go test ./appserver -run 'Agent|Registry' -count=1`.

### Realtime V3 reasoning status relay (Round 956)

- Rust #47377 adds opt-in `backend_reasoning_status` (default false) that relays public reasoning summaries as `[STATUS]` commentary context for active server-managed realtime V3 delegations (#47377, complete).
- `realtime.StartParams` already carried `backendReasoningStatus` (omitted when false) and `realtime/wire.go:358` only writes `delegation.context.append` for an active, non-client-managed V3 delegation, bounded to 256 bytes at a character boundary.
- `appserver/turn_runtime.go:680` now stores public summary deltas per summary part index (`recordReasoningSummaryDelta`), and `:656` (`takeReasoningSummary`) returns the last non-empty trimmed part of the completed reasoning item instead of the concatenation of every part; raw reasoning deltas remain unrecorded.
- Tests: `TestResponsesStreamReasoningSummaryUsesLastNonEmptyPartLikeRust`, `TestResponsesStreamReasoningSummaryPartsAccumulateIndependentlyLikeRust`, `TestResponsesStreamReasoningSummaryIsConsumedOnce`, `TestV3BackendReasoningStatusAppendsQuietCommentary` (also asserts no `response.create`), `TestV3BackendReasoningStatusDisabledSendsNothingLikeRust`, `TestStartParamsBackendReasoningStatusDefaultsFalseAndRoundTrips`.
- Verified with `go build ./...`, `go test ./appserver -run 'ReasoningSummary|ReasoningStatus' -count=1` and `go test ./realtime -count=1`.

### Bundled model catalog refresh (Round 957)

- Rust #47397 refreshes `models-manager/models.json` and its prompt snapshots: the GPT-6, GPT-5.6, GPT-5.5 and auto-review entries move to `shell_command`, `gpt-6-astra` accepts reasoning-effort updates without experimental context, plan availability gains `ent26`/`promax`, and Astra/auto-review guidance text changes (#47397, complete).
- `model/catalog.go:1407` adds `bundledShellCommandSlugs` and `:1389` stamps `shell_command` onto the offline fallback entries for `gpt-6-astra`, `gpt-6-sol`, `gpt-6-luna`, `gpt-5.6-sol/terra/luna`, `gpt-5.5` and `codex-auto-review`; the fallback already carried astra's `supports_reasoning_effort_updates=true` and `supports_experimental_context=false`.
- `model/catalog_test.go:251` (`TestFallbackBundledCatalogUsesShellCommandLikeRust`) pins the shell type for all eight slugs plus the astra and serde-default flags.
- Not carried: `available_in_plans` (Go's `ModelInfo` has no plan-type field; access comes from the runtime catalog payload) and the catalog-embedded `model_messages`/collaboration-mode text with its Rust scenario snapshots. At the `7ac60816` baseline the canonical `collaboration-mode-templates/templates/default.md` is unchanged, and `TestRustCollaborationModeTemplatesMatchGo` keeps Go pinned to it.
- Verified with `go build ./...`, `go test ./model -count=1` and `go test ./parity -run 'CollaborationMode|TestAlignment' -count=1`.

### Shared embedded network policy across CLI and TUI (Round 958)

- Rust #47411 makes the embedded network policy one host-owned object: startup activates the effective and local controllers once and hands the same pair to exec, the TUI, the MCP runtime and the app-server runtime, so reloads and load failures can no longer desynchronise them (#47411, complete).
- `config/embedded_network_policy.go:33` adds `EmbeddedNetworkPolicy` with `LoadLocal`, `Activate`, `BindConfig`, `Invalidate` and `Unavailable`; `config/config.go:43` adds `ApplicationNetworkPolicyOwner`, and `config/network_policy.go:81` (`BindApplicationNetworkPolicy`) reuses the host owner instead of building a private controller on each reload.
- `exec/application_network.go` and `app/application_network.go:37` (`publishHostApplicationNetworkPolicy`) publish through that owner, `exec.Runner.NetworkPolicy` is inherited by subagents, and the invalidate path revokes outstanding operations when a config load fails; `appserver/application_network.go:51` reuses the embedder's effective and local controllers rather than creating private ones.
- `ForkNetwork` is not applicable: Go has no fork/exec hand-off of the network policy.
- Tests: `config/embedded_network_policy_test.go`, `app/application_network_test.go`, `exec/application_network_test.go`, `appserver/application_network_test.go`.
- Verified with `go build ./...`; `go test ./config ./appserver ./exec -run 'EmbeddedNetworkPolicy|HostPolicy|LivePolicy|ApplicationNetworkPolicy' -count=1`; `go test ./app -run 'ApplicationNetworkPolicy|LivePolicy|MCPCLIStore|ChatGPTLoginOAuthOptions|InteractiveAuthOnboarding' -count=1`; and `go test ./otelinit ./network -count=1`.

### Per-backend gateway OAuth decryption cache (Round 959)

- Rust #47413 gives `LocalSecretsBackend` one bounded cache per backend for `GatewayOAuth` snapshots, reuses decrypted secrets only while the file path, ciphertext hash and keyring passphrase hash all match, invalidates the entry on write, and hides the cache from `Debug` (#47413, complete).
- Go already ships this behaviour as part of the later generic cache (`safety/secrets.go:150`, `:262`): `LocalBackend.gatewayCache` is a `*localFileCache` so copies of one backend share exactly one snapshot, while `LocalNamespaceMCPOAuth` keeps the process-wide `mcpOAuthSecretsCache` as in Rust's `MCP_OAUTH_CACHE`.
- `safety/secrets.go:311` still hashes the ciphertext and the keyring passphrase before reusing a snapshot, and `:371` drops the entry when a save rewrote the same path, so an independent manager or an external edit is never served stale plaintext.
- Rust's hand-written `Debug` exists only because the derive would print the cache; `LocalBackend` has no exported fields and no `String`/`GoString`/`LogValue`, so cached plaintext cannot leak through formatting.
- Tests: `TestLocalGatewayCacheIsScopedToBackendLikeRust`, `TestLocalSecretCacheReusesPlaintextLikeRust`, `TestLocalSecretCacheDetectsExternalChangesLikeRust`, `TestLocalSecretCacheStillChecksKeyringLikeRust`.
- Verified with `go test ./safety -run 'TestLocalSecretCache|TestLocalGatewayCache' -count=1`.

### Shift-click transcript selection extension (Round 960)

- Rust #47414 lets a Shift-click extend an existing transcript selection from its original anchor and unit, keeps the gesture open for continued dragging and repeated extension in either direction, and still starts a fresh selection on an ordinary click (#47414, complete).
- `tui/chatwidget/transcript_overlay.go` now counts presses within 400 ms at the same cell (cycling 1/2/3 like Rust `text_selection::click_count`), so a double-click marks the word under the pointer and a triple-click marks the row; repeated clicks past a row end are ignored, matching Rust's `position_at(column) == position_at(u16::MAX)` guard.
- `handleSelectMouse` extends instead of re-anchoring when Shift is held over a non-empty range, and `extendMouseSelection` recomputes the unit range at the pointer so backwards extension pins the original unit's end while forward extension pins its start.
- `tui/chatwidget/transcript_select.go` adds `SelectUnitWord`, `selectUnitForClicks`, `unitRangeAt`, `wordSpanAtColumn` and `selectMode.originStart/originEnd`; word spans split on the same alphanumeric boundaries as the keyboard word motions, and line units cover the whole logical row.
- Tests: `TestTranscriptShiftClickExtendsWordSelectionLikeRust` (both directions, repeated extension, ordinary-click reset) and `TestTranscriptShiftClickPreservesUnitsAndDragLikeRust` (character and row units with continued dragging).
- Verified with `go build ./tui/...`, `go test ./tui/chatwidget -count=1` and `go test ./tui -count=1`.

### Instruction refresh versus tool preparation ordering (Round 961)

- Rust #47458 overlaps the AGENTS.md/thread-instruction refresh with capability discovery, MCP setup and tool construction, and keeps delivering refresh warnings when tool preparation fails or the step is cancelled (#47458, complete).
- Go has no overlap to gain: the per-step tool list is assembled in memory from the registry (`turn/agent_loop.go:542`, `turn/runtime.go` `buildTools`) with no await point or failure path, while the instruction refresh is a side-effecting provider that emits its warnings as it runs.
- The loop calls `InstructionsProvider` before `ToolsForStep` on every step, so refresh warnings are delivered before tools are assembled and cannot be swallowed; a skill-catalog failure warns and falls back to the turn's base instructions (`appserver/global_instructions.go`), preserving Rust's instruction-error precedence.
- Rust publishes the selected-plugin snapshot only when both branches succeed; Go composes plugin and skill context into the instruction text inside the provider, so a failed refresh publishes nothing.
- Test: `TestAgentLoopRefreshesInstructionsBeforeStepToolsLikeRust` pins the provider-before-tools ordering and that the refreshed instructions and rebuilt tools both reach the second sampling request.
- Verified with `go test ./turn -run 'TestAgentLoopRefreshesInstructionsBeforeStepToolsLikeRust|TestAgentLoopRebuildsToolsForLaterModelStepsLikeRust' -count=1`.

### Agent operations routed through the control trait (Round 962)

- Rust #47520 reshapes the agent control API: tool-facing spawn/send/wait stay on `AgentControl`, resume/close/watch/inspect move to `LocalAgentRuntime`, admission, usage accounting, completion notification and shared settings become synchronous trait methods, and `AgentControlInit` hands the parent controller to delegated and guardian sessions (#47520, complete).
- Go already splits the surface the same way: `agent/tools.go:46` `ToolController` carries spawn/send/wait/resume/close for the agent tools, while turn admission, usage accounting, notification fan-out and the shared service tier live on `RuntimeRouter`.
- Delegated and guardian sessions inherit the parent runtime by construction: `runtimeAgentController` is built from the parent router and thread (`appserver/agent_controller.go`, `newRuntimeAgentControllerForTurn`), and `rootServiceTierForSpawn` mirrors Rust's `service_tier()`, so there is no separate controller object to hand down.
- `AgentControlInit`, `StatusSubscription` and `ensure_agent_known` have no Go counterparts to add: Go has no dyn-trait hand-off, and the router owns the live registry, so inspection, watching and thread-id generation resolve against the same runtime.
- Verified with `go build ./...` and `go test ./appserver -run 'TestRuntimeAgentController|TestAgentController' -count=1`.

### Delivered assistant messages in Guardian retained context (Round 963)

- Rust #47585 retains the confirmed assistant text carried by a messaging tool result's metadata at the original call's position in thread-owned Guardian context, recovers it when restoring checkpoints, and honours the call's inherited-context status (#47585, complete).
- Go already implements this: `appserver/retained_context.go:584` (`deliveredAssistantRecords`) resolves the call from the thread record plus the items seen so far, builds the retained message with the call's turn id and message id, and takes the source from the call's harness metadata; an inherited worker call is skipped unless inherited user messages are retained.
- Checkpoint restore recovers the same text from item metadata (`appserver/guardian_root_context.go:555`), so a blocked, aborted or truncated result, compaction, and resume all keep the assistant message ahead of its approval.
- Tests: `TestRuntimeRouterRetainsDeliveredAssistantMessageLikeRust`, `TestRuntimeRouterSkipsInheritedDeliveredAssistantMessageForWorkerLikeRust`.
- Verified with `go test ./appserver -run 'TestRuntimeRouterRetainsDeliveredAssistantMessageLikeRust|TestRuntimeRouterSkipsInheritedDeliveredAssistantMessageForWorkerLikeRust' -count=1`.

### Unfinished link destinations in rich streaming previews (Round 964)

- Rust #47589 keeps a Markdown link's label visible and withholds the opening parenthesis plus destination until the link closes, scanning incrementally with nested parentheses, escapes, angle-bracket destinations, titles and inline code accounted for; the holdback applies only to rich prose previews (#47589, complete).
- `tui/streaming/link_holdback.go` adds `linkPreview`, the incremental scanner mirroring Rust's `LinkPreview`, and `refreshProsePreview` now tracks `richSafeLen` alongside the structural `safeLen`.
- Prose previews cut at the pending destination offset while math previews keep the structural bound, and newline commitment plus finalization still render the original source, so resizing or completing a long URL cannot collapse rows that were already shown.
- Tests: `TestStreamingPreviewHidesUnfinishedLinkDestinationsLikeRust` (seven destinations pushed character by character, plus a long Unicode destination past the 8 KiB preview bound), `TestStreamingUnfinishedLinkFlushesOnNewlineLikeRust` (resize, newline flush, committed line), `TestStreamingPreviewWithoutLinkHoldbackLikeRust` (escaped labels, inline code, complete links).
- Verified with `go build ./...` and `go test ./tui/streaming -count=1`.

### Native POSIX spawning for hooks and shell snapshots (Round 965)

- Rust #47610 routes Unix command hooks through the PTY launcher with `ProcessMode::NewSession` instead of a `pre_exec` detach callback, keeps the session environment snapshot, hook overrides and restricted-variable filtering, and covers process-group cleanup plus preserved background descendants (#47610, complete).
- Go already detaches hooks the same way: `appserver/hook_process_tree_unix.go` starts every hook command with `SysProcAttr{Setsid: true}` and kills the negative PID on cancellation or timeout, while `hooks_runner.go:295-301` layers the session snapshot, hook overrides and `envutil.ScrubCommandEnv`. Native spawning is the Go default because `os/exec` has no `pre_exec` hook.
- Rust #47611 moves Unix shell snapshots onto the same launcher, passes the complete scrubbed child environment, and rejects a NUL-containing script before execution (#47611, complete). Go's capture already owns its process group (`tool/shell_snapshot_process_unix.go`), passes `snapshotCaptureEnv` through `envSlice`, and `os/exec` fails `Start` with `EINVAL` for NUL arguments, so the shell never runs.
- Rust #47613 only expands Linux spawn-helper lifecycle tests for a helper process Go does not have (not applicable); the guarantees it protects are covered by `TestSnapshotCaptureOwnsItsProcessGroupLikeRust` and `TestHookProcessTreeTimeoutTerminatesTree`.
- Tests: `TestSnapshotCaptureRejectsNULScriptBeforeExecutionLikeRust` (new), `TestHookCommandDetachesFromControllingTerminalLikeRust`, `TestHookProcessTreeTimeoutTerminatesTree`, `TestHookRunnerRunsCommandAndNotifies`.
- Verified with `go test ./tool -count=1`, `go test ./appserver -run Hook -count=1`, and a Linux test-binary build (`GOOS=linux go test -c ./appserver`) so the Unix files compile.

### Shift-arrow queued-message and question hints (Round 966)

- Rust #47618 makes `Shift+Left` / `Shift+Right` the primary shortcuts for advancing through async questions and editing the last queued message, retaining `Alt+Up` / `Alt+Down` as alternatives, and removes terminal-specific hint selection (#47618, complete).
- `tui/keymap.go:45` now orders the defaults `shift-left,alt-up` / `shift-right,alt-down` and uses Rust's "Move forward through questions" description.
- `tui/tea/model.go:11482` derives the queued-message edit hint from the resolved keymap's first binding and clears it when the action is unbound; `tui/bottom_pane/pending_input_preview.go:26` defaults to `shift+←` for direct preview construction.
- `tui/tea/async_questions.go:617` and `:737` use the resolved `edit_queued_message` binding for the collapsed "to answer" hint and the expanded "next question" footer, while `prompt_stack_back` resolves `shift+→`.
- `tui/tea/async_questions.go:819` displays two-stroke chords as separate shortcut labels (`ctrl-x up` -> `ctrl+x ↑`) so configured chord hints match the keymap.
- Tests: `TestQueuedMessageEditHintUsesPrimaryShortcutLikeRust`, `TestQueuedMessageEditAcceptsBothDefaultShortcutsLikeRust`, `TestAsyncQuestionHintsFollowResolvedShortcutLikeRust`, `TestPendingInputPreviewRendersQueues` (default, configured order, custom chord, unbound).
- Verified with `go test ./tui/bottom_pane ./tui ./tui/tea -count=1`.

### Reused Windows sandbox private desktops (Round 967)

- Rust #40672 keeps parent-owned private desktops alive across command-runner exits and idle gaps, reusing them only when the sandbox account and effective permissions match (#40672, complete).
- `sandbox/windowssandbox/desktop_policy.go:11` adds the policy key over write capabilities, capability SIDs, network/proxy policy, read/write roots, and deny-read/deny-write paths; order, duplicates and `..` spellings normalize while every effective permission change selects a separate desktop.
- `sandbox/windowssandbox/desktop_windows.go:59` validates the `CodexSandboxDesktop-` prefix and 1-32 hex nonce, `:74` opens an existing desktop without creating or falling back to `Default`, and `:92`/`:106` own the shared cache and legacy reopen path.
- `sandbox/windowssandbox/elevated/ipc_framed.go:17` bumps the runner protocol to 5 and carries `private_desktop_name`; `sandbox/windowssandbox/elevated_impl.go:104`/`:210` select the shared desktop per sandbox account, and `sandbox/windowssandbox/bin/command_runner/win/run_windows.go:231` opens it before spawning.
- Legacy capture and PTY/pipe launches pass the selected desktop through `ProcessSpawnRequest`/`PipeSpawnRequest`/`conpty.SpawnRequest`, so runner exits no longer destroy a policy-equivalent desktop.
- Tests: `TestPrivateDesktopNameValidationLikeRust`, `TestOpeningMissingPrivateDesktopDoesNotCreateLikeRust`, `TestSharedPrivateDesktopSurvivesLaunchHandlesAndConcurrentRequestsLikeRust`, `TestLegacyDesktopReusesOnlyEquivalentPermissionsLikeRust`, `TestElevatedDesktopPolicySeparatesEffectivePermissionsLikeRust`.
- Verified with `go test ./sandbox/... -count=1`, `go build ./...`, and `GOOS=linux go build ./sandbox/...`.

### Isolated Guardian reviewer sessions (Round 968)

- Rust #40742 adds `GuardianExtension::prepare_reviewer_options`: it clones the parent config into an isolated reviewer config, resolves ready environments to read-only permissions while preserving deny-read entries, fails closed on pending/failed environments, disables 20 features, and returns `StartThreadOptions` with internal Guardian / guardian_review sources (#40742, complete).
- `appserver/guardian_reviewer_config.go:67` adds `guardianPrepareReviewerOptions`, with `guardianReviewerIsolatedConfig` (`:93`), `guardianPrepareReviewerEnvironments` (`:157`), `guardianReviewerReadOnlyProfile` (`:198`) and `guardianReviewerDisabledFeatures` (`:211`): the parent value tree is deep-cloned, developer instructions/notify/MCP/memory/budget/personality surfaces are cleared, approval becomes `never`, the 20 features are disabled without dropping nested feature tables, and a different reviewer model drops inherited context-window limits.
- `appserver/agent_runtime.go:619` resolves the reviewed turn's parent profile and prepares the options inside `guardianReviewPlanForTurn`; pending/failed attachments set `PreparedError`. `guardianReviewInstructions` is split around `guardianReviewBaseInstructions` so the prepared config records Rust's template+contract prompt.
- `appserver/guardian_reviewer.go:904` fails the review closed when preparation fails, and the review request carries the prepared read-only profile, provider id, session id and parent ephemeral intent.
- Tests: `TestGuardianPrepareReviewerOptionsIsolatesParentConfigLikeRust`, `TestGuardianPrepareReviewerOptionsKeepsSameModelContextLikeRust`, `TestGuardianPrepareReviewerOptionsRetainsClientDeveloperMessagesLikeRust`, `TestGuardianPrepareReviewerOptionsNarrowsEnvironmentsLikeRust`, `TestGuardianPrepareReviewerOptionsRejectsUnresolvedEnvironmentsLikeRust`, `TestGuardianPrepareReviewerOptionsExternalSandboxFallbackLikeRust`, `TestGuardianReviewPlanPreparesReviewerSessionLikeRust`.
- Verified with `go test ./appserver -run 'Guardian|InternalSession' -count=1`, `go test ./state ./sandbox ./model ./config ./features -count=1`, `go build ./...`, and `go test ./parity -run TestAlignment -count=1`. Full `go test ./appserver` still hits the pre-existing `TestRuntimeRouterTurnCompletedEmitsAcceptedLineFingerprintsLikeRust` / `TestRuntimeRouterFileChangeEmitsAnalyticsLikeRust` timeouts and the Rust-checkout `https_mock` manifest drift, all reproduced at `c1f05435` without these changes.

### Typed retained section content (Round 969)

- Rust #51642 matches retained instruction splitting, assistant framing markers and empty assistant-context detection on `SectionContent::Other(ContentItem::InputText)` instead of bare `ContentItem::InputText`, and extends the snapshot-split test over line/JSON transcripts with and without a source message ID (#51642, complete).
- Go's split is structurally typed already: `state/guardian_retained_context.go:371` partitions by `RetainedInstructionPolicy(fragment.Kind)`, `:387` adds the assistant markers only while building the framed section, and `:613` accepts only a complete captured source as delivery proof, so no text comparison can move a message between sections.
- `state/guardian_retained_typed_sections_like_rust_test.go` mirrors the extended matrix: `line`/`json` x source id present/absent over four generations, asserting a stable instruction prefix, one delivery of the instruction, assistant originals framed behind the transcript, format-correct transcript records, banner-only deduplication for sourced records, no delivery proof without a message ID, and idempotent repeated preparation.
- Verified with `go test ./state -run TestRetainedInstructionTypedSectionsAcrossTranscriptFormatsLikeRust -count=1` and `go test ./state -count=1`.

### Thread unloading after disconnect during MCP startup (Round 970)

- Rust #50380 starts the thread listener even when the requesting connection closed during required MCP startup, so normal idle unloading reclaims the thread while the result stays `ConnectionClosed` (#50380, partial).
- `appserver/runtime_router.go` records closed transports (`RuntimeRouter.closedConnections`, under `clientInfoMu`, cleared by a successful initialize) from `ConnectionClosed`, and `markResponseThreadLoaded` (:6700) no longer re-subscribes a connection that closed while startup was running: it calls `scheduleThreadIdleUnload` so the thread cannot stay loaded with a dead subscriber.
- `appserver/thread_idle_lifecycle.go:382` broadcasts `thread/closed` after an idle eviction, matching Rust's ThreadClosed on unload; `markThreadUnloaded` already closes the thread MCP runtime.
- Tests: `TestThreadStartFinishingAfterConnectionCloseStillUnloadsLikeRust` (disconnect before publish, then no subscriber, idle unload, empty `thread/loaded/list`, `thread/closed`), `TestThreadStartWithLiveConnectionSubscribesThenUnloadsLikeRust`, `TestInitializeReopensClosedConnectionLikeRust`.
- Residual: `idleThreadEvictable` admits only persistent non-subagent children (existing #52081 policy), so Go keeps root and ephemeral-root threads resident and Rust's ephemeral `thread/start` unload-by-idle path has no Go counterpart; the fix covers every thread Go can evict.
- Verified with `go test ./appserver -run 'Idle|ThreadLifecycle|ThreadLoad|ThreadStart|ConnectionClosed|WorkspaceSwitch|AccountSession|ThreadClosed' -count=1`. Full `go test ./appserver` fails only on the pre-existing Rust-checkout protocol/manifest drift and `RuntimeRouter` analytics timeouts, reproduced without these changes.

### Disconnected multi-agent v2 child cleanup (Round 971)

- Rust #52081 starts lifecycle listeners for persisted multi-agent v2 children created after the client disconnects, defers attachment until an earlier runtime cleanup finishes, keeps ephemeral children loaded, and raised the default unload delay to 30 minutes (#52081, complete).
- Go realizes the listener as the retained-thread idle lifecycle: `appserver/thread_manager.go` invokes the retained hook from `RetainLiveThread`/`LiveThreadRetention.Commit`, and `appserver/thread_idle_lifecycle.go` arms `scheduleThreadIdleUnload`, pins residency during admission, and blocks resume/turn start on `awaitPendingThreadUnload`.
- The 30-minute default was superseded by Rust #57242 back to one minute; `config/config.go:2161` already matches it, so no delay change was needed here.
- Round 970 (this session) added the closed-connection guard so a start that outlives its connection cannot re-attach a dead subscriber.
- Tests: `TestIdleChildCreatedWithoutClientDisconnectsLikeRust`, `TestIdleLifecycleWaitsForPendingCleanupLikeRust`, `TestIdleLifecycleKeepsEphemeralAndRootThreadsLikeRust`, `TestIdleEphemeralThreadWithZeroOverrideUnloadsLikeRust`, `TestIdleEphemeralSubagentStaysLoadedEvenWithZeroOverrideLikeRust`, `TestIdleUnloadPreservesQueuedMailLikeRust`, `TestThreadUnloadDelayDefaultsToOneMinuteLikeRust`.
- Verified with `go test ./appserver -run 'TestIdle|TestThreadUnloadDelay' -count=1`.

### Daemon restart feature overrides (Round 972)

- Rust #53751 applies `--enable`/`--disable` and boolean `features.*` overrides to `codex app-server daemon restart`, accepts nested `enabled` values, normalizes aliases, replaces conflicting saved aliases while preserving unrelated settings, persists them, and rejects invalid toggles before stopping the daemon (#53751, complete).
- `app/daemon_restart_features.go` extracts and validates the overrides (canonical keys, `imagegenext` precedence, unknown/non-boolean errors) before any daemon work; `app/app.go:1766` dispatches to `LifecycleRunner.RestartApplyingFeatures`.
- `appserverdaemon/lifecycle.go:215` replaces every saved spelling of a requested canonical feature, stores the canonical key, and restarts the managed backend for running and stopped daemons; `RestartWithFeatures` keeps the compatibility path that reuses an unchanged backend.
- Tests: `TestDaemonRestartFeatureOverridesLikeRust`, `TestLifecycleRunnerRestartApplyingFeaturesLikeRust`, `TestLifecycleRunnerRestartWithFeaturesLikeRust`, `TestDaemonSettingsFeatureOverridesAndUpdaterLikeRust`.
- Verified with `go test ./app -run 'TestDaemonRestartFeatureOverridesLikeRust|TestAppServerDaemon' -count=1` and `go test ./appserverdaemon -run 'TestLifecycleRunnerRestartApplyingFeaturesLikeRust|TestLifecycleRunnerRestartWithFeaturesLikeRust|TestDaemonSettings' -count=1`.

### WebSocket continuation failure reasons (Round 973)

- Rust #51895 replaces the boolean continuation-reuse check with typed `NonIncrementalReason` values (changed request properties, `input_shortened`, `input_mismatch`, missing response state/ID) and adds `previous_item_type`/`current_item_type` labels (#51895, not applicable).
- Go has no client-side continuation-eligibility comparison to annotate: `model/responses_agent.go` sends the accumulated input with an optional caller-resolved `previous_response_id`, and `model/agent_metrics.go:424` classifies sends only as `incremental/previous_response`, `full/connection_reused` or `full/initial`.
- The per-item and per-property reasons therefore have no Go code path; diagnostics stay bounded metadata with no request contents or identifiers. The same architecture difference was recorded for `#46051` (Round 705).
- Verified with `go test ./model -run 'WebsocketContinuation|ContinuationMetric|PrewarmUsesWebSocket' -count=1`.
### Windows environment for remote MCP servers (Round 974)

- Rust #50129 extends the remote stdio MCP allowlist with `SYSTEMROOT`/`TEMP`/`TMP` inside `ExecutorStdioServerLauncher::remote_env_policy`, for a Unix orchestrator launching a stdio server on a Windows executor (#50129, not applicable).
- Go has no remote stdio launcher: `mcp/stdio_env.go` implements only the local placement, and `localStdioEnvVarNames` rejects `env_vars` entries with `source: remote` ("which requires remote MCP stdio"), so there is no remote allowlist to extend.
- The same conclusion was recorded in `update/plan_2026_10_06.md:102` and `:293`.
- Verified with `go test ./mcp -run TestCreateEnvForMCPServerRejectsRemoteSourceLikeRust -count=1`.
### Shared thread instruction provider self-delegation (Round 975)

- Rust #50093 keeps one replaceable `SharedThreadInstructionsProviders` wrapper per root and avoids replacing its backing provider when a relaunched root supplies that same wrapper (#50093, not applicable).
- Go has no host `ThreadInstructionsProvider`: `ThreadStartParams` exposes only static `baseInstructions`, while `turnInstructionsProvider` and `turnStepInstructionsProvider` build per-turn closures, skip subagents, and are never stored in a root-scoped replaceable wrapper.
- The missing provider surface was already audited as an architecture difference for #44701 (Round 481) and recorded for #50093 in `update/plan_2026_10_06.md:294`; no shared wrapper exists that could recurse into itself.
### Existing audit closures (Round 976)

- Rust #49956 caches the MCP hook argument placeholder regex in a `LazyLock` (#49956, complete). Go already has package-scoped `mcpArgumentPlaceholderPattern` in `appserver/hooks_runner.go:467`, reused by `resolveMcpTemplateString`; `TestExpandMcpArgumentTemplateMatchesRust` covers substitution and missing-field errors.
- Rust #50035 follows `tools/list` pagination in legacy MCP mode (#50035, complete). Go passes `NextCursor` unconditionally from both `listMCPHTTPTools` and `listMCPStdioTools` into `collectMCPPaginatedWithLimitAndMeta`; `TestMCPReadOnlyToolsRequestMetadataLikeRust` exercises both protocol modes over two pages.
- Rust #50052 preserves question context in recovered TUI answer drafts (#50052, complete). `bottompane.AsyncQuestions.TakePendingDrafts`/`recoveredQuestionDraft` already quotes sanitized 512-byte-bounded titles and keeps answers untruncated; `tui/tea/async_questions.go` separates recovered entries with blank lines while retaining composer elements. Tests cover sanitization, UTF-8 truncation, turn-end recovery, and command-prefix escaping.
- Verified with `go test ./appserver -run TestExpandMcpArgumentTemplateMatchesRust -count=1`, `go test ./mcp -run TestMCPReadOnlyToolsRequestMetadataLikeRust -count=1`, `go test ./tui/bottom_pane -run TestAsyncQuestionsTakePendingDrafts -count=1`, and `go test ./tui/tea -run TestTurnEndRecoversAsyncQuestionDraftsLikeRust -count=1`.
### Composer footer and tmux probe audits (Round 977)

- Rust #50105 moves composer footer construction, mode resolution, hint overrides, quit hints, and custom height into `footer_state.rs` without behavior changes (#50105, complete). Go already has the equivalent `composer.FooterState` in `tui/bottom_pane/chat_composer/footer_state.go` plus `tui/bottom_pane/footer.go` props/rendering; `TestFooterStateFlashContextAndQuitReminderMatchRustCore` covers context order, flash/quit expiry, and mode reset.
- Rust #50219 bounds tmux input-option probes to one second (#50219, not applicable). Go has no tmux `show-options`/`set-option` probe for terminal input setup; Bubble Tea owns mouse and keyboard modes, and the only tmux subprocesses are explicit clipboard-copy operations in `tui/clipboard_tmux.go`. The same structural N/A is recorded in `update/plan_2026_10_07.md:1345`.
- Verified with `go test ./tui/bottom_pane/chat_composer -run TestFooterStateFlashContextAndQuitReminderMatchRustCore -count=1` and `go test ./tui/tui -count=1`.
### Guardian verified answers across compaction (Round 978)

- Rust #41852 preserves trusted user answers across compaction by matching recorded answers against retained review history (#41852, complete). Go records accepted answers directly in `retainedctx.RetainedContext` through `verifiedAnswerRecorderForTurn`, persists them as sparse rollout facts, and the reviewer reads that context independently of the model-visible window; `RetainedContext.Rollback` removes answers at the rolled-back turn boundary.
- Rust #41857 also matches the current conversation so answers survive when the original tool call exists only there (#41857, complete). Go does not filter answers by locating the original tool call: accepted answers are retained directly and restored after restart, which covers both current and retained history while preserving rollback semantics.
- Tests: `TestRuntimeRouterRecordsVerifiedAnswersLikeRust`, `TestRuntimeRouterReplaysVerifiedAnswersAfterRestartLikeRust`, `TestModelGuardianReviewerIncludesVerifiedAnswersLikeRust`, `TestGuardianReviewHistorySurvivesCompactionAndRollbackLikeRust`, and `TestRetainedEvidencePreservesOrderThroughRecoveryCheckpointAndRollback`.
- Verified with `go test ./appserver -run 'TestRuntimeRouterRecordsVerifiedAnswersLikeRust|TestRuntimeRouterReplaysVerifiedAnswersAfterRestartLikeRust|TestModelGuardianReviewerIncludesVerifiedAnswersLikeRust|TestGuardianReviewHistorySurvivesCompactionAndRollbackLikeRust' -count=1` and `go test ./retainedctx -run TestRetainedEvidencePreservesOrderThroughRecoveryCheckpointAndRollback -count=1`.
### Stable Markdown tables during streaming (Round 979)

- Rust #50207 releases table holdback once the containing top-level Markdown block is stable, and requests a completion scrollback reflow when late reference-link definitions can change already-emitted blocks (#50207, complete).
- Go: `tui/streaming/table_holdback.go` `Scan(source, completedSourceLen)` mirrors `TableHoldbackScanner::scan` by releasing holdback when the table start precedes the boundary, resetting fence/line state, and rescanning the remaining tail for later tables.
- Go: `tui/streaming/controller.go` derives `completedSourceLen` from `markdown.AnalyzeStreamingMarkdown` (goldmark top-level block start, Rust `last_top_level_block_start`, filtered by pending display math) and rescans after push, finalize, and resize, so a completed table enters the stable queue while later tables stay mutable.
- Go: `StreamController.NeedsScrollbackReflow` mirrors `needs_scrollback_reflow`, including a reference definition in the unterminated pending source; the Tea transcript replaces the streamed answer with the finalized markdown and re-renders it from source, matching the Required reflow branch.
- Tests: `TestStreamControllerReleasesCompletedTableIntoScrollbackLikeRust`, `TestStreamControllerNeedsScrollbackReflowForLateReferenceDefinitionLikeRust`, `TestAnalyzeStreamingMarkdownLastTopLevelBlockLikeRust`, `TestHasReferenceLinkDefinitionLikeRust`.
- Verified with `go test ./tui/streaming ./tui/markdown -count=1` and `go test ./tui/... -count=1`.
### Dependency, extension-lifetime, and plugin-cache audits (Round 980)

- Rust #50166 upgrades the Rust `age` crate and drops the RUSTSEC-2026-0173 advisory exception (#50166, not applicable). Go has no Cargo/Bazel lockfile or deny.toml and uses the Go module `filippo.io/age` v1.3.2 with no proc-macro-error2 chain.
- Rust #41020 scopes extension capabilities to invocation lifetimes (#41020, not applicable). Go has no extension registry or `ToolEnvironment`/`ToolCall` lifetime surface; `tool.Executor.Execute(ctx, *Invocation)` receives call-scoped values and runs synchronously.
- Rust #41231 instruments the loaded-plugin cache (#41231, not applicable). Go has no `PluginsManager` loaded-plugin cache or load semaphore; only the owned manifest cache from #49099 exists, and capabilities are derived synchronously from the catalog.
- Rust #50189 replaces the Figma OAuth issuer-binding exception with Mercado Pago (#50189, complete). `mcp/oauth_discovery.go` already carries the Mercado Pago and Robinhood triples with exact issuer/origin matching; the provider-exception test now also covers the two Robinhood lookalikes from upstream.
- Tests: `TestMCPOAuthDiscoveryKeepsIssuerBindingExceptionsAndRejectsLookalikes`, the `plugin` suite, the `safety` secret tests, and the `tool` suite.
- Verified with `go test ./mcp -run TestMCPOAuthDiscoveryKeepsIssuerBindingExceptionsAndRejectsLookalikes -count=1`, `go test ./plugin -count=1`, `go test ./safety -run Secret -count=1`, and `go test ./tool -count=1`.
### Native verification in MCP tool continuations (Round 981)

- Rust #44346 routes MCP 2026-07-28 tool inputs through the client elicitation service, accepts `openai/elicitation/create` form and `openai/userVerification` inputs, preserves opaque `requestState`, backs off on state-only rounds, and refuses to replay a tool after a proof was submitted (#44346, complete).
- `mcp/protocol_2026.go` validates the whole input-required round before prompting, rejects unsupported methods/modes and unadvertised capabilities without prompting, answers `openai/userVerification` through `mcpElicitationResult`, and sleeps 50/100/200/250ms for state-only rounds.
- `mcp/user_verification.go` validates the request payload like Rust's `user_verification::parse_request`; `mcp/elicitation_runtime.go` now populates `Title`/`Description`/`Challenge` for the TUI prompt.
- `mcp/http_client.go` tracks a submitted proof across rounds and returns `the tool was not restarted` instead of replaying the call after a 404/410 session expiry, while 401 auth challenges still surface unchanged.
- Tests: `TestNativeVerificationContinuationValidatesAndPreservesLikeRust`, `TestNativeVerificationContinuationRejectsBeforePromptingLikeRust`, `TestNativeVerificationContinuationRejectsUnsupportedModesLikeRust`, `TestNativeVerificationContinuationBacksOffStateOnlyRoundsLikeRust`, `TestNativeVerificationContinuationDoesNotRestartAfterProofLikeRust`, `TestNativeVerificationContinuationPreservesAuthChallengesLikeRust`.
- Verified with `go test ./mcp -count=1`, `go vet ./mcp`, and `go build ./...`.

### Startup WebSocket preconnect overlap (Round 982)

- Rust #47635 overlaps the startup WebSocket prewarm handshake with tool capture, carries model/service-tier routing hints, recovers auth failures during preconnect and falls back to HTTP on 426 (#47635, partial).
- `model/responses_agent.go` `Prewarm` now builds the warmup through `buildResponsesAgentRequest`, so `generate=false` carries finalized tool/Responses Lite metadata and the effective service tier; guardian reviewer preconnects keep draining `service_tier` and the routing hint, while ordinary Codex-backend preconnects send `model=<model>;tier=<tier>`.
- Preconnect recovers a 401 handshake through `recoverUnauthorizedAuth` and retries the dial (bounded by the recovery cursor); 426 still disables websockets and returns `nil`.
- `appserver/session_startup_prewarm.go` passes the thread's selected service tier to the base and history prewarms.
- Tests: `model/prewarm_preconnect_like_rust_test.go` (routing hint and `service_tier`, guardian omission, 401 refresh retry, finalized `additional_tools` declarations). Verified with `go test ./model -count=1` and `go test ./appserver -run Prewarm -count=1`.
- Residual: Rust's startup prewarm captures its own turn-scoped tools during the handshake; Go's startup hook runs at thread/start before MCP discovery and has no such snapshot, so the base warmup carries only caller-supplied tools.

### Goal empty-continuation breaker (Round 983)

- Rust #44320 blocks a goal after three consecutive empty automatic continuation turns (#44320, complete).
- `appserver/goal_empty_responses.go` counts only automatically admitted goal turns, treats a textless terminal agent message or an interrupted turn with no activity as empty, and resets the streak on user input, reasoning/answer text, questions, tool activity, non-automatic turns, and goal changes; `appserver/thread_goal_state.go` blocks the goal, emits `ThreadGoalUpdated`, records the transition, and clears the streak.
- Tests: `TestGoalEmptyAutomaticContinuationsBlockAfterThree`, `TestGoalInterruptedAutomaticContinuationsCountTowardNoActivityLikeRust`, `TestGoalEmptyStreakResetsOnActivityAndNonAutomaticTurns`, `TestGoalEmptyStreakResetsOnGoalChange`, `TestGoalEmptyPartialAnswersDoNotCountAsEmptyFinalLikeRust`. Verified with `go test ./appserver -run 'GoalEmpty|GoalInterrupted' -count=1`.

### Managed proxy listener handoff audit (Round 984)

- Rust #40999 hardens linux-sandbox managed proxying by moving loopback listeners to host bridges over anonymous socketpairs with descriptor validation and private, short-lived bootstrap channels (#40999, not applicable).
- Go's proxy routing has no listener handoff: the host bridge listens on a filesystem UDS and the bwrap inner helper runs a loopback bridge that dials it, with `Pdeathsig` bounding both helper lifetimes. No socketpair/SCM_RIGHTS or inherited control descriptor exists on this path.
- Residual recorded for future work: the Go transport keeps the readable socket directory and the extra loopback hop, and stale `codex-linux-sandbox-proxy-*` directories are never removed.

### Configurable sleep-tool gating (Round 985)

- Rust #41243 adds a stable `sleep_tool` feature and a structured `features.sleep_tool.mode` (`model_driven` default, `always_on`) that gates `clock.sleep` independently of `clock.curr_time` (#41243, complete).
- `appserver/turn_runtime.go` `decideClockToolEnablement` already implemented the runtime gate: `always_on` registers sleep whenever the feature is enabled, `model_driven` keeps the legacy `current_time_reminder.sleep_tool` value when the reminder feature is on and falls back to `model_has_clock` otherwise; `clock.curr_time` stays gated by the reminder feature or the model clock.
- `config/config.go` now rejects an unknown `features.sleep_tool.mode` (closed enum, top level and profiles) on the `LoadEffective` path, and strict config rejects unknown `sleep_tool` fields, mirroring `SleepToolConfigToml`'s `deny_unknown_fields`.
- `config/features_file.go` `SetFeature` writes `enabled` into an existing `[features.sleep_tool]` table or inline `sleep_tool = {...}` value instead of appending a conflicting boolean, so `codex features enable/disable sleep_tool` preserves `mode`; `config/api.go` `applyEdit` routes a boolean `SetPath` on an existing structured feature table to `<feature>.enabled`.
- Tests: `config/sleep_tool_gating_like_rust_test.go` and `appserver/sleep_tool_gating_like_rust_test.go`. Verified with `go test ./config -count=1`, `go test ./appserver -run 'SleepTool|CurrentTime|Clock' -count=1`, `go test ./app -run Feature -count=1`, and `go vet ./config`.

### App-server notification media filtering (Round 986)

- Rust #41416 adds the disabled-by-default `omit_app_server_notification_media` feature and strips inline image/audio content from `item/started`, `item/completed`, and `rawResponseItem/completed` while leaving model input untouched (#41416, complete).
- `appserver/runtime_router.go` already gated `withoutNotificationMedia` on the feature and `appserver/notification_media.go` already dropped media content entries; this round closed the remaining Rust differences.
- `imageGeneration` and raw `image_generation_call` items now clear their inline `result` payload while keeping `fileId`, `revisedPrompt`, and `inputImages`; MCP tool results drop content entries that carry `resource.blob` while keeping text and blob-less resources; `local_image`/`local_audio` references are preserved.
- Tests: `appserver/notification_media_like_rust_test.go` covers image generation, raw image generation, MCP blob filtering, local media retention, and the feature gate over all three notification types. Verified with `go test ./appserver -run NotificationMedia -count=1` and `go test ./appserver -count=1` (only the seven known pre-existing manifest/protocol-surface/analytics failures remain).

### Shared linked-tool mention parsing (Round 987)

- Rust #41218 removes the TUI skill extractor's duplicate linked-tool parser and reuses `mention_codec`'s parser, mention-name character set, and environment-variable filter (#41218, complete).
- `tui/mention_codec.go` now exports `ParseLinkedToolMention` (history decoding keeps calling the same unexported implementation with its existing byte slice), and `tui/chatwidget/skills.go` calls it plus `IsCommonEnvVar`/`IsMentionNameByte` instead of its own copies.
- The plaintext scan keeps Go's colon-qualified skill spelling (`$Docs:review`), matching the submission Rust produces after the composer binds a qualified skill, while linked mentions follow the codec's stricter name characters.
- Tests: `tui/mention_codec_like_rust_test.go` mirrors the upstream UTF-8 byte-offset and malformed-link tables, and `tui/chatwidget/skills_mention_like_rust_test.go` covers Unicode/whitespace-trimmed paths, both sigils, env-var and non-skill links, and the qualified spelling. Verified with `go test ./tui/... -count=1`.

### Delegated tool authority in the TUI (Round 988)

- Rust #41046 starts delegated `create_thread` / `send_message_to_thread` turns as namespaced tool output with an empty user input list, keeps delegation parsing namespace-scoped, and renders replayed delegation outputs with their source task attribution (#41046, complete).
- `app/dynamic_tools_helpers.go` `startTurn` already sent `ToolOutput{Name, Namespace: codex_tui}` with no user input, and `dynamicParseDelegatedToolOutput` already restricted summaries to `codex_tui`/`codex_app`.
- `context.ParseDelegatedPrompt` now decodes `&lt;`/`&gt;` before `&amp;` (so `&amp;lt;` stays escaped) and `ParseDelegatedToolOutput` trusts `codex_tui`/`codex_app` only; `context.DelegatedToolOutputNotice` and `tui/history_cell.NewDelegatedPromptCell` provide the Rust `PrefixedWrappedHistoryCell` shape, and `chatwidget.ReplayDelegatedToolOutput(Cell)` plus `ReplayItemRouteFunctionCallOutput` expose the replay/transcript behavior without touching composer history.
- Tests: `context/delegation_like_rust_test.go`, `tui/chatwidget/delegated_replay_like_rust_test.go`. Verified with `go test ./context -count=1`, `go test ./tui/... -count=1`, and the delegated session/state tests.

### Bundled resource directory move (Round 989)

- Rust #41477 moves core/tui include_str! resources into `core/assets` and `tui/assets` and narrows Bazel compile data; asset bytes are unchanged (#41477, complete).
- Go vendors the same blobs rather than mirroring the Bazel layout: `context/templates/persistent_mode.md`, `state/templates/guardian/policy_template.md`, and `applypatch.LarkGrammar` already had frozen-snapshot comparisons, and the new `parity/tui_assets_like_rust_test.go` compares `tui/tooltips.txt` and `tui/tea/prompt_for_init_command.md` byte-for-byte against `codex-rs/tui/assets` from the pinned snapshot.
- `tui/tooltips.txt` carried a Go-only `/presets` tip that no Rust revision had while missing Rust's `chat.open_presets` 0-9 menu tip; Go already implements that menu (`tui/tea/quick_dials.go`), so the file now matches upstream exactly.
- Resources Go builds in code instead of vendoring (`agent_names.txt`, agent builtins, consequential tool templates, inline-visualization viewer html/css) are implementation details with no asset counterpart, not behavioral drift from the move.
- Verified with `go test ./parity -run 'TestRustTuiAssetsMatchGo|TestRustPersistentModePromptMatchesGo|TestRustGuardianPolicyPrompt|TestRustApplyPatchFreeformSpecMatchesGo' -count=1` and `go test ./tui/... -count=1`.

### History ingestion turn metadata (Round 990)

- Rust #41743 sets `history_ingest_requested` in Responses turn metadata whenever the history-notes token-budget extension is enabled, reserves the key against caller override, and omits it when the extension is off (#41743, complete).
- Go already carried the field end to end: `codexapi/responses_metadata.go` reserves the key, `codexapi/client_context.go` serializes it only when true and keeps it out of MCP metadata, and `appserver/turn_runtime.go` derives it from `TokenBudgetConfig().UseHistoryNotesExtension` on both the turn and compaction request paths.
- The new `appserver/history_ingest_metadata_like_rust_test.go` exercises the production entry point (`RegisterTurn` + `activeTurnClientMetadata`): the key is true with the extension on, absent when off, and caller metadata can neither flip it to false nor forge it.
- Verified with `go test ./appserver -run 'TestActiveTurnClientMetadataCarriesHistoryIngest|TestHistoryIngestRequestedForTurn|TestCompactResponsesClientMetadataCarriesHistoryIngest' -count=1`, `go test ./turn -run HistoryIngest -count=1`, and `go test ./codexapi -run 'HistoryIngest|Reserved' -count=1`.

### Guardian review evidence across compaction (Round 991)

- Rust #41846 keeps a bounded, chronological review transcript independent of the model's compacted history, limits user messages and other items separately, uses it for synchronous and asynchronous Guardian review, and resets it only on rollback or history reconstruction (#41846, complete).
- Go mirrors that with `appserver/guardian_review_history.go`: per-kind count/byte limits with same-kind oldest-first eviction, `truncateBeforeTurn` for thread/revert, and `forget` on reconstruction; `turn_runtime.go` captures the pre-compaction items before replacing the record, and `guardian_reviewer.go` feeds the same selection to the synchronous and asynchronous reviewers.
- The new `appserver/guardian_review_history_bounds_like_rust_test.go` floods 300 tool outputs without evicting a user instruction (and stays within 128 non-user items), then forces the byte budget with 6x1MiB tool outputs to confirm only old tool evidence is dropped.
- Verified with `go test ./appserver -run 'TestGuardianReviewHistory' -count=1`.

### Boxed session startup future audit (Round 992)

- Rust #41853 makes `Session::spawn` return a `BoxFuture`, marks the wrapper `#[inline(never)]`, and awaits it directly at creation sites; initialization stays lazy and no protocol or persistence behavior changes (#41853, not applicable).
- Go has no future type to box and no concrete-future-at-API-boundary or inlined-megaframe problem: session startup is built synchronously in the appserver thread-creation path, so there is no corresponding call site to change.

### MCP event stream client retention (Round 993)

- Rust #41892 builds the `events/stream` request inside `McpEventStream::open` (including the caller `_meta`), retains only the `RmcpClient` for the subscription lifetime, and drops the connection-level helper and Plugin Runtime wording (#41892, complete).
- Go already keeps the per-stream HTTP client alive in the streaming goroutine until `Close` or server termination, but the app-server start path parsed `_meta` and never forwarded it. `mcp.OpenMCPEventStreamWithMeta` now copies the caller object into the request params, and `appserver/runMCPEventStream` validates and passes `params.Meta`, rejecting non-objects with the Rust-equivalent message.
- Tests: `mcp/mcp_event_stream_retention_like_rust_test.go` drives a real SSE transport through active and follow-up notifications then asserts `Close` releases it, `mcp/mcp_event_stream_meta_like_rust_test.go` covers `_meta` forwarding and omission, and `appserver/mcp_event_stream_meta_like_rust_test.go` covers the end-to-end start request.
- Residual: Rust's rmcp client auto-injects `progressToken` into `_meta`; Go handles progress through separate call options, so that token is not part of the emitted object.

### Detached PID startup module split (Round 994)

- Rust #42374 only moves Unix detached-process launch and PID publication from `pid.rs` into `pid_start.rs`; PID reservation and lifecycle behavior are unchanged (#42374, complete).
- Go already splits the equivalent code by platform and concern (`pid_process_windows.go`, `managed_command_darwin.go`, `pid_identity_linux.go`), so there is no behavior to port; existing `appserverdaemon` tests cover detached launch probing/reaping, the restricted error contract, PID start timeouts, shutdown watching, and lifecycle start/restart/stop.
- Verified with `go test ./appserverdaemon -count=1`.

### Bedrock GPT-6-Astra catalog entry (Round 995)

- Rust #42619 adds `openai.gpt-6-astra` to the Amazon Bedrock Mantle catalog ahead of the 5.6 series and emits global/US cross-region Runtime variants that keep bundled metadata (#42619, complete).
- Go already carries the newer shape of that catalog: `AmazonBedrockGPT6AstraModelID`, priority 1 in `AmazonBedrockModelCatalog`, and both Runtime prefixes. Existing tests pin ordering, display names, context windows, the Runtime variant list (with a comment naming #42619), and Astra's inherited `ultrafast` capability.
- Verified with `go test ./model -run 'Bedrock|AmazonBedrock' -count=1`.

### Feedback note view module split (Round 996)

- Rust #42792 only moves the optional feedback note input and its tests out of the submission module into `bottom_pane/feedback_note_view.rs`, updating exports and snapshot metadata without changing submission behavior (#42792, complete).
- Go's layout is already split (`tui/bottom_pane/feedback_view.go` with `feedback_view_test.go`); the note view's submit/cancel/paste/row behavior and multiline-paste burst handling are pinned by existing Rust-mirroring tests. Verified with `go test ./tui/bottom_pane -run Feedback -count=1`.

### Global metrics installation entry point (Round 996)

- Rust #42603 exposes `install_global_metrics` for installing an externally owned `MetricsClient` during startup and returning a handle that keeps the exporter alive for flush/shutdown (#42603, complete).
- Go already exposes `telemetry.InstallGlobalMetrics(*MetricsClient) *MetricsClient` (delegating to `metrics.InstallGlobal`, nil clears) and returns the client as the owner; `otelinit` calls it on startup/teardown. `telemetry/global_metrics_test.go` and `otelinit/otelinit_test.go` cover install, return identity, timer routing, and clearing.
- Verified with `go test ./telemetry -run TestStartGlobalTimerUsesInstalledMetricsClientLikeRust -count=1`.

### Exec-server release version in environment info (Round 997)

- Rust #42270 adds `executorVersion` to `EnvironmentInfo`, returns it from `initialize` and `environment/info`, stabilizes it at process startup, and falls back to `0.0.0` for unknown or legacy executors (#42270, complete).
- Go already sets the version once from `cmd/internal/codexmain` via `execserver.SetExecServerVersion`, defaults `ExecServerExecutorVersion` to `0.0.0`, serves it through `localEnvironmentInfo` on both surfaces, and caches the initialize response on the client (`rememberEnvironmentInfo`/`cachedEnvironmentInfo`).
- Existing tests pin the fallback, stamped version, JSON serialization, and version-gated capability discovery. Verified with `go test ./execserver -run 'TestExecServerBuildIdentityMetadata|TestBuildIDVectorsMatchRust|TestCapabilityDiscoveryV2' -count=1` and `go test ./appserver -run ExecutorCapabilityDiscoveryV2 -count=1`.

Status after reconciliation: `2157 complete`, `451 not_applicable`,
`346 audit_pending`, `190 regression_pending`, `5 partial`,
`6 blocked_precondition`.
