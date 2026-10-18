# #42854 Persist Daybreak preferences in thread metadata

## Upstream change

Rust stores `daybreakEnabled` in thread metadata, exposes it on thread
responses, supports `thread/metadata/update`, preserves it through reads,
resumes, reconciliation, restarts and forks, and keeps it independent of the
per-turn `cyberAccessProgram`.

## Go carriers

- `session.Metadata.DaybreakEnabled` is part of the persisted thread record and
  is deep-cloned by `cloneMetadata`.
- `BuildThread` maps the value to the protocol `thread.daybreakEnabled` field.
- `ThreadMetadataUpdateParams.DaybreakEnabled` is validated and converted to a
  session metadata patch; no access-program field is derived from it.
- `Store.ForkRecord` uses `forkMetadata`, which clones the source metadata, so a
  fork receives an independent copy.
- Thread resume rebuilds the protocol thread from the stored record.

## Regression coverage

Added tests that:

- update Daybreak from `thread/metadata/update`;
- cold-reload the store;
- fork the thread and confirm the preference is copied;
- update the source afterwards and confirm the fork does not change;
- resume through a new router/store and observe the persisted preference;
- persist a metadata-only choice on a thread with no first turn.

## Verification

- `go test ./appserver ./session -run 'Daybreak' -count=1`
