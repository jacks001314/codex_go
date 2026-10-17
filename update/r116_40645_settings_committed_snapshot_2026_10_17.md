# Round 116 · #40645 settings operations keep their committed snapshot

Rust source: `4e883ce36e`.

## Landed

- Added `ThreadExtraService.UpdateSettingsWithCommit`, which applies a settings
  write under the service lock and returns the exact `Settings` snapshot that
  write committed. `UpdateSettings` keeps its existing wire response (`{}`) by
  delegating and discarding the snapshot.
- Added `RuntimeRouter.commitThreadSettingsUpdate` (Go's
  `SessionSettingsCommit` path): standalone `thread/settings/update` and
  turn-start/thread-resume settings writes now share one commit → persist →
  notify path. The `thread/settings/updated` notification carries the committed
  snapshot instead of re-reading `ThreadExtras.Settings` after the commit.
- `persistThreadSettingsUpdate` takes that committed snapshot and uses its
  approval policy, cwd, and collaboration mode for the persisted
  `thread_settings_applied` event. A concurrent settings write that lands
  between commit and persistence can no longer change what this operation
  reports or applies.
- The thread-resume collaboration-mode restore persists its own committed
  snapshot through the same path.

## Verification

```text
go test ./appserver -run 'TestUpdateSettingsWithCommitKeepsItsSnapshotLikeRust|TestPersistThreadSettingsUsesCommittedSnapshotLikeRust' -count=1  # ok
go test ./appserver -run 'Settings|ThreadExtra|DisabledPlugin|CollaborationMode' -count=1
# only the three known platform newline-fixture collaboration failures
go vet ./appserver  # ok
go build ./...  # ok
```

The regression test commits `never`/`/committed`, applies a later
`on-request`/`/later` write, runs the first operation's post-commit
persistence, and verifies the rollout's `thread_settings_applied` snapshot
still carries `never` and `/committed`.
