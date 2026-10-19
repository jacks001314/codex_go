# Allowlists, external MITM CA, cloud plugin catalog, and audit batch

Implemented without intermediate commits per user request:

- `#45711` thread startup tool allowlists.
- `#47132` caller-provided MITM CA support.
- `#46572` turn-start cloud plugin catalog provider/projection.

Also verified complete:

- `#42606` trusted remote exec WebSocket headers.
- `#40710` explicit remote executor refresh/reconnect.
- `#42137` shell snapshot prewarming.
- `#44658` Windows private desktop lifetime.
- `#41933` configured sandbox policy reporting.
- `#41909` executor path-aware permission transforms.
- `#42391` executor path-aware apply_patch authorization.
- `#40443` batched capability-root discovery.
- `#47683` executor capability discovery V2.
- `#44350` / `#43949` thread attachment RPCs and transactional state.
- `#44066` / `#44068` / `#44072` credential broker aliases, destination hints, and shell snapshots.

Unified tests/fixes will run before the final commit.
