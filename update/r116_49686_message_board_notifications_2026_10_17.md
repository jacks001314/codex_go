# Round 116 · #49686 remote message board notifications for active turns

Rust source: `49be2c7ab0`.

## Landed

- Added `RuntimeRouter.installMessageBoardNotifications`, the Go analogue of
  `agent-message-board-client::install_notifications`. When the thread's handle
  is a remote board, it starts one turn-owned receiver and returns a stop
  function; local and in-memory boards stay no-op.
- The first `Notifications` connection attempt completes before the turn starts
  inference (success or failure). A failed or disconnected stream is reopened
  after Rust's one-second pause while the turn remains active, and missed
  previews are not replayed.
- Every accepted notice is delivered through the existing `messageBoardHost`,
  which resolves the recipient, rejects foreign-tree recipients, checks the
  active turn and enqueues the preview with `EnqueueIfAcceptingDelivery`.
  Stale, finished, or canceled turns are skipped instead of waking an idle
  agent. Receiver-side validation in `agentboard` still rejects malformed
  frames, mismatched recipients/turns, and previews above the bound.
- `runTurnRuntime` installs the receiver after `turn/started` and defers its
  cancellation, so stop, abort, error, and every early return end the stream.
  Guardian turns are exempt, matching Rust's regular-task start phase.

## Verification

```text
go test ./appserver -run 'TestMessageBoardNotificationReceiverRetriesAndDeliversLikeRust|TestInstallMessageBoardNotificationsWithoutRemoteBoardLikeRust' -count=1  # ok
go test ./appserver -run 'MessageBoard|Board' -count=1  # ok
go test ./agentboard -count=1  # ok
go vet ./appserver  # ok
go build ./...  # ok
```

The regression test fails the first notification connection, verifies the
ready signal still releases startup, waits for the one-second retry, checks the
delivered preview, and confirms cancellation ends the receiver.
