# Round 116 · #53963 memory extraction ciphertext budget

Rust source: `0d58012ec2`.

## Landed

- Memory response-item sanitization now removes top-level
  `encrypted_content`, matching Rust's
  `clear_encrypted_content()` before evidence selection and byte accounting.
- Legacy `response_item` rollout records clear their raw payload after the item
  is normalized, so unsanitized ciphertext does not survive in the in-memory
  record.
- The 8 MiB bounded reader now charges sanitized evidence rather than assistant
  ciphertext. Large encrypted blocks can no longer evict earlier readable
  memory evidence.
- Added regression coverage for assistant messages, function/search calls, and
  developer-message exclusion, plus a rollout whose combined ciphertext exceeds
  the reader budget while each record stays below the per-record limit.

## Verification

```text
go test ./memories -run 'Ciphertext|BoundedRolloutInput' -count=1  # ok
go test ./memories -count=1                                         # ok
go vet ./memories                                                   # ok
go build ./...                                                      # ok
```
