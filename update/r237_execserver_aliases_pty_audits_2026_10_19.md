# Exec-server request bounds, macOS aliases, and PTY audits

Audited upstream commits:

- `#47362` Bound inbound exec-server requests across client transports
- `#47879` Fix macOS system-alias matching in patch permission checks
- `#47617` Route Linux PTY launches through the process setup helper
- `#47612` Launch Linux pipe processes through a fresh setup helper

## Verdicts

- `#47362` complete: Go bounds in-flight inbound requests at 256, de-duplicates
  request ids, enforces a 64 MiB Noise/JSON-RPC frame limit, and covers direct
  websocket, relay, and server-request paths.
- `#47879` complete: patch permission checks use executor path conventions,
  normalize only depth-one macOS system aliases, and test alias plus canonical
  paths, including case-insensitive filesystem variants when available.
- `#47617` / `#47612` not applicable: both upstream changes add Rust
  spawn-helper indirection around PTY/pipe creation. Go uses `os/exec` and
  `creack/pty` directly with explicit process lifecycle management.

## Verification

```text
go test ./execserver -run 'Inbound|ServerRequest|RemoteRelay|Noise' -count=1
go test ./tool -run 'ApplyPatch.*Alias|CaseVariant|SystemAlias' -count=1
go test ./sandbox -run 'Seatbelt|Alias' -count=1
```
