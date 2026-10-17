# Round 116 · #53369 unavailable message board

Rust source: `6c372fbef4`.

## Landed

- Removed the turn-construction rejection that required an advertised
  `collaboration.post` tool when `disable_direct_message` was enabled.
- Turns now continue when the message board is unavailable, a legacy agent is
  selected, or the session is ephemeral without an in-memory board.
- Missing `post`, `send_message`, and `followup_task` calls reach the model as
  the existing unsupported-tool error instead of failing the whole turn.
- No board database is created when the board is unavailable.

## Verification

```text
go test ./appserver -run DisableDirectMessageAllowsUnavailableBoard -count=1  # ok
go test ./appserver -run 'DisableDirectMessage|MessageBoard' -count=1        # ok
go vet ./appserver                                                           # ok
go build ./...                                                               # ok
```

The regression test drives `toolRouterForTurn` with V2 `disable_direct_message`
enabled and no board feature, verifies all three board tools are absent,
dispatches `collaboration.post` through the model-facing router error path, and
checks that no local board database was created.
