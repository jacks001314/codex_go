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

// SetHistoryItemSink installs the host callback that persists canonical
// realtime items in event order. It runs before the matching lifecycle
// notifications, mirroring Rust's append-then-notify ordering.
func (m *Manager) SetHistoryItemSink(sink func(string, []RealtimeItem)) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.historyItemSink = sink
	m.mu.Unlock()
}

// SetHistoryEnabled installs the host predicate that decides whether a thread
// has durable history. Ephemeral threads report false, so they neither persist
// items nor publish history lifecycle events (Rust #41924).
func (m *Manager) SetHistoryEnabled(enabled func(string) bool) {
	if m == nil {
		return
	}
	m.mu.Lock()
	m.historyEnabled = enabled
	m.mu.Unlock()
}

// historyPublicationEnabled reports whether canonical history applies to the
// thread.
func (m *Manager) historyPublicationEnabled(threadID string) bool {
	m.mu.Lock()
	enabled := m.historyEnabled
	m.mu.Unlock()
	return enabled == nil || enabled(threadID)
}

// persistHistoryItems hands the canonical items to the host sink.
func (m *Manager) persistHistoryItems(threadID string, items []RealtimeItem) {
	if len(items) == 0 {
		return
	}
	m.mu.Lock()
	sink := m.historyItemSink
	m.mu.Unlock()
	if sink != nil {
		sink(threadID, items)
	}
}

// publishHistoryEffects emits the live transcript stream, persists the durable
// items in event order, then publishes their lifecycle notifications.
func (m *Manager) publishHistoryEffects(threadID string, effects RealtimeEventEffects) []Notification {
	if m == nil || effects.Empty() || !m.historyPublicationEnabled(threadID) {
		return nil
	}
	notifications := transcriptStreamNotifications(threadID, effects)
	m.persistHistoryItems(threadID, effects.Items)
	return append(notifications, historyLifecycleNotifications(threadID, effects.Items)...)
}

// transcriptStreamNotifications publishes the live transcript boundaries and
// delta for one observed event.
func transcriptStreamNotifications(threadID string, effects RealtimeEventEffects) []Notification {
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
	return notifications
}

// historyLifecycleNotifications publishes the durable boundaries of the
// canonical items. Transcript segments publish only their completed item;
// every other item publishes both boundaries.
func historyLifecycleNotifications(threadID string, items []RealtimeItem) []Notification {
	var notifications []Notification
	for _, item := range items {
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

// historyItemNotifications converts reducer effects into just the app-server
// item notifications, without persisting them. Hosts that own no durable store
// use it for projection-only callers.
func historyItemNotifications(threadID string, effects RealtimeEventEffects) []Notification {
	notifications := transcriptStreamNotifications(threadID, effects)
	return append(notifications, historyLifecycleNotifications(threadID, effects.Items)...)
}

// observeSessionEffects records a session boundary in the timeline and returns
// the item notifications it produced.
func (m *Manager) observeSessionEffects(threadID string, effects RealtimeEventEffects) []Notification {
	if m == nil || effects.Empty() {
		return nil
	}
	return m.publishHistoryEffects(threadID, effects)
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
		return m.publishHistoryEffects(threadID, history.TranscriptDelta(RealtimeTranscriptRoleUser, event.Delta))
	case "output_transcript.delta":
		return m.publishHistoryEffects(threadID, history.TranscriptDelta(RealtimeTranscriptRoleAssistant, event.Delta))
	case "input_transcript.done":
		return m.publishHistoryEffects(threadID, history.TranscriptDone(RealtimeTranscriptRoleUser, event.Text))
	case "output_transcript.done":
		return m.publishHistoryEffects(threadID, history.TranscriptDone(RealtimeTranscriptRoleAssistant, event.Text))
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
	return m.publishHistoryEffects(threadID, effects)
}

// StreamAgentMessageDelta records streamed agent text and returns the item
// notifications for any promotion it triggers.
func (m *Manager) StreamAgentMessageDelta(threadID, turnID, itemID, delta string) []Notification {
	if m == nil {
		return nil
	}
	effects := m.historyFor(threadID).StreamAgentMessageDelta(turnID, itemID, delta)
	return m.publishHistoryEffects(threadID, effects)
}

// PromoteAgentItem records a whole-item promotion for a backing agent item.
func (m *Manager) PromoteAgentItem(threadID, turnID, itemID string) []Notification {
	if m == nil {
		return nil
	}
	effects := m.historyFor(threadID).PromoteAgentItem(turnID, itemID, BemItemPresentation{Kind: BemPresentationWholeItem})
	return m.publishHistoryEffects(threadID, effects)
}

// SealRealtimeUserInput seals live transcript segments before a new user
// message enters the timeline.
func (m *Manager) SealRealtimeUserInput(threadID string) []Notification {
	if m == nil {
		return nil
	}
	effects := m.historyFor(threadID).SealUserInput()
	return m.publishHistoryEffects(threadID, effects)
}

// SealRealtimeUserInputForPrompt seals live transcript segments before a user
// turn enters the timeline. Realtime handoff delegations are excluded, matching
// Rust's `should_seal_user_input` rule.
func (m *Manager) SealRealtimeUserInputForPrompt(threadID, prompt string) []Notification {
	if m == nil {
		return nil
	}
	effects := m.historyFor(threadID).SealUserInputForPrompt(prompt)
	return m.publishHistoryEffects(threadID, effects)
}

// ActiveRealtimeSessionID reports the session recorded in the thread timeline.
func (m *Manager) ActiveRealtimeSessionID(threadID string) string {
	if m == nil {
		return ""
	}
	return m.historyFor(threadID).ActiveSessionID()
}
