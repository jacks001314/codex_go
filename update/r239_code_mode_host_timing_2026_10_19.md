# Code Mode host request durations

Rust upstream: `48e22a5fa08b03a9d8acc6a6577dd334c5319446` -
`Report code mode host request durations (#41452)`.

## Alignment

Rust measures execute, wait and terminate operations inside the Code Mode host.
That duration travels through stdio/gRPC and becomes the model-visible wall
time, so client response-read delays and idle time are excluded. It also emits
a structured `codex.code_mode.host_timing` record.

Go already carried `code_mode_host_duration_ns` through its runtime protocol.
This update completes the downstream contract:

- normal model-visible Code Mode output uses the host duration instead of the
  outer client elapsed time;
- the duration remains in the tool output data and completed Code Mode call
  observation;
- app-server emits `codex.code_mode.host_timing` with conversation, turn, call,
  cell and tool fields before the existing dynamic tool-call analytics event.

The experimental overhead display still receives both total and host durations,
so its code-mode/overhead breakdown remains meaningful.

## Verification

```text
go test ./tool -run 'HostTiming|HostDuration|CodeModeRemoteHostDurationFeedsTheOverheadHeader' -count=1
go test ./appserver -run 'CodeModeHostTiming' -count=1
go test ./codemode -run 'RuntimeResponseHostDuration|HostDuration' -count=1
go test ./parity -count=1
```
