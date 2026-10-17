# Round 116 · #53672 Apps tools during startup recovery

Rust source: `4c1cd8bbcc`.

## Landed

- `mcp/api.go` no longer clears an already published Codex Apps tool catalog
  when startup recovery fails. The status still becomes `failed` and carries the
  startup error, but the retained Apps tools remain available to
  `CaptureBinding`, so the next invocation can use the discovered function while
  the HTTP client reinitializes/retries the connection.
- Ordinary MCP servers keep the old fail-closed behavior and clear tools on a
  failed inventory read.

## Verification

```text
go test ./mcp -run 'AppsStartupFailureRetainsCachedTools|OrdinaryStartupFailureStillClearsTools' -count=1
# ok (each case exercises a real HTTP startup failure)

go test ./mcp -count=1  # ok
go vet ./mcp            # ok
go build ./...          # ok
```

RC: removing the Apps exemption clears the cached `calendar_list_events` tool and
fails `TestAppsStartupFailureRetainsCachedToolsLikeRust`.
