# Round 116 · #40280 retained image budget in remote compaction

Rust source: `6677fd827d`.

## Landed

- `compact.retentionPolicy` gained `imageBudget`, resolved from the
  `compaction_image_budget` feature (later promoted to stable/default-on
  upstream). With the feature enabled, retained messages charge their content
  (including input images) instead of text only; disabled behavior keeps the
  text-only accounting, and retained tool outputs keep their serialized
  estimate.
- Added `truncateRetainedMessageWithImages`, the Go analogue of
  `truncate_message_to_token_budget`: content parts are walked newest-first,
  each input image is kept or dropped whole with its adjacent `<image>` /
  `</image>` labels, text uses the normal token truncation, and audio/other
  media stay uncharged.
- When an image-bearing message at the truncation boundary exceeds the budget,
  the walk stops and the remaining budget is not backfilled with older
  messages. `content_item_kinds` is rewritten for the surviving parts.
- Costs use the existing `compactImageReferenceBytes` estimator (resized
  estimate for inline images; original-detail file references use the maximum
  patch count), matching Rust's `estimate_image_bytes`.

## Verification

```text
go test ./compact -run 'TestRemoteRetention|TestResolveRetentionPolicyReadsImageBudget' -count=1  # ok
go test ./compact -count=1  # ok
go test ./appserver -run 'Compact|Compaction' -count=1  # ok
go vet ./compact  # ok
go build ./...  # ok
```

Coverage: feature-on image charging versus feature-off text-only accounting,
atomic image/label retention, no backfill after an oversized boundary image,
and feature-key resolution.
