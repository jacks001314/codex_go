# Tool metadata budgets and non-TUI audit batch

Upstream commits audited:

- `#47758` Preserve more tool metadata within outgoing message budgets
- `#47957` Bound tool-call observations to the outgoing Responses message budget
- `#48100` Add an HTTP client for remote agent message boards
- `#47603` Add a reap-only drop policy for child processes
- `#47686` Make thread-owned Guardian context always enabled

## #47758 / #47957 implementation

- `NewToolResultMetadata` now captures the complete MCP `_meta` snapshot. The
  obsolete 32 KiB per-result cap is removed, and the legacy aggregate prompt
  budget is 2 MiB as in #47758.
- `codex.tool_metadata.shed_bytes` is emitted with Rust's byte buckets and the
  `stage=message` / `stage=direct_retained` labels.
- The direct-retention budget now retains `openai/resource_access`, then omits
  generic snapshots, then clears metadata, and only then drops the call record;
  the recorder is wired to the app-server turn metrics.
- The outgoing message bounding callback invalidates a Code Mode cell when the
  wire copy loses or changes its recorded calls or arguments (#47957).
- The app-server executor plugin test now closes its router so background
  workers cannot write into a removed `TempDir` during Windows cleanup.

## Verification

```text
go test ./model ./turn -count=1
go test ./features ./config ./agentboard -count=1
go test ./appserver -count=1
go vet ./model ./turn ./appserver
go build ./...
```

## Audit verdicts

- `#48100`: complete; `agentboard.RemoteBoard` and notifications cover the
  Rust client, limits, clock injection, and tests.
- `#47603`: not applicable; Go owns PTY processes through `os/exec` and
  explicit `Wait`/`Kill`, with no Rust `Child` drop-policy carrier.
- `#47686`: complete; the deprecated feature key is removed/ignored and the
  thread-owned retained-context implementation is the only Guardian path.
