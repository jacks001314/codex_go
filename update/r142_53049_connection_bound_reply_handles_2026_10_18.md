# Rust #53049: connection-bound app-server reply handles

Upstream commit: `8c0f7011674145da7e10627769fb221c74fbf7a0`

## Result

Go uses a different request/reply carrier but now enforces the same
connection-bound, single-use reply semantics.

- `ServerRequestBroker` assigns globally unique `server-request-N` IDs. A
  pending entry retains the connection selected by `RequestToConnection`, so
  IDs reused by independent Rust clients cannot select or collide with another
  Go connection.
- Targeted sinks deliver only to the retained connection. `RejectPending`
  rejects requests owned by a closing connection without ending requests owned
  by other connections.
- Replies resolve the broker's pending completion channel directly; there is
  no later UI/client selection that can redirect a reply.
- Pending requests now use an atomic claim. Concurrent duplicate responses, a
  response racing cancellation, and response racing connection closure have a
  single winner. Later resolutions return `false` instead of submitting the
  reply again or blocking on the completion buffer.
- Cancellation and connection closure claim and remove the pending entry before
  notifying the resolved callback, so callback and status release run at most
  once.

Go does not expose the Rust `AppServerReplyHandle` type because its app-server
request API is already synchronous and owns the completion channel. The broker
entry is the equivalent retained reply capability.

## Verification

```text
go test ./appserver -run 'TestServerRequestBroker|TestConnectionServerRequestSink|TestRuntimeRouter.*Connection' -count=1
```

Passed on 2026-10-18.
