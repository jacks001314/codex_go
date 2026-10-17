# Round 116 · #47327 image generation request IDs on failure

Rust source: `6cfe299849`.

## Landed

- The image-generation handler now reads `x-codex-imagegen-request-id` from the
  Images API response and carries it through HTTP failures, response decoding
  failures, body-read failures, and "no image data" failures.
- Added `ImageGenerationOptions.RequestIDObserver` and an in-process
  `RuntimeRouter` map keyed by thread and call id. The id is delivered to
  analytics when the failed item's `codex_image_generation_event` is emitted,
  then consumed.
- `telemetry.CodexImageGenerationEventParams` gained
  `imagegen_request_id` (omitempty).
- The request id is deliberately kept out of `tool.Output.Data` and
  `ThreadItem` data, matching Rust's `#[serde(skip)]`:
  `ImageGenerationItem::imagegen_request_id` is analytics-only and never
  reaches the client wire shape or rollout persistence.

## Verification

```text
go test ./turn -run 'TestImageGenerationCarriesRequestIDOnFailureLikeRust' -count=1  # ok
go test ./turn -run 'TestImageGeneration' -count=1  # ok
go test ./telemetry -count=1  # ok
go test ./appserver -run 'ImageGeneration' -count=1  # ok
go vet ./turn ./appserver ./telemetry  # ok
go build ./...  # ok
```

Coverage: HTTP 500 with the header, decode failure, empty `data`, a missing
header (no observer call), and an assertion that no `imagegen*` key leaks into
item data; the app-server analytics test asserts the emitted
`imagegen_request_id`.
