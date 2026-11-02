package tea

import (
	"strings"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

// Rust parity subset: codex-rs/tui/src/app/thread_events.rs.
//
// Rust forks a side conversation into its own thread
// (codex-rs/tui/src/app_server_session.rs:1156-1176,
// ForkPresentation::SideConversation), so the side thread owns its own
// ThreadEventStore and its own delegated_turns bookkeeping. The parent thread
// keeps the store it already had. Go keeps side transcripts as snapshots inside
// the model instead, so the delegated-turn bookkeeping has to travel with each
// snapshot: the parent snapshot and the side snapshot are separate threads and
// must not share markers.
//
// The snapshot never renders user prompts, so what the filter protects here is
// the delegated turn's private agent output (commentary/reasoning), exactly the
// items thread_events.rs:34-51 (hide_private_items_after_voice_handoff) and
// thread_events.rs:465-490 (buffered notification filter) drop on replay.

// sideVoicePrivacy records the turn ids opened by a realtime voice handoff for
// one snapshot (the parent transcript or the side thread's transcript).
type sideVoicePrivacy struct {
	delegated map[string]bool
	typed     map[string]bool
	// currentTurn is the turn the snapshot's thread last reported as started.
	// The app-server bridge forwards turn/started for inactive threads but the
	// item notifications it turns into thread-scoped events carry no turn id
	// (app/remote_tui.go:5198 AgentMessageDelta, :5258 ItemCompleted), so the
	// snapshot has to remember the turn context itself.
	currentTurn string
}

func (p *sideVoicePrivacy) noteTurnStarted(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if p == nil || turnID == "" {
		return
	}
	p.currentTurn = turnID
}

// noteDelegatedTurn mirrors ThreadEventStore::push_notification_inner
// (thread_events.rs:195-201): the marker is append-only and survives
// turn completion.
func (p *sideVoicePrivacy) noteDelegatedTurn(turnID string) {
	turnID = strings.TrimSpace(turnID)
	if p == nil || turnID == "" {
		return
	}
	if p.delegated == nil {
		p.delegated = map[string]bool{}
	}
	p.delegated[turnID] = true
}

func (p *sideVoicePrivacy) noteTypedItem(turnID string, itemID string) {
	if p == nil {
		return
	}
	if p.typed == nil {
		p.typed = map[string]bool{}
	}
	p.typed[realtimeItemKey(turnID, itemID)] = true
}

// syncFromModel copies the active thread's markers into the snapshot. It is
// only called while the model is displaying the thread that owns the snapshot:
// the model's delegatedTurns/typedRealtimeItems describe the displayed thread
// (thread_reset.go:23-27 clears them on a thread switch), so this is the
// pointer-follow of Rust's "the snapshot is the thread's store".
func (p *sideVoicePrivacy) syncFromModel(m *Model) {
	if p == nil || m == nil {
		return
	}
	if len(m.delegatedTurns) > 0 {
		if p.delegated == nil {
			p.delegated = map[string]bool{}
		}
		for turnID := range m.delegatedTurns {
			p.delegated[turnID] = true
		}
	}
	if len(m.typedRealtimeItems) > 0 {
		if p.typed == nil {
			p.typed = map[string]bool{}
		}
		for key := range m.typedRealtimeItems {
			p.typed[key] = true
		}
	}
	p.noteTurnStarted(m.currentTurnID)
}

func (p *sideVoicePrivacy) turnID(event protocol.ThreadEvent) string {
	if id := strings.TrimSpace(firstNonEmpty(event.TurnID, event.InternalTurnID)); id != "" {
		return id
	}
	if p == nil {
		return ""
	}
	return p.currentTurn
}

// suppressed mirrors the live filter (realtime_delegation.go suppressRealtimeDelta
// / suppressPrivateRealtimeItem): a delegated turn drops its private reasoning
// and commentary unless the item started as typed work.
func (p *sideVoicePrivacy) suppressed(turnID string, event protocol.ThreadEvent) bool {
	if p == nil || !p.delegated[turnID] {
		return false
	}
	switch event.Type {
	case "item.delta":
		if event.Delta == nil || strings.TrimSpace(event.Delta.Text) == "" {
			return false
		}
		return !p.typed[realtimeItemKey(turnID, event.Delta.ItemID)]
	case "item.reasoning.delta":
		return event.Delta == nil || !p.typed[realtimeItemKey(turnID, event.Delta.ItemID)]
	case "item.completed":
		if event.Item == nil {
			return false
		}
		if !chatwidget.IsPrivateRealtimeAgentItem(event.Item.Type, event.Item.Phase, firstNonEmpty(event.Item.Text, event.Item.Message)) {
			return false
		}
		return !p.typed[realtimeItemKey(turnID, event.Item.ID)]
	}
	return false
}

// applySideSnapshotEvent folds one thread-scoped event into a side snapshot with
// the voice-handoff privacy applied, then falls back to the plain snapshot
// renderer for everything the filter keeps.
func applySideSnapshotEvent(privacy *sideVoicePrivacy, messages []codextui.Message, event protocol.ThreadEvent) []codextui.Message {
	if privacy == nil {
		return applyThreadEventToSideSnapshot(messages, event)
	}
	if event.Type == "turn.started" {
		privacy.noteTurnStarted(firstNonEmpty(event.TurnID, event.InternalTurnID))
	}
	turnID := privacy.turnID(event)
	if event.Item != nil && (event.Type == "item.started" || event.Type == "item.completed") {
		if realtimeItemIsDelegationUserMessage(event.Item) {
			// thread_events.rs:195-201: the marker is recorded on item
			// start/completed and outlives the turn.
			privacy.noteDelegatedTurn(turnID)
			// Side snapshots never render user prompts, so nothing else to do.
			return messages
		}
		switch chatwidget.NormalizeItemKind(event.Item.Type) {
		case "reasoning", "agentmessage", "assistantmessage":
			// Rust keeps items that started before the handoff visible
			// (RealtimeAgentItemOrigin::Typed, thread_events.rs:451-462).
			if !privacy.delegated[turnID] {
				privacy.noteTypedItem(turnID, event.Item.ID)
			}
		}
	}
	if privacy.suppressed(turnID, event) {
		return messages
	}
	return applyThreadEventToSideSnapshot(messages, event)
}
