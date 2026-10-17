# Round 116 · #53691 replay-cache-aware compaction budgets

Rust source: `979771476a`.

## Landed

- `compact.EstimateCompactionContentLength` normalizes encrypted compaction
  payloads at three ciphertext bytes per content byte; the normal four-byte
  token estimate then yields one token per twelve ciphertext bytes.
- `compact.EstimateItemTokens` now uses that estimate for both `compaction` and
  `context_compaction` items, while framing and metadata remain separately
  charged by their serialized-byte share.
- `compact.CompactionItemWireBytes` exposes the complete serialized checkpoint
  size without mutating the checkpoint.
- Guardian parent-compaction reuse now enforces both the configured token budget
  and Rust's independent 100k-token (400k-byte) wire allowance. A checkpoint
  that fits the token estimate still fails closed when the wire payload exceeds
  the hard bound.

## Verification

```text
go test ./compact -run EncryptedCompactionUsesReplayCache -count=1  # ok
go test ./appserver -run 'GuardianParentCompaction|GuardianSessionRunner|Compaction' -count=1  # ok
go test ./compact -count=1  # ok
go build ./...              # ok
go vet ./compact ./appserver
```

RC: bypassing `estimateCompactionItemTokens` lets an 18k-byte encrypted
checkpoint pass a 1k-token parent budget and fails
`TestGuardianParentCompactionUsesEncryptedReplayCacheBudgetLikeRust`.
