# Renewable EMA HTTP authentication audit

Rust upstream: `ecc78e4cf5 Add renewable EMA HTTP authentication and credential versioning (#49987)`.

## Verdict

Complete. Enterprise OAuth credentials carry refresh tokens, and the HTTP MCP
client renews them under the credential lock: it re-reads the authoritative
snapshot, adopts a concurrent refresh, retires rejected refresh tokens, and
persists the refreshed credential. Enterprise generation files invalidate stale
login/commit attempts after logout or replacement, preventing an older
authorization from being restored.

## Verification

```text
go test ./mcp -run 'Enterprise|Refresh|EMA|OAuth' -count=1
go test ./mcp -count=1
```
