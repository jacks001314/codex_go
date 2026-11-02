package tea

import (
	"strings"

	"codex_go/protocol"
	"codex_go/tui/chatwidget"
)

// Rust parity subset: codex-rs/tui/src/chatwidget/realtime.rs
// (delegated_reasoning_turns / RealtimeAgentItemOrigin) and
// chatwidget/protocol.rs handle_item_started/completed_notification.
//
// A realtime voice handoff opens a turn with a <realtime_delegation> user
// message. That turn's reasoning and commentary belong to the voice
// conversation: the live TUI must not paint them, and the marker must outlive
// turn completion so a replay of the thread makes the same decision.

// maxDelegatedTurnMarkers bounds the per-thread marker set. Rust bounds it by
// the replay buffer capacity; the TUI only needs enough recent turns for a
// thread rebuild.
const maxDelegatedTurnMarkers = 64

func realtimeItemKey(turnID string, itemID string) string {
	return strings.TrimSpace(turnID) + "\x00" + strings.TrimSpace(itemID)
}

// noteDelegatedTurn records turnID as opened by a voice handoff. The marker is
// append-only: Rust never removes it on TurnCompleted.
func (m *Model) noteDelegatedTurn(turnID string) {
	if m == nil {
		return
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return
	}
	if m.delegatedTurns == nil {
		m.delegatedTurns = map[string]struct{}{}
	}
	if _, ok := m.delegatedTurns[turnID]; ok {
		return
	}
	m.delegatedTurns[turnID] = struct{}{}
	m.delegatedTurnOrder = append(m.delegatedTurnOrder, turnID)
	for len(m.delegatedTurnOrder) > maxDelegatedTurnMarkers {
		oldest := m.delegatedTurnOrder[0]
		m.delegatedTurnOrder = m.delegatedTurnOrder[1:]
		delete(m.delegatedTurns, oldest)
	}
}

// turnIsDelegated reports whether turnID was opened by a voice handoff.
func (m *Model) turnIsDelegated(turnID string) bool {
	if m == nil || len(m.delegatedTurns) == 0 {
		return false
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return false
	}
	_, ok := m.delegatedTurns[turnID]
	return ok
}

func (m *Model) currentTurnIsDelegated() bool {
	if m == nil {
		return false
	}
	return m.turnIsDelegated(m.currentTurnID)
}

// noteTypedRealtimeItem records a reasoning/agent item that started while its
// turn was still a typed turn. Rust keeps such items visible even if a later
// voice handoff steers the same turn (RealtimeAgentItemOrigin::Typed).
func (m *Model) noteTypedRealtimeItem(turnID string, itemID string) {
	if m == nil {
		return
	}
	turnID = strings.TrimSpace(turnID)
	itemID = strings.TrimSpace(itemID)
	if turnID == "" || itemID == "" {
		return
	}
	if m.typedRealtimeItems == nil {
		m.typedRealtimeItems = map[string]struct{}{}
	}
	m.typedRealtimeItems[realtimeItemKey(turnID, itemID)] = struct{}{}
}

func (m *Model) realtimeItemIsTyped(turnID string, itemID string) bool {
	if m == nil || len(m.typedRealtimeItems) == 0 {
		return false
	}
	_, ok := m.typedRealtimeItems[realtimeItemKey(turnID, itemID)]
	return ok
}

func realtimeItemIsDelegationUserMessage(item *protocol.ThreadItem) bool {
	if item == nil {
		return false
	}
	switch chatwidget.NormalizeItemKind(item.Type) {
	case "usermessage", "message":
	default:
		return false
	}
	_, ok := chatwidget.RealtimeDelegationInput(firstNonEmpty(item.Text, item.Message))
	return ok
}

// observeRealtimeItem keeps the per-turn bookkeeping in step with the item
// stream: delegation prompts open a delegated turn, and reasoning/agent items
// seen before that point stay typed.
func (m *Model) observeRealtimeItem(item *protocol.ThreadItem) {
	if m == nil || item == nil {
		return
	}
	turnID := m.currentTurnID
	if realtimeItemIsDelegationUserMessage(item) {
		m.noteDelegatedTurn(turnID)
		return
	}
	switch chatwidget.NormalizeItemKind(item.Type) {
	case "reasoning", "agentmessage", "assistantmessage":
		if !m.turnIsDelegated(turnID) {
			m.noteTypedRealtimeItem(turnID, item.ID)
		}
	}
}

// suppressPrivateRealtimeItem reports whether a delegated turn's private item
// must be dropped from the live transcript (Rust
// handle_item_completed_notification / handle_item_started_notification).
func (m *Model) suppressPrivateRealtimeItem(item *protocol.ThreadItem) bool {
	if m == nil || item == nil {
		return false
	}
	if !m.currentTurnIsDelegated() {
		return false
	}
	text := firstNonEmpty(item.Text, item.Message)
	if !chatwidget.IsPrivateRealtimeAgentItem(item.Type, item.Phase, text) {
		return false
	}
	// Typed work that predates the handoff stays visible.
	return !m.realtimeItemIsTyped(m.currentTurnID, item.ID)
}

// suppressRealtimeDelta reports whether a streamed delta belongs to a private
// delegated item (Rust drops AgentMessageDelta and reasoning deltas of a
// delegated turn unless the item started as typed work).
func (m *Model) suppressRealtimeDelta(itemID string) bool {
	if m == nil || !m.currentTurnIsDelegated() {
		return false
	}
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return true
	}
	return !m.realtimeItemIsTyped(m.currentTurnID, itemID)
}
