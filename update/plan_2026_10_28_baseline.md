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

Status after reconciliation: `2032 complete`, `400 not_applicable`,
`460 audit_pending`, `254 regression_pending`, `3 partial`,
`6 blocked_precondition`.
