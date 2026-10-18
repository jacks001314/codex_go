# File upload retry and Guardian assistant-context audit

Audited upstream commits:

- `#47393` Retry transient OpenAI file blob upload failures
- `#47582` Retain assistant context for Guardian authorization reviews
- `#47143` Extract exec-server CLI startup into a dedicated module

## Verdicts

- `#47393` complete: the Go uploader already implements the 5-minute deadline,
  bounded retries, transient transport/502/503/504 classification, Azure
  Retry-After parsing, stable client request ids, and non-retryable read/status
  failures. `TestOpenAIFileBlobUploadRetryBehaviorLikeRust` covers the behavior.
- `#47582` complete: retained assistant messages are ordered, source-attributed,
  omission-aware, and rendered as untrusted Guardian authorization context,
  including checkpoint/legacy recovery.
- `#47143` not applicable: upstream only extracted Rust CLI startup code into a
  module; Go already keeps exec-server startup in separate packages.

## Verification

```text
go test ./mcp -run 'OpenAIFileBlobUploadRetry' -count=1
go test ./retainedctx -count=1
go test ./state -run 'Guardian|Retained' -count=1
```
