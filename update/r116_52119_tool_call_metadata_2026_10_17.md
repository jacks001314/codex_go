# Round 116 · #52119 originating tool call metadata

Rust source: `9b738582b1`.

## Landed

- `exec_command` launches now export the originating call ID as
  `CODEX_TOOL_CALL_ID`; a later `write_stdin` keeps the launch's original ID
  because it writes to the existing process instead of restarting it.
- Executor metadata carries `CODEX_THREAD_ID` and `CODEX_TOOL_CALL_ID` through
  local shell requests and the exec-server wire protocol.
- Metadata is applied after shell environment policies, command env overlays,
  and shell-snapshot replay, so runtime-selected values always win.
- Metadata presence clears an omitted thread ID. Without metadata the inherited
  thread context is preserved while a stale call ID is removed. An empty call ID
  is preserved; a NUL-containing call ID is omitted.
- Windows environment names are treated case-insensitively when replacing
  metadata, removing every case variant before setting the canonical name.
- Snapshot capture excludes both execution IDs, and the snapshot replay wrapper
  restores the launch's call ID after sourcing a snapshot.
- Remote unified-exec forwards metadata to exec-server, covering pipe, TTY, and
  sandboxed process starts through the shared `childEnv` path. The labels are
  documented as correlation metadata, not authorization.

## Verification

```text
go test ./envutil ./shell -run 'ExecutionMetadata|SnapshotCapture|ParseCapturedSnapshot' -count=1  # ok
go test ./execserver -run 'ExecutionMetadata|ChildEnv|MetadataWireShape' -count=1               # ok
go test ./tool -run 'SnapshotBuilderCaptureShape|OriginatingToolCallID|RemoteExposesOriginating' -count=1  # ok
go test ./envutil ./shell ./execserver ./tool -count=1
# only the known Windows exec-server path and login-shell PATH baseline failures

go vet ./envutil ./shell ./execserver ./tool  # ok
go build ./...                                # ok
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c for all four packages  # ok
```

Regression coverage includes metadata precedence over policy and overlays,
missing/empty/NUL IDs, Windows case variants, snapshot capture exclusion and
replay restoration, local and remote process environments, and preservation of
the originating call ID across later input.
