# Rust #53691: account for replay caches in encrypted compaction budgets

Upstream commit: `979771476a6dc8cae59dfc9224b91ae0515b04fd`

## Result

Equivalent implementation was already present.

- Encrypted compaction content is charged using replay-cache token length
  rather than the larger serialized ciphertext envelope.
- Content length and serialized bytes use Rust's bounded approximation helpers.
- Guardian parent-compaction reuse applies the same budget.
- Encrypted agent-message compaction estimates use the same approach.

## Verification

```text
go test ./compact -run EncryptedCompactionUsesReplayCacheTokenEstimate -count=1
go test ./appserver -run GuardianParentCompactionUsesEncryptedReplayCacheBudget -count=1
```

Passed on 2026-10-18.
