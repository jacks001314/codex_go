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

- `audit_pending` 105 → 91
- `complete` 837 → 848
- `not_applicable` 957 → 958
- `blocked_precondition` 3 → 4
- `partial` 3 → 4

Newly closed rows: `#44661`, `#50113`, `#46542`, `#41183`, `#41421`,
`#41393`, `#41452`, `#40466`, `#42196`, `#42373`, `#40007`; `#45559` is
`not_applicable` (no service-backed provisioning client); `#45799` stays
`partial` (legacy cleanup only); `#44872` stays `blocked_precondition` (no MXC
runtime).

## Verification

Pending full-suite rerun.
