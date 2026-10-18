# Code Mode host startup prewarm

Rust upstream: `4b24638e1c6dc031a5900bccdced4ee8d81635fa` -
`Prewarm code-mode host connections at session startup (#40678)`.

## Alignment

When `features.code_mode_prewarm` is enabled, Go now starts a best-effort
background prewarm for the thread's shared Code Mode host during thread-start
processing.

- process, stdio gRPC and WebSocket providers expose a `Prewarm` connection
  path;
- `CodeModeRuntime` forwards prewarm to the configured provider;
- app-server resolves the feature from the new thread's effective config and
  does not block thread start;
- prewarm failure is debug-logged and leaves the first turn to connect lazily;
- server shutdown remains bounded by the existing startup-prewarm timeout.

## Verification

```text
go test ./appserver -run 'ThreadStartPrewarmsCodeModeHost|SessionStartupPrewarm|CodeModeHost' -count=1
go test ./tool -run 'CodeModeRuntimePrewarms' -count=1
go test ./codemode -run 'Prewarm|Provider' -count=1
go test ./parity -count=1
```
