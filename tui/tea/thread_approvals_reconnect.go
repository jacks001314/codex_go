package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// Rust #58720 keeps its per-thread event stores across a reconnection and
// re-derives the pending-approval set from them
// (app/reconnect.rs:551 -> app/thread_routing.rs:1006
// refresh_pending_thread_approvals). Go has no per-thread store and no
// persistent connection: every remote client is scoped to one turn, so the
// broker that awaited an approval dies with the transport and the queue has to
// be reconstructed from the server.
//
// The app-server is the other half of the Rust design: resuming a thread
// replays the server requests it still holds for that thread
// (app-server/src/outgoing_message.rs:525
// replay_requests_to_connection_for_thread, Go counterpart
// appserver/runtime_router.go replayPendingServerRequestsForThread ->
// ServerRequestBroker.ReplayThreadToConnection). Rebuilding therefore means
// retiring the dead queue and asking the host to resume the threads that were
// waiting; the replayed requests re-enter routeApprovalRequest.

// retireThreadApprovalsForReconnect drops the approvals whose answer channels
// belonged to the retired connection and reports the threads that must be
// reattached so the server can replay what is still pending.
//
// Which approvals are rebuilt is decided by the server, not by this queue: a
// request the operator already answered is no longer held by the server and is
// never replayed, and an archived thread is not reattached at all (its pending
// prompt is retired with it).
func (m *Model) retireThreadApprovalsForReconnect() []string {
	if m == nil {
		return nil
	}
	threadIDs := make([]string, 0, len(m.pendingApprovals)+1)
	seen := make(map[string]bool, len(m.pendingApprovals)+1)
	record := func(threadID string) {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" || seen[threadID] || m.agentThreadArchived(threadID) {
			return
		}
		seen[threadID] = true
		threadIDs = append(threadIDs, threadID)
	}
	if m.modal != nil && m.modal.kind == ModalKindApproval {
		record(m.modal.threadID)
	}
	for _, request := range m.pendingApprovals {
		record(request.ThreadID)
	}
	if m.modal != nil && m.modal.kind == ModalKindApproval {
		// Keeping the popup would offer a decision the retired broker can never
		// deliver; Rust rebuilds it only after the server replays the request.
		m.modal = nil
		m.addInfoHistoryMessage("The app-server connection was lost while an approval was pending; reconnecting.")
		m.refreshTranscript()
	}
	if len(m.pendingApprovals) > 0 {
		m.pendingApprovals = nil
		m.refreshThreadApprovalViews()
	}
	return threadIDs
}

// reconnectThreadApprovalsCmd asks the host to reattach the waiting threads.
// The replayed requests arrive as ordinary ApprovalRequestMsg values, so they
// flow through routeApprovalRequest and the picker/bottom-pane views rebuild
// themselves.
func (m *Model) reconnectThreadApprovalsCmd(threadIDs []string) bubbletea.Cmd {
	if m == nil || len(threadIDs) == 0 || m.onReconnectThreadApprovals == nil {
		return nil
	}
	handler := m.onReconnectThreadApprovals
	pending := append([]string(nil), threadIDs...)
	return func() bubbletea.Msg {
		return handler(pending)
	}
}

// approvalRequestAlreadyKnown reports whether the request is already queued or
// on screen. Rust keys the pending interactive replay state by request id, so a
// duplicate delivery (or a replay racing a still-visible prompt) resolves to one
// entry and one popup.
func (m *Model) approvalRequestAlreadyKnown(message ApprovalRequestMsg) bool {
	if m == nil {
		return false
	}
	serverRequestID := strings.TrimSpace(message.ServerRequestID)
	id := strings.TrimSpace(message.ID)
	if serverRequestID == "" && id == "" {
		return false
	}
	matches := func(candidateID string, candidateServerRequestID string) bool {
		if serverRequestID != "" && strings.TrimSpace(candidateServerRequestID) == serverRequestID {
			return true
		}
		return id != "" && strings.TrimSpace(candidateID) == id
	}
	if m.modal != nil && m.modal.kind == ModalKindApproval {
		if matches(m.modal.id, m.modal.serverRequestID) {
			return true
		}
	}
	for _, queued := range m.pendingApprovals {
		if matches(queued.ID, queued.ServerRequestID) {
			return true
		}
	}
	return false
}
