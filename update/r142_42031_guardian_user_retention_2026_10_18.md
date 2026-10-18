# Rust #42031: share Guardian user-message retention logic

Upstream commit: `82099786163f3c05facf09078136679e18b64279`

## Result

Implemented.

- `state.GuardianRootMessage` renders every nonempty line with preserving
  indentation.
- `state.GuardianSenderMessages` centralizes sender retention: up to three
  recent user messages with preceding assistant context, a 900-byte per-exchange
  budget, and the persisted-history, no-messages, and missing-evidence notices.
- Omission/truncation behavior is covered by the Guardian root/sender message
  tests.

## Verification

```text
go test ./state -run 'GuardianRoot|GuardianSender' -count=1
```

Passed on 2026-10-18.
