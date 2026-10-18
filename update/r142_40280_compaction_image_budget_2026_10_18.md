# #40280 Budget retained images during remote compaction

## Upstream change

Rust adds the opt-in `compaction_image_budget` feature. Retained remote
compaction messages charge images with the existing image estimate, keep image
labels atomic at the truncation boundary, and stop backfilling older messages
when an image does not fit.

## Go carriers

- `compact.CompactionImageBudgetKey` is the registered feature key.
- `retainedMessagesForRemoteCompaction` switches from text-only accounting to
  `EstimateItemTokens` when the policy enables the image budget.
- `truncateRetainedMessageWithImages` treats each image plus adjacent
  `<image>`/`</image>` labels as one group and keeps only complete later
  groups within the remaining budget.
- A non-fitting boundary image sets the remaining budget to zero, preventing
  older messages from filling the unused space.
- Client-authored developer messages retain their serialized estimate, and the
  disabled path keeps existing text behavior.

## Verification

- `go test ./compact -run 'RemoteRetention|ImageBudget|ResolveRetentionPolicy' -count=1`
