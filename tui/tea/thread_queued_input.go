package tea

import bubbletea "charm.land/bubbletea/v2"

// queuedThreadInput contains unsent input belonging to one conversation.
// Submitted steers are stored separately until their receipts arrive.
type queuedThreadInput struct {
	queued   []queuedSubmission
	rejected []queuedSubmission
}

func (m *Model) stashQueuedThreadInput() {
	if m == nil {
		return
	}
	threadID := m.currentThreadID()
	if threadID == "" {
		return
	}
	if len(m.queued) > 0 || len(m.rejectedSteers) > 0 {
		if m.threadQueuedInput == nil {
			m.threadQueuedInput = map[string]queuedThreadInput{}
		}
		saved := m.threadQueuedInput[threadID]
		saved.queued = append(saved.queued, m.queued...)
		saved.rejected = append(saved.rejected, m.rejectedSteers...)
		m.threadQueuedInput[threadID] = saved
	}
	m.queued = nil
	m.rejectedSteers = nil
}

func (m *Model) restoreQueuedThreadInput(threadID string) {
	if m == nil {
		return
	}
	if saved, ok := m.threadQueuedInput[threadID]; ok {
		m.queued = saved.queued
		m.rejectedSteers = saved.rejected
		delete(m.threadQueuedInput, threadID)
	}
}

// consumeResumedQueue runs after the current UI update has finished restoring
// settings, history and draft state. The normal queue gate checks eligibility.
func (m *Model) consumeResumedQueue() bubbletea.Cmd {
	if m == nil || m.resumedQueueThreadID == "" {
		return nil
	}
	threadID := m.resumedQueueThreadID
	m.resumedQueueThreadID = ""
	if threadID != m.currentThreadID() {
		return nil
	}
	return m.submitNextQueued()
}
