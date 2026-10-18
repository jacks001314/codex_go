# OAuth diagnostics and Guardian cached-score audit

Audited upstream commits:

- `#46300` Centralize OAuth login and refresh handling with safer diagnostics
- `#46245` Publish Guardian cached scores and coverage atomically

## Verdicts

- `#46300` complete: Go's auth package centralizes refresh reasons, unauthorized
  recovery steps, redacted diagnostics, PKCE/authorization handling, and
  provider-owned recovery. App-server OAuth notifications and MCP issuer
  validation use the same paths.
- `#46245` not applicable: Go has no Guardian v2 async scorer, cached-score
  approval path, or score-coverage state; synchronous Guardian reviews are the
  only carrier.

## Verification

```text
go test ./auth -run 'OAuth|Refresh|Recovery' -count=1
go test ./appserver -run OAuth -count=1
```
