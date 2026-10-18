# Rust #51808: preserve MCP user verification failure reasons

Upstream commit: `b38942ce7ce73a5cee7981f1f1b6f1a3d2c5a5c0`

## Result

Equivalent implementation was already present.

- MCP user verification carries a closed diagnostic reason vocabulary in
  bounded `openai/userVerificationReason` metadata.
- Unknown or malformed reasons are discarded/normalized rather than propagated
  as arbitrary text.
- App-server response errors, closed response channels, interrupts, and
  unavailable approval paths preserve their specific reasons.

## Verification

```text
go test ./mcp -run UserVerification -count=1
go test ./appserver -run UserVerification -count=1
```

Passed on 2026-10-18.
