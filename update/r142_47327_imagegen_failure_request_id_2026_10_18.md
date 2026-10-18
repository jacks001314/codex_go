# Rust #47327: preserve ImageGen request IDs on failure

Upstream commit: `6cfe29984965abfb5f7a5f86048f655d45d028a7`

## Result

Equivalent implementation was already present.

- The image handler reads `x-codex-imagegen-request-id` from both successful
  responses and HTTP/decode failures.
- Failure errors carry the request ID through typed wrappers, and the handler
  reports it through `RequestIDObserver`.
- The app-server retains the ID by thread and call ID until the failed
  `codex_image_generation_event` is emitted, then removes it.
- The ID remains analytics-only: it is absent from serialized image items and
  rollout persistence, matching Rust's `#[serde(skip)]`.

## Verification

```text
go test ./turn -run 'ImageGeneration.*RequestID|ImageGeneration' -count=1
go test ./appserver -run 'ImageGeneration.*Analytics|Imagegen|ImageGeneration' -count=1
go vet ./turn ./appserver
```

Passed on 2026-10-18.
