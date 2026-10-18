# Turn cost OTEL metric

Rust upstream: `2497518490a4cfc67a0c7a5c099ef632fdc2ed78` -
`Export turn cost as an OTEL metric (#40488)`.

## Alignment

Go's turn-cost worker exports:

```text
codex.turn.cost_microusd
```

The counter only emits a settled, visible estimate after every tracked
response for the turn has settled. Tags include:

- `turn.id`
- `conversation.id`
- `turn.interrupted`
- optional `speed`
- optional `reasoning_effort`

The tracker records each completed response once and applies Rust's bounded
turn/response retention. Existing tests also cover missing estimates, partial
settlement, interruption, and response-cap behavior.

## Verification

```text
go test ./appserver -run 'TurnCost' -count=1
go test ./parity -count=1
```
