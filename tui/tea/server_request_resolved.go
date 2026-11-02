package tea

import (
	"strings"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// ServerRequestResolvedMsg reports that an app-server request was resolved by
// another path, so its visible prompt must be removed (Rust #55682
// dismiss_app_server_request). It is distinct from the response we send after
// the user answers a modal.
type ServerRequestResolvedMsg struct {
	ThreadID  string
	RequestID string
}

// applyServerRequestResolved dismisses only a server-request modal whose id
// matches the resolved request. Local settings and picker views are never
// affected, even if an unrelated request happens to share their id. Closing
// the last visible modal can settle a permissions hold, so queued prompts
// resume only after the covering request is gone.
func (m *Model) applyServerRequestResolved(message ServerRequestResolvedMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	requestID := strings.TrimSpace(message.RequestID)
	if requestID == "" {
		return nil
	}
	if m.removeQueuedApproval(requestID) {
		m.refreshThreadApprovalViews()
		return nil
	}
	if m.modal == nil {
		return nil
	}
	if strings.TrimSpace(m.modal.serverRequestID) != requestID && strings.TrimSpace(m.modal.id) != requestID {
		return nil
	}
	switch m.modal.kind {
	case ModalKindApproval, ModalKindElicitation, ModalKindUserInput:
	default:
		return nil
	}
	m.modal = nil
	m.refreshThreadApprovalViews()
	settle := m.settlePermissionPopup()
	next := m.openNextThreadApproval()
	if settle != nil && next != nil {
		return bubbletea.Batch(settle, next)
	}
	if settle != nil {
		return settle
	}
	return next
}
