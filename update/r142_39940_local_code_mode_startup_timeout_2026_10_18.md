# Rust #39940 audit: local Code Mode host startup timeout

Upstream commit: `275ef855fac71203ffff904d20c860a7717d6e8a`

The Go port already had the requested behavior:

- `codemode/process_provider.go` defines a 30-second
  `localHostStartupTimeout` for locally spawned hosts.
- The process provider applies it to the connection handshake context.
- WebSocket-owned connections retain their separate shorter handshake timeout.

Added `TestLocalCodeModeHostStartupTimeoutLikeRust` to lock the 30-second value.

```text
go test ./codemode -run LocalCodeModeHostStartupTimeout -count=1
go test ./codemode -run ProcessProvider -count=1
```

Passed on 2026-10-18.
