package tea

import (
	"strings"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// reconcileBufferedSessionInput consumes receipts independently of transcript
// replay: a read-only or already populated snapshot still confirms delivery.
func (m *Model) reconcileBufferedSessionInput(threadID string, events []protocol.ThreadEvent) {
	var receipts []codextui.Message
	for _, event := range events {
		if event.Type != "item.completed" || !isUserMessageThreadItem(event.Item) {
			continue
		}
		if event.ThreadID != "" && event.ThreadID != threadID {
			continue
		}
		receipts = append(receipts, codextui.Message{Role: codextui.RoleUser, ClientID: event.Item.ClientID})
	}
	m.reconcileConfirmedSessionInput(threadID, receipts)
}

// reconcileConfirmedSessionInput removes submissions the resumed history
// already contains (Rust #59817 App::reconcile_saved_session_input and
// ThreadInputState::reconcile_confirmed_messages). Matching uses the client
// message id, so two submissions with identical text stay distinct: only the
// acknowledged id is dropped. Unconfirmed submitted steers continue to hold
// the unsent queue until a matching receipt arrives.
func (m *Model) reconcileConfirmedSessionInput(threadID string, messages []codextui.Message) {
	if m == nil {
		return
	}
	confirmed := confirmedUserMessageIDs(messages)
	if len(confirmed) == 0 {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || strings.TrimSpace(m.pendingSteersThreadID) == threadID {
		m.pendingSteers = withoutConfirmedPendingSteers(m.pendingSteers, confirmed)
	}
	if parked := m.threadPendingSteers[threadID]; len(parked) > 0 {
		parked = withoutConfirmedPendingSteers(parked, confirmed)
		if len(parked) == 0 {
			delete(m.threadPendingSteers, threadID)
		} else {
			m.threadPendingSteers[threadID] = parked
		}
	}
	m.queued = withoutConfirmedQueuedSubmissions(m.queued, confirmed)
	m.rejectedSteers = withoutConfirmedQueuedSubmissions(m.rejectedSteers, confirmed)
}

// confirmedUserMessageIDs mirrors Rust's confirmed_user_message_ids: only user
// message items confirm a submission. Assistant text that happens to repeat a
// client id must not acknowledge pending input.
func confirmedUserMessageIDs(messages []codextui.Message) map[string]struct{} {
	ids := map[string]struct{}{}
	for _, message := range messages {
		if message.Role != codextui.RoleUser {
			continue
		}
		if id := strings.TrimSpace(message.ClientID); id != "" {
			ids[id] = struct{}{}
		}
	}
	return ids
}

func withoutConfirmedPendingSteers(pending []pendingSteerSubmission, confirmed map[string]struct{}) []pendingSteerSubmission {
	if len(pending) == 0 {
		return pending
	}
	kept := make([]pendingSteerSubmission, 0, len(pending))
	for _, steer := range pending {
		if _, ok := confirmed[strings.TrimSpace(steer.ID)]; ok {
			continue
		}
		kept = append(kept, steer)
	}
	return kept
}

func withoutConfirmedQueuedSubmissions(queued []queuedSubmission, confirmed map[string]struct{}) []queuedSubmission {
	if len(queued) == 0 {
		return queued
	}
	kept := make([]queuedSubmission, 0, len(queued))
	for _, submission := range queued {
		if _, ok := confirmed[strings.TrimSpace(submission.Request.ClientID)]; ok {
			continue
		}
		kept = append(kept, submission)
	}
	return kept
}
