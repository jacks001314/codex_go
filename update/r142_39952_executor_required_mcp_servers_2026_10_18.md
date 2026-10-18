# Rust #39952: required MCP servers from selected executors

Upstream commit: `054588acfe4a51ef73bf0209dcaff370419322ab`

## Implemented

- Executor-discovered HTTP MCP servers retain their `required` setting during
  parsing and during projection into the thread MCP runtime.
- The selected-executor projection no longer downgrades every discovered server
  to optional.
- Optional discovery remains best effort, while a required server participates
  in startup validation and waits for its live inventory.

## Verification

The executor MCP discovery test now marks its bearer-authenticated server
required and delays the first startup request by 100 ms. A non-blocking capture
with a 10 ms optional grace still returns the server's tool, proving the
required path waits for the executor connection.

```text
go test ./appserver -run Executor -count=1
go vet ./appserver
```

Passed on 2026-10-18.
