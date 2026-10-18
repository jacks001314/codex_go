# Rust #43458: centralize Guardian context mode and checkpoint policy

Upstream commit: `8260619cb6dbb23fc43fd9b8567e98fb103aebd4`

## Result

Equivalent centralized carrier.

- `state.ContextProfile` owns the synchronous and asynchronous Guardian context
  modes, transcript retention/truncation budgets, and original entry numbering.
- Appserver review planning applies the resolved profile, and the parent
  compaction checkpoint policy is handled by the reviewer session reset/reuse
  path.

## Verification

```text
go test ./state -run 'GuardianContextProfile' -count=1
```

Passed on 2026-10-18.
