# Rust #52686: opt-in retention for turn tool outputs

Upstream commit: `45311351c218f62ea81b72f16e9b6667b572059d`

## Result

Equivalent implementation was already present.

- `TurnToolOutput` carries an optional `retain` annotation.
- Retained standalone tool outputs are recorded as client-authored harness
  metadata and survive ordinary non-retained history filtering.
- Retention remains opt-in and does not change default tool-output handling.

## Verification

```text
go test ./appserver -run ToolOutputRetention -count=1
go test ./appserver -run Schema -count=1
```

Passed on 2026-10-18.
