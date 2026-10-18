# Rust #49686: remote message board notifications on active turns

Upstream commit: `49be2c7ab029434dc9545cf85a7a41fd5d3beb1a`

## Result

Equivalent implementation was already present.

- `RuntimeRouter.installMessageBoardNotifications` opens a receiver only for a
  provisioned remote board and skips Guardian turns.
- The first connection attempt releases inference; failed setup leaves the same
  turn-owned receiver retrying after one second.
- Notices are validated for recipient/turn identity and preview bounds;
  individual invalid notices are skipped while transport failures reconnect.
- Accepted previews are delivered through the message-board host, which keeps
  delivery best effort and avoids waking finished turns.
- The returned stop function is wired to turn cleanup and cancels both the
  stream and any scheduled reconnect.

## Verification

```text
go test ./appserver -run 'MessageBoardNotification|RemoteMessageBoard|MessageBoardHostNotify' -count=1
```

Passed on 2026-10-18.
