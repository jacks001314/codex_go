package app

import (
	"strings"

	"codex_go/appserver"
	"codex_go/tui/chatwidget"
)

// Rust parity subset: codex-rs/tui/src/app/thread_events.rs
// (ThreadEventStore::delegated_turns and hide_private_items_after_voice_handoff).
// A voice handoff arrives as a user message whose text carries the
// <realtime_delegation> envelope. That turn is "delegated": its reasoning and
// commentary are private to the voice conversation, so replay must not show
// them (Rust is_private_realtime_agent_item).

const maxDelegatedTurnIDLength = 512

// NoteDelegatedTurn records turnID as delegated and reports whether it was
// newly added. The set is append-only: a completed turn keeps its marker so a
// later thread rebuild suppresses the same private items (Rust never removes a
// marker on TurnCompleted).
func (s *ThreadEventStore) NoteDelegatedTurn(turnID string) bool {
	if s == nil {
		return false
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" || len(turnID) > maxDelegatedTurnIDLength {
		return false
	}
	for _, existing := range s.DelegatedTurns {
		if existing == turnID {
			return false
		}
	}
	s.ensure()
	s.DelegatedTurns = append(s.DelegatedTurns, turnID)
	s.trimDelegatedTurns()
	return true
}

// DelegatedTurnIDs returns the ordered delegated turn ids of this thread. The
// slice is a copy, so callers cannot mutate the store.
func (s *ThreadEventStore) DelegatedTurnIDs() []string {
	if s == nil {
		return nil
	}
	return append([]string(nil), s.DelegatedTurns...)
}

// IsDelegatedTurn reports whether turnID was marked by a voice handoff.
func (s *ThreadEventStore) IsDelegatedTurn(turnID string) bool {
	if s == nil {
		return false
	}
	turnID = strings.TrimSpace(turnID)
	for _, existing := range s.DelegatedTurns {
		if existing == turnID {
			return true
		}
	}
	return false
}

// trimDelegatedTurns bounds the marker list like Rust trim_delegated_turns: a
// marker whose turn still has buffered output is kept in preference to an
// older one (an evicted marker makes replay hide private items conservatively).
func (s *ThreadEventStore) trimDelegatedTurns() {
	capacity := s.Capacity
	if capacity < 1 {
		capacity = 1
	}
	for len(s.DelegatedTurns) > capacity {
		stale := 0
		for i, turnID := range s.DelegatedTurns {
			if !s.turnHasBufferedOutput(turnID) {
				stale = i
				break
			}
		}
		s.DelegatedTurns = append(s.DelegatedTurns[:stale], s.DelegatedTurns[stale+1:]...)
	}
}

func (s *ThreadEventStore) turnHasBufferedOutput(turnID string) bool {
	for _, event := range s.Buffer {
		if event.Type != ThreadBufferedEventNotification || event.Notification == nil {
			continue
		}
		notification := event.Notification
		switch notification.Name {
		case ServerNotificationItemCompleted,
			ServerNotificationAgentMessageDelta,
			ServerNotificationReasoningSummaryTextDelta,
			ServerNotificationReasoningTextDelta,
			ServerNotificationReasoningSummaryPartAdded,
			ServerNotificationTurnCompleted:
			if realtimeDelegationTurnID(*notification) == turnID {
				return true
			}
		}
	}
	return false
}

// noteDelegatedTurnFromNotification marks a turn when its user message carries
// the realtime delegation envelope, mirroring Rust push_notification_inner.
func (s *ThreadEventStore) noteDelegatedTurnFromNotification(notification ServerEvent) bool {
	if s == nil {
		return false
	}
	if notification.Name != ServerNotificationItemStarted && notification.Name != ServerNotificationItemCompleted {
		return false
	}
	if !IsRealtimeDelegationUserMessage(notification.Item) {
		return false
	}
	return s.NoteDelegatedTurn(realtimeDelegationTurnID(notification))
}

func IsRealtimeDelegationUserMessage(item *appserver.ThreadItem) bool {
	if item == nil {
		return false
	}
	switch chatwidget.NormalizeItemKind(item.Type) {
	case "usermessage", "message":
	default:
		return false
	}
	for _, content := range item.Content {
		if _, ok := chatwidget.RealtimeDelegationInput(content.Text); ok {
			return true
		}
	}
	_, ok := chatwidget.RealtimeDelegationInput(item.Text)
	return ok
}

// realtimeDelegationTurnID resolves the turn a notification belongs to (Rust
// reads the typed notification's own turn_id).
func realtimeDelegationTurnID(notification ServerEvent) string {
	if turnID := strings.TrimSpace(notification.TurnID); turnID != "" {
		return turnID
	}
	if turnID := strings.TrimSpace(notification.Target.TurnID); turnID != "" {
		return turnID
	}
	if notification.Item != nil {
		if turnID := strings.TrimSpace(notification.Item.TurnID); turnID != "" {
			return turnID
		}
	}
	if notification.Turn != nil {
		return strings.TrimSpace(notification.Turn.ID)
	}
	return ""
}

func realtimeDelegationItemID(notification ServerEvent) string {
	if itemID := strings.TrimSpace(notification.ItemID); itemID != "" {
		return itemID
	}
	if notification.Item != nil {
		return strings.TrimSpace(notification.Item.ID)
	}
	return ""
}

func threadItemPhase(item *appserver.ThreadItem) string {
	if item == nil || item.Data == nil {
		return ""
	}
	for _, key := range []string{"phase", "messagePhase"} {
		if value, ok := item.Data[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func isPrivateRealtimeThreadItem(item *appserver.ThreadItem) bool {
	if item == nil {
		return false
	}
	text := item.Text
	if len(item.Content) > 0 {
		parts := make([]string, 0, len(item.Content))
		for _, content := range item.Content {
			parts = append(parts, content.Text)
		}
		if joined := strings.Join(parts, ""); strings.TrimSpace(joined) != "" {
			text = joined
		}
	}
	return chatwidget.IsPrivateRealtimeAgentItem(item.Type, threadItemPhase(item), text)
}

// HidePrivateRealtimeItemsAfterVoiceHandoff mirrors Rust
// hide_private_items_after_voice_handoff: a present marker lets earlier typed
// output survive, while an evicted marker hides private items conservatively.
func HidePrivateRealtimeItemsAfterVoiceHandoff(items []appserver.ThreadItem, delegated bool) []appserver.ThreadItem {
	hasMarker := false
	for i := range items {
		if IsRealtimeDelegationUserMessage(&items[i]) {
			hasMarker = true
			break
		}
	}
	voiceStarted := delegated && !hasMarker
	out := make([]appserver.ThreadItem, 0, len(items))
	for i := range items {
		item := &items[i]
		if IsRealtimeDelegationUserMessage(item) {
			voiceStarted = true
		}
		if voiceStarted && isPrivateRealtimeThreadItem(item) {
			continue
		}
		out = append(out, *item)
	}
	return out
}

// cloneTurnsForReplay deep-copies turns so snapshot pruning cannot mutate the
// live store (Rust clones ThreadEventSnapshot::turns).
func cloneTurnsForReplay(turns []appserver.Turn) []appserver.Turn {
	out := make([]appserver.Turn, len(turns))
	for i := range turns {
		out[i] = turns[i]
		if turns[i].Items == nil {
			continue
		}
		out[i].Items = make([]appserver.ThreadItem, len(turns[i].Items))
		for j := range turns[i].Items {
			out[i].Items[j] = cloneAppThreadItem(&turns[i].Items[j])
		}
	}
	return out
}

// applyRealtimeDelegationPrivacy prunes private voice-handoff content from a
// snapshot's turns and buffered events. It mirrors the Rust snapshot filter so
// a rebuilt thread widget never replays reasoning or commentary that belonged
// to a spoken turn.
func applyRealtimeDelegationPrivacy(snapshot *ThreadEventSnapshot) {
	if snapshot == nil {
		return
	}
	delegated := make(map[string]bool, len(snapshot.DelegatedTurns))
	for _, turnID := range snapshot.DelegatedTurns {
		if turnID = strings.TrimSpace(turnID); turnID != "" {
			delegated[turnID] = true
		}
	}
	for i := range snapshot.Turns {
		turn := &snapshot.Turns[i]
		for j := range turn.Items {
			if IsRealtimeDelegationUserMessage(&turn.Items[j]) {
				delegated[turn.ID] = true
				break
			}
		}
	}
	for i := range snapshot.Turns {
		turn := &snapshot.Turns[i]
		turn.Items = HidePrivateRealtimeItemsAfterVoiceHandoff(turn.Items, delegated[turn.ID])
	}

	bufferedMarkers := make(map[string]bool)
	for _, event := range snapshot.Events {
		if event.Type != ThreadBufferedEventNotification || event.Notification == nil {
			continue
		}
		notification := event.Notification
		if notification.Name != ServerNotificationItemStarted && notification.Name != ServerNotificationItemCompleted {
			continue
		}
		if IsRealtimeDelegationUserMessage(notification.Item) {
			bufferedMarkers[realtimeDelegationTurnID(*notification)] = true
		}
	}
	voiceStarted := make(map[string]bool)
	for turnID := range delegated {
		if !bufferedMarkers[turnID] {
			voiceStarted[turnID] = true
		}
	}
	typedItems := map[string]bool{}
	out := make([]ThreadBufferedEvent, 0, len(snapshot.Events))
	for _, event := range snapshot.Events {
		if event.Type != ThreadBufferedEventNotification || event.Notification == nil {
			out = append(out, event)
			continue
		}
		notification := event.Notification
		turnID := realtimeDelegationTurnID(*notification)
		key := turnID + "\x00" + realtimeDelegationItemID(*notification)
		switch notification.Name {
		case ServerNotificationItemStarted:
			if IsRealtimeDelegationUserMessage(notification.Item) {
				voiceStarted[turnID] = true
			} else if kind := chatwidget.NormalizeItemKind(threadItemKind(notification.Item)); kind == "agentmessage" || kind == "reasoning" {
				if voiceStarted[turnID] {
					delete(typedItems, key)
				} else {
					typedItems[key] = true
				}
			}
		case ServerNotificationItemCompleted:
			if IsRealtimeDelegationUserMessage(notification.Item) {
				voiceStarted[turnID] = true
				break
			}
			if voiceStarted[turnID] && !typedItems[key] && isPrivateRealtimeThreadItem(notification.Item) {
				continue
			}
		case ServerNotificationAgentMessageDelta,
			ServerNotificationReasoningSummaryTextDelta,
			ServerNotificationReasoningTextDelta,
			ServerNotificationReasoningSummaryPartAdded:
			if voiceStarted[turnID] && !typedItems[key] {
				continue
			}
		case ServerNotificationTurnCompleted:
			if notification.Turn != nil && delegated[notification.Turn.ID] {
				notification.Turn.Items = HidePrivateRealtimeItemsAfterVoiceHandoff(notification.Turn.Items, true)
			}
		}
		out = append(out, event)
	}
	snapshot.Events = out
}

func threadItemKind(item *appserver.ThreadItem) string {
	if item == nil {
		return ""
	}
	return item.Type
}
