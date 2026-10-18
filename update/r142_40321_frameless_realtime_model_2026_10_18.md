# Rust #40321: update the frameless realtime default model

Upstream commit: `068c49f075cf287a1fe7d1ee36cf005efac922e7`

## Result

Fixed.

V3 frameless realtime sessions still defaulted to
`gpt-live-1-boulder-alpha`. `StartParams.Normalized` now defaults them to
`gpt-live-1-codex`, while an explicit `StartParams.Model` continues to
override the default.

`TestStartParamsNormalizedFramelessRealtimeModelLikeRust` covers both branches,
and the WebSocket V3 integration expectation now asserts the new model in the
live-session URL.

## Verification

```text
go test ./realtime -count=1
go vet ./realtime
```

Passed on 2026-10-18.
