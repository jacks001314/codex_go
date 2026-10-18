# Rust #51892 / #52268: executed tool-call argument completeness

Upstream commits:

- `35a9b704447b7876997ddd8541ccf0cb64c864c7`
- `515c291d875e07faa64e3fa43dc9bbffed8db31f`

## Result

Equivalent implementation was already present.

- `model.ExecutedToolCall.HasCompleteInventory` reports incomplete only when
  calls or tool names were omitted. Truncated arguments alone do not damage the
  inventory marker.
- Message-budget lowering truncates only the serialized argument copy. It does
  not mutate the recorded inventory or clear `tool_calls_complete`.
- The recorder no longer applies Rust's former 8 KiB per-call or 32 KiB
  per-output argument caps. It retains full normalized arguments, including
  large nested Code Mode calls.
- Go does not need the removed Rust normalization helper: full arguments are
  recorded directly, and completeness is evaluated from inventory loss rather
  than argument byte size.

## Verification

```text
go test ./turn -run PreservesLargeArgumentsAndCompleteness -count=1
go test ./turn -run ExecutedToolCall -count=1
```

Passed on 2026-10-18.
