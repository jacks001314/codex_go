# Realtime connection thread source

Rust upstream: `18b9e7fd9e3f6670cc4f300338e44050b2c301e4` -
`Include thread source in realtime connection metadata (#41250)`.

## Alignment

Realtime voice calls may span zero or many backing turns, so Rust sends only the
saved thread source in `x-codex-turn-metadata` for WebSocket, existing-call and
WebRTC transports.

Go already builds a common header set with `realtimeThreadHeaders`:

- the saved thread source is encoded as `{"thread_source":"..."}`;
- empty sources and values larger than 256 bytes are omitted;
- non-ASCII and control characters are escaped into ASCII JSON;
- the same common headers are merged into both WebSocket and WebRTC call
  headers, covering existing-call sideband connections too.

The current regression tests cover source propagation, the byte limit, absent
sources, and header-safe escaping.

## Verification

```text
go test ./appserver -run 'RealtimeThreadHeadersIncludeThreadSource|ASCIICJSONHeaderStringEscapesNonASCII' -count=1
go test ./parity -count=1
```
