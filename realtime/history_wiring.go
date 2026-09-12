package realtime

// Bridges the realtime session manager to the canonical Voice timeline. The
// manager owns one reducer per thread and turns its effects into the app-server
// item notifications.

// historyForLocked returns the reducer for a thread. The caller must hold m.mu.
func (m *Manager) historyForLocked(threadID string) *RealtimeHistoryState {
	m.ensureLocked()
	state := m.history[threadID]
	if state == nil {
		state = &RealtimeHistoryState{}
		m.history[threadID] = state
	}
	return state
}

// historyFor returns the reducer for a thread under the manager lock.
func (m *Manager) historyFor(threadID string) *RealtimeHistoryState {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.historyForLocked(threadID)
}

// historyItemNotifications converts reducer effects into the app-server item
// notifications. Transcript segments publish only their completed item; every
// other item publishes both boundaries.
func historyItemNotifications(threadID string, effects RealtimeEventEffects) []Notification {
	if effects.Empty() {
		return nil
	}
	var notifications []Notification
	if stream := effects.TranscriptStream; stream != nil {
		if stream.StartedItem != nil {
			notifications = append(notifications, Notification{
				Method: NotificationItemStarted,
				Params: ItemStartedNotification{ThreadID: threadID, Item: *stream.StartedItem},
			})
		}
		notifications = append(notifications, Notification{
			Method: NotificationItemTranscriptDelta,
			Params: ItemTranscriptDeltaNotification{
				ThreadID: threadID,
				ItemID:   stream.ItemID,
				Delta:    stream.Delta,
			},
		})
	}
	for _, item := range effects.Items {
		if item.Content.Kind != RealtimeItemKindTranscriptSegment {
			notifications = append(notifications, Notification{
				Method: NotificationItemStarted,
				Params: ItemStartedNotification{ThreadID: threadID, Item: item},
			})
		}
		notifications = append(notifications, Notification{
			Method: NotificationItemCompleted,
			Params: ItemCompletedNotification{ThreadID: threadID, Item: item},
		})
	}
	return notifications
}

// observeSessionEffects records a session boundary in the timeline and returns
// the item notifications it produced.
func (m *Manager) observeSessionEffects(threadID string, effects RealtimeEventEffects) []Notification {
	if m == nil || effects.Empty() {
		return nil
	}
	return historyItemNotifications(threadID, effects)
}

// observeTimelineEvent records one transport event in the canonical timeline.
// It mirrors the Rust reducer's observation set: transcript streaming, handoff
// requests, and errors.
func (m *Manager) observeTimelineEvent(threadID string, event Event) []Notification {
	if m == nil {
		return nil
	}
	history := m.historyFor(threadID)
	if history == nil {
		return nil
	}
	switch event.Type {
	case "input_transcript.delta":
		return historyItemNotifications(threadID, history.TranscriptDelta(RealtimeTranscriptRoleUser, event.Delta))
	case "output_transcript.delta":
		return historyItemNotifications(threadID, history.TranscriptDelta(RealtimeTranscriptRoleAssistant, event.Delta))
	case "input_transcript.done":
		return historyItemNotifications(threadID, history.TranscriptDone(RealtimeTranscriptRoleUser, event.Text))
	case "output_transcript.done":
		return historyItemNotifications(threadID, history.TranscriptDone(RealtimeTranscriptRoleAssistant, event.Text))
	case "handoff.requested":
		history.NoteHandoffRequested()
		return nil
	case "error":
		history.MarkFailed()
		return nil
	default:
		return nil
	}
}

// BindTurnSession records which realtime session produced a turn. The app-server
// calls it when the backing turn starts.
func (m *Manager) BindTurnSession(threadID, turnID string) {
	if m == nil {
		return
	}
	m.historyFor(threadID).BindTurnSession(turnID)
}

// SetActiveTurn records the backing turn that a later session start belongs to.
func (m *Manager) SetActiveTurn(threadID, turnID string) {
	if m == nil {
		return
	}
	m.historyFor(threadID).SetActiveTurn(turnID)
}

// ClearActiveTurn clears the recorded backing turn on completion or abort.
func (m *Manager) ClearActiveTurn(threadID, turnID string) {
	if m == nil {
		return
	}
	m.historyFor(threadID).ClearActiveTurn(turnID)
}

// ObserveAgentItem records a backing agent item in the timeline and returns the
// item notifications for any promotion it triggers.
func (m *Manager) ObserveAgentItem(threadID, turnID, itemID, text string, completed bool) []Notification {
	if m == nil {
		return nil
	}
	history := m.historyFor(threadID)
	effects := history.ObserveAgentItem(turnID, itemID, text, completed)
	if completed {
		history.FinishStreamingAgentMessage(itemID)
	}
	return historyItemNotifications(threadID, effects)
}

// StreamAgentMessageDelta records streamed agent text and returns the item
// notifications for any promotion it triggers.
func (m *Manager) StreamAgentMessageDelta(threadID, turnID, itemID, delta string) []Notification {
	if m == nil {
		return nil
	}
	effects := m.historyFor(threadID).StreamAgentMessageDelta(turnID, itemID, delta)
	return historyItemNotifications(threadID, effects)
}

// PromoteAgentItem records a whole-item promotion for a backing agent item.
func (m *Manager) PromoteAgentItem(threadID, turnID, itemID string) []Notification {
	if m == nil {
		return nil
	}
	effects := m.historyFor(threadID).PromoteAgentItem(turnID, itemID, BemItemPresentation{Kind: BemPresentationWholeItem})
	return historyItemNotifications(threadID, effects)
}

// SealRealtimeUserInput seals live transcript segments before a new user
// message enters the timeline.
func (m *Manager) SealRealtimeUserInput(threadID string) []Notification {
	if m == nil {
		return nil
	}
	effects := m.historyFor(threadID).SealUserInput()
	return historyItemNotifications(threadID, effects)
}

// ActiveRealtimeSessionID reports the session recorded in the thread timeline.
func (m *Manager) ActiveRealtimeSessionID(threadID string) string {
	if m == nil {
		return ""
	}
	return m.historyFor(threadID).ActiveSessionID()
}
