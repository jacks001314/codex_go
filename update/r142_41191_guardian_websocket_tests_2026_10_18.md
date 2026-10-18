# #41191 Stabilize Guardian WebSocket tests

## Upstream change

The Rust tests wait for Guardian prewarming to populate the idle connection
pool, use two initial test connections instead of eight, and register for
request-log notifications before checking the log to avoid a lost wakeup.

## Go status

Not applicable by architecture.

The change is confined to Rust test synchronization and a test-only connection
count. Go's Guardian tests use buffered notification/event channels and their
own wait helpers; no production connection-pool behavior is affected.

## Verification

- `go test ./appserver -run 'Guardian' -count=1`
