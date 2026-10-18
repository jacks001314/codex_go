# Rust #40648: atomic automatic turn admission with settings updates

Upstream commit: `ba6cf9c69277caec51a4c12c5b7401a9920930e0`

## Result

Implemented a shared turn-admission transaction boundary.

- `RuntimeRouter.handleTurnStart` and `thread/settings/update` now acquire the
  same per-router admission mutex.
- An automatic turn therefore validates and registers against one stable
  settings snapshot; a settings commit cannot interleave between admission
  checks and turn reservation.
- Settings updates remain serialized with each other and with new turns, while
  live-turn settings patches continue to use their existing turn-scoped path.
- Rejected admission and failed settings validation return before any turn is
  registered, preserving the existing no-partial-commit behavior.

## Verification

```text
go test ./appserver -run 'TurnStart|ThreadSettings|Automatic|Spawn|AgentMessageWake|Queue' -count=1
go vet ./appserver
```

Passed on 2026-10-18.
