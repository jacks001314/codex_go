# JSON byte-count allocation refactor

Rust upstream: `46aa019e805352c9d7fd9a740cbf7f8b9aeb162d` -
`Count serialized JSON bytes without allocating strings (#40604)`.

## Verdict

Not applicable as a separate Go carrier.

The upstream change replaces `serde_json::to_string(...).len()` with a counting
writer to avoid retaining a serialized string. Go's equivalent budget paths use
`encoding/json` and byte-length accounting; their observable limits, retained
call bounds and omission metadata are already covered by the executed-tool-call
and outgoing-message tests.

There is no public API, protocol field, user-visible output or persistence shape
introduced by this allocation-only refactor.

## Verification

```text
go test ./parity -count=1
```
