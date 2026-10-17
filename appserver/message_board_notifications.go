package appserver

import (
	"context"
	"strings"
	"time"

	"codex_go/agentboard"
	"codex_go/turn"
)

// messageBoardNotificationReconnectDelay mirrors Rust's one-second pause before
// reopening a notification stream (the service does not replay missed previews).
const messageBoardNotificationReconnectDelay = time.Second

// installMessageBoardNotifications starts the turn-owned remote board receiver
// (Rust #49686 install_notifications). The first connection attempt finishes
// before inference, later failures retry in the background, and the returned
// stop function cancels the receiver when the turn stops, aborts, or errors.
// Local and in-memory boards have no live stream.
func (r *RuntimeRouter) installMessageBoardNotifications(ctx context.Context, threadID, turnID string, params *turn.TurnStartParams) func() {
	noop := func() {}
	if r == nil || guardianTurnStart(params) {
		return noop
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return noop
	}
	r.messageBoardHandlesMu.Lock()
	handle := r.messageBoardHandles[threadID]
	r.messageBoardHandlesMu.Unlock()
	remote, ok := handle.(*agentboard.RemoteBoard)
	if !ok || remote == nil {
		return noop
	}
	tree, _ := r.runtimeAgentIdentity(threadID)
	host := &messageBoardHost{router: r, tree: tree, caller: threadID}
	receiverCtx, cancel := context.WithCancel(ctx)
	ready := make(chan struct{})
	go runMessageBoardNotificationReceiver(receiverCtx, remote, host, threadID, turnID, ready)
	select {
	case <-ready:
		return cancel
	case <-ctx.Done():
		cancel()
		return noop
	}
}

// runMessageBoardNotificationReceiver owns one active turn's stream. Each
// notice is delivered through the board host so stale or finished turns are
// skipped instead of being woken. The receiver reconnects until ctx ends.
func runMessageBoardNotificationReceiver(
	ctx context.Context,
	board *agentboard.RemoteBoard,
	host agentboard.Host,
	caller string,
	turnID string,
	ready chan<- struct{},
) {
	firstAttempt := true
	markReady := func() {
		if firstAttempt && ready != nil {
			firstAttempt = false
			close(ready)
		}
	}
	for ctx.Err() == nil {
		receiver, err := board.Notifications(ctx, caller, turnID)
		markReady()
		if err == nil {
			for ctx.Err() == nil {
				notice, nextErr := receiver.Next(ctx)
				if nextErr != nil || notice == nil {
					break
				}
				// Delivery is best effort: a skipped or failed preview must not
				// tear down the stream for later notices.
				_, _ = host.Notify(ctx, caller, notice.Post)
			}
			receiver.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(messageBoardNotificationReconnectDelay):
		}
	}
}
