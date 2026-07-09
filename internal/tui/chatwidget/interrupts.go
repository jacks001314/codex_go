package chatwidget

type QueuedInterruptKind string

const (
	QueuedInterruptExecApproval       QueuedInterruptKind = "exec_approval"
	QueuedInterruptApplyPatchApproval QueuedInterruptKind = "apply_patch_approval"
	QueuedInterruptElicitation        QueuedInterruptKind = "elicitation"
	QueuedInterruptRequestPermissions QueuedInterruptKind = "request_permissions"
	QueuedInterruptRequestUserInput   QueuedInterruptKind = "request_user_input"
	QueuedInterruptItemStarted        QueuedInterruptKind = "item_started"
	QueuedInterruptItemCompleted      QueuedInterruptKind = "item_completed"
)

type QueuedInterrupt struct {
	Kind       QueuedInterruptKind
	ID         string
	ApprovalID string
	ServerName string
	RequestID  string
	Payload    any
}

type ResolvedPrompt struct {
	Kind       QueuedInterruptKind
	ID         string
	ServerName string
	RequestID  string
}

type InterruptManager struct {
	queue []QueuedInterrupt
}

func NewInterruptManager() *InterruptManager {
	return &InterruptManager{}
}

func (m *InterruptManager) IsEmpty() bool {
	return m == nil || len(m.queue) == 0
}

func (m *InterruptManager) Len() int {
	if m == nil {
		return 0
	}
	return len(m.queue)
}

func (m *InterruptManager) Push(interrupt QueuedInterrupt) {
	if m == nil {
		return
	}
	m.queue = append(m.queue, interrupt)
}

func (m *InterruptManager) PushExecApproval(callID string, approvalID string, payload any) {
	m.Push(QueuedInterrupt{Kind: QueuedInterruptExecApproval, ID: callID, ApprovalID: approvalID, Payload: payload})
}

func (m *InterruptManager) PushApplyPatchApproval(callID string, payload any) {
	m.Push(QueuedInterrupt{Kind: QueuedInterruptApplyPatchApproval, ID: callID, Payload: payload})
}

func (m *InterruptManager) PushElicitation(serverName string, requestID string, payload any) {
	m.Push(QueuedInterrupt{Kind: QueuedInterruptElicitation, ServerName: serverName, RequestID: requestID, Payload: payload})
}

func (m *InterruptManager) PushRequestPermissions(callID string, payload any) {
	m.Push(QueuedInterrupt{Kind: QueuedInterruptRequestPermissions, ID: callID, Payload: payload})
}

func (m *InterruptManager) PushUserInput(callID string, payload any) {
	m.Push(QueuedInterrupt{Kind: QueuedInterruptRequestUserInput, ID: callID, Payload: payload})
}

func (m *InterruptManager) PushItemStarted(id string, payload any) {
	m.Push(QueuedInterrupt{Kind: QueuedInterruptItemStarted, ID: id, Payload: payload})
}

func (m *InterruptManager) PushItemCompleted(id string, payload any) {
	m.Push(QueuedInterrupt{Kind: QueuedInterruptItemCompleted, ID: id, Payload: payload})
}

func (m *InterruptManager) Pop() (QueuedInterrupt, bool) {
	if m == nil || len(m.queue) == 0 {
		return QueuedInterrupt{}, false
	}
	item := m.queue[0]
	m.queue = m.queue[1:]
	return item, true
}

func (m *InterruptManager) FlushAll() []QueuedInterrupt {
	if m == nil || len(m.queue) == 0 {
		return nil
	}
	out := append([]QueuedInterrupt(nil), m.queue...)
	m.queue = nil
	return out
}

func (m *InterruptManager) RemoveResolvedPrompt(resolved ResolvedPrompt) bool {
	if m == nil || len(m.queue) == 0 {
		return false
	}
	out := m.queue[:0]
	removed := false
	for _, item := range m.queue {
		if item.MatchesResolvedPrompt(resolved) {
			removed = true
			continue
		}
		out = append(out, item)
	}
	m.queue = out
	return removed
}

func (q QueuedInterrupt) MatchesResolvedPrompt(resolved ResolvedPrompt) bool {
	if q.Kind != resolved.Kind {
		return false
	}
	switch q.Kind {
	case QueuedInterruptExecApproval:
		if q.ApprovalID != "" {
			return nonEmptyEqual(q.ApprovalID, resolved.ID)
		}
		return nonEmptyEqual(q.ID, resolved.ID)
	case QueuedInterruptElicitation:
		return nonEmptyEqual(q.ServerName, resolved.ServerName) && nonEmptyEqual(q.RequestID, resolved.RequestID)
	case QueuedInterruptItemStarted, QueuedInterruptItemCompleted:
		return false
	default:
		return nonEmptyEqual(q.ID, resolved.ID)
	}
}

func nonEmptyEqual(left string, right string) bool {
	return left != "" && right != "" && left == right
}
