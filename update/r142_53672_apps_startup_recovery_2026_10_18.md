# Rust #53672: keep discovered Apps tools during startup recovery

Upstream commit: `4c1cd8bbccb337c7043817c8e894058b103c9cdd`

## Result

Equivalent implementation was already present.

- A failed Codex Apps startup retains the previously published cached tool
  catalog.
- Binding capture includes those cached Apps tool names.
- Invocations reuse the same cached HTTP/stdio client. Its mutex protects lazy
  initialization, so concurrent callers join one shared recovery instead of
  starting parallel connection attempts.
- Discovery/status reads remain nonblocking during the recovery.
- Ordinary non-Apps servers still clear stale tools after a startup failure.

## Verification

```text
go test ./mcp -run AppsStartup -count=1
```

Passed on 2026-10-18.
