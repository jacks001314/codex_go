# Context window IDs in response metadata

Rust upstream: `27c05a52e0cdf93ef7db5a96ccaf88d6b9095472` -
`Include context window IDs in response metadata (#39847)`.

## Alignment

Go already carries both a monotonic conversation `window_id` and a UUID-shaped
`context_window_id` in Responses request metadata:

- turn and Code Mode requests read the active thread window;
- compaction requests carry the captured compaction window;
- a context-window reset or compaction advances the ID;
- resume restores the persisted `auto_compact_context_window_id`;
- client-supplied reserved metadata cannot override the server identity.

The existing unit and integration tests cover stability, reset/advance,
persistence across resume, and compaction metadata propagation.

## Verification

```text
go test ./turn -run 'ResponsesClientMetadata|ContextWindow' -count=1
go test ./appserver -run 'ContextWindowID|ContextWindowRollOver|CompactResponsesClientMetadata' -count=1
go test ./parity -count=1
```
