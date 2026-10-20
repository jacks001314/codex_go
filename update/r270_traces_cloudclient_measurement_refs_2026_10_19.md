# 2026-10-19 non-TUI round: tool traces, cloud gRPC, measurement references

## Implemented

- `#44661`: `turn.ToolDispatcher` emits `codex.tool_call_received` and
  `codex.tool_result_ready` for direct and code-mode calls (ids/tool names only),
  records readiness for ordinary errors and aborts while fatal errors propagate,
  emits nested dispatch/interruption events, and opens
  `code_mode.handler.execute|wait` spans with completed/failed/interrupted
  outcomes.
- `#50113`: new `cloudclient` package: endpoint/credential validation,
  `ThreadService/Resume` unary RPC, `ThreadService/Attach` server stream with
  opaque Notification/ServerRequest envelopes, 150s setup bound, no total stream
  deadline, sanitized `RpcError`.
- `#46542`: `plugin.RemotePluginMeasurementCache` (four entries keyed by auth
  identity/endpoint/remote id/plugin id/version, shared preparation, historical
  version reuse, leased isolated homes) plus
  `appserver.authenticatedMeasurementBundleFetcher` (GLOBAL-only authenticated
  catalog lookup, ambiguity rejection, private tar.gz materialization).
- `#47132`: proxy tables and nested MITM CA/hook tables now reject unknown
  fields; external MITM CA parsing/validation from the previous round remains.
- `#46572`: cloud plugin catalog cache is scoped by provider type plus account
  auth identity; a scope change drops the stale catalog before refresh and a
  mid-flight identity change discards the result.
- `#52081`/`#52034`: idle-thread lifecycle for persistent multi-agent v2
  children. A retained child arms `thread_unload_delay_secs`, unsubscribe /
  connection loss / turn completion reschedule it, running or subscribed
  threads are rechecked at fire time, ephemeral children and roots stay loaded,
  residency pins block eviction during input submission, queued mail survives
  unload, and resume/turn start waits for pending cleanup.
- `#48812`: idle unsubscribed threads schedule a history-aware websocket
  prewarm with `generate:false`; the prepared response id is reused only when
  the next input extends the prewarmed history and model/instructions/service
  tier match, and prewarm metrics carry `input_mode=base|history`.

## Test and harness fixes

- `state`: the quick-check identity now includes size/modtime so a rebuilt
  database that reuses a Windows file index is validated again.
- `exec`: an unset request cwd defaults to the absolute process working
  directory instead of `.`, which config path facts reject.
- `app`: cloud backend origin validation applies only to saved ChatGPT auth;
  API-key/access-token callers may use a custom origin.
- `tool`: POSIX login-shell path tests use POSIX-convention executor
  directories on Windows hosts.
- `recordreplay`: token-usage and other internal paginated records count toward
  ordinal continuity, and the frozen digest comparison normalizes CRLF.
- `parity`: the Go top-level directory inventory includes `cloudclient`.

## Ledger

Non-TUI verdicts after the recompute:

- `audit_pending` 105 → 0
- `complete` 837 → 928
- `not_applicable` 957 → 974
- `blocked_precondition` 3 → 2
- `partial` 3 → 1

Newly closed rows: `#44661`, `#50113`, `#46542`, `#41183`, `#41421`,
`#41393`, `#41452`, `#40466`, `#42196`, `#42373`, `#40007`; `#45559` is
`not_applicable` (no service-backed provisioning client); `#45799` stays
`partial` (legacy cleanup only); `#44872` stays `blocked_precondition` (no MXC
runtime). The same recompute also closed the audited Guardian/MCP rows
(`#41660`, `#43478`, `#42819`, `#42852`, `#42744`, `#39994`, `#42144`,
`#42746`, `#40013`, `#42770`, `#44569`, `#41117`, `#47081`, `#43601`,
`#41006`, `#43602`, `#44281`, `#43031`, `#41899`, `#42413`, `#40739`,
`#41005`) plus `#43884`, `#42149`, `#41208`, `#41569`, `#41924`, `#40653`,
`#40487`, `#43547`, `#43568`.

## Verification

- `go test ./cloudclient ./network ./plugin ./turn -count=1`
- `go test ./appserver -run 'AllowedTools|CloudPlugin|AuthenticatedMeasurementBundleFetcher|ThreadAttachment' -count=1`
- `go test ./state ./exec ./recordreplay ./model ./tool -count=1`
- `go test ./appserver -run TestIdle -count=1`
- `go build ./...`
- `go test ./... -count=1 -p 1` (full suite, zero failures)
- `git diff --check` and `gofmt -l` on every changed Go file are clean.

## Remaining known gaps

The shared `r109` status table was reconciled concurrently by the TUI lane;
`r90` remains the evidence ledger of record for the non-TUI rows below.

- `partial`:
  - `#51840` bubblewrap integrity runtime (Linux build covered, no host runtime)
- `blocked_precondition`:
  - `#51831` MXC integrity dependency/policy
  - `#44872` MXC managed network policy
