package appserver

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/turn"
)

// Rust parity: codex-rs/core/src/session/mod.rs's
// `record_inter_agent_communication`. An accepted agent communication is
// recorded as soon as the turn takes it, before the first sampling request: the
// thread history gains the model-visible `agent_message` response item, and the
// rollout gains the delivery boundary followed by that item. A resumed thread
// therefore replays the same conversation, and a reader that never saw the
// envelope still assigns earlier deliveries by physical order.

// agentCommunicationSessionItem builds the persisted response item for one
// delivered communication. Rust's `ResponseItem::AgentMessage` carries no role,
// so neither does the session item, and the recorded payload is the model input
// itself.
func agentCommunicationSessionItem(id string, turnID string, input map[string]any, createdAt time.Time) (session.Item, bool) {
	if len(input) == 0 || strings.TrimSpace(stringFromMap(input, "type")) != "agent_message" {
		return session.Item{}, false
	}
	if strings.TrimSpace(stringFromMap(input, "author")) == "" || strings.TrimSpace(stringFromMap(input, "recipient")) == "" {
		return session.Item{}, false
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return session.Item{}, false
	}
	metadata := map[string]any{}
	if turnID != "" {
		metadata["turnId"] = turnID
	}
	return session.Item{
		ID:        id,
		Type:      "agent_message",
		CreatedAt: createdAt,
		Raw:       raw,
		Metadata:  metadata,
	}, true
}

// agentCommunicationItemID mirrors Rust's `ResponseItem::AgentMessage{id}`: a
// communication carries its identity from the moment it is created
// (`amsg_<uuid>`), so a re-delivered mail keeps one recorded item instead of
// producing a second delivery boundary. Inputs without an identity fall back to
// the admission position.
func agentCommunicationItemID(input map[string]any, idPrefix string, index int) string {
	if id := strings.TrimSpace(stringFromMap(input, "id")); id != "" {
		return id
	}
	return fmt.Sprintf("%s-%d", idPrefix, index)
}

// persistRuntimeAgentCommunications records this turn's accepted agent mail
// before the first sampling request and reports whether anything was recorded.
func (r *RuntimeRouter) persistRuntimeAgentCommunications(threadID string, turnID string, params *turn.TurnStartParams, createdAt time.Time) bool {
	if params == nil {
		return false
	}
	idPrefix := fmt.Sprintf("agent-message-%s", safeIdentifier(turnID))
	return r.persistRuntimeAgentCommunicationItems(threadID, turnID, params.AdditionalInputItems, params.AgentCommunicationTriggerTurns, idPrefix, createdAt)
}

// persistRuntimeSteerAgentCommunications mirrors the hook runtime's handling of
// `TurnInput::InterAgentCommunication` for mail delivered to a running turn
// (`core/src/hook_runtime.rs`): the communication is recorded when the turn
// admits it, before the next sampling request, and its boundary carries
// trigger_turn=false because the delivery did not start the turn.
func (r *RuntimeRouter) persistRuntimeSteerAgentCommunications(threadID string, turnID string, items []any, createdAt time.Time) bool {
	// A turn can steer more than once, so the fallback identity is scoped to
	// this admission rather than to the turn.
	idPrefix := fmt.Sprintf("agent-message-%s-steer-%d", safeIdentifier(turnID), createdAt.UTC().UnixNano())
	return r.persistRuntimeAgentCommunicationItems(threadID, turnID, items, nil, idPrefix, createdAt)
}

// persistRuntimeAgentCommunicationItems records each agent_message input as an
// accepted communication. Producers that build a triggering communication pass
// its per-item `trigger_turn` flag; queued and steering mail defaults to false.
func (r *RuntimeRouter) persistRuntimeAgentCommunicationItems(threadID string, turnID string, inputs []any, triggerTurns []bool, idPrefix string, createdAt time.Time) bool {
	if r == nil || !r.hasRuntimeThreadStore() {
		return false
	}
	// Mail that was already recorded stays recorded: a turn interrupted after it
	// admitted a communication re-queues its unrecorded input, so a re-delivery
	// with the same identity must not write a second item or boundary (Rust
	// confirms the sender at the recording point and consumes the mail).
	recordedIDs, recordedPayloads := r.recordedAgentCommunications(threadID)
	recorded := false
	rolloutReady := false
	for index, input := range inputs {
		payload, ok := configurationUpdatePayload(input)
		if !ok {
			continue
		}
		carriedID := strings.TrimSpace(stringFromMap(payload, "id"))
		id := agentCommunicationItemID(payload, idPrefix, index)
		if id == "" || recordedIDs[id] {
			continue
		}
		// A communication without a carried identity may already be in history
		// from another recording path (an injected agent_message is recorded when
		// the injection lands and then steered into the running turn). Its
		// canonical payload matches that recorded item exactly, because items
		// recorded from identified mail carry the `id` key and never match here.
		if carriedID == "" && recordedPayloads[agentCommunicationPayloadKey(payload)] {
			continue
		}
		item, ok := agentCommunicationSessionItem(id, turnID, payload, createdAt)
		if !ok {
			continue
		}
		if !rolloutReady {
			// Opening the rollout before the item reaches the thread store keeps a
			// freshly seeded rollout from writing the same item twice.
			r.ensureRuntimeRollout(threadID)
			rolloutReady = true
		}
		// The append annotates the item with the thread's harness metadata
		// (user_input_order, history_sequence, sampled_through_sequence) and
		// copies the recorded version back, so the rollout line carries the same
		// sidecar Rust writes beside the response item.
		recordedItems := []session.Item{item}
		if _, err := r.runtimeAppendItems(session.ThreadID(threadID), recordedItems); err != nil {
			continue
		}
		item = recordedItems[0]
		triggerTurn := index < len(triggerTurns) && triggerTurns[index]
		if err := r.appendRuntimeAgentCommunication(threadID, triggerTurn, item, createdAt); err != nil {
			continue
		}
		recordedIDs[id] = true
		if carriedID == "" {
			recordedPayloads[agentCommunicationPayloadKey(payload)] = true
		}
		recorded = true
	}
	return recorded
}

// recordedAgentCommunications reports the communications the thread already
// recorded, keyed both by identity and by canonical payload, so re-delivered
// mail is not recorded twice.
func (r *RuntimeRouter) recordedAgentCommunications(threadID string) (map[string]bool, map[string]bool) {
	ids := map[string]bool{}
	payloads := map[string]bool{}
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return ids, payloads
	}
	for i := range record.Items {
		item := &record.Items[i]
		if item.Type != "agent_message" {
			continue
		}
		if item.ID != "" {
			ids[item.ID] = true
		}
		if key := agentCommunicationRawKey(item.Raw); key != "" {
			payloads[key] = true
		}
	}
	return ids, payloads
}

// agentCommunicationPayloadKey canonicalizes a communication payload so an
// identity-less delivery can be compared against a recorded item's raw body.
func agentCommunicationPayloadKey(payload map[string]any) string {
	if len(payload) == 0 {
		return ""
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return ""
	}
	return agentCommunicationRawKey(encoded)
}

func agentCommunicationRawKey(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var object map[string]any
	if err := json.Unmarshal(raw, &object); err != nil {
		return string(raw)
	}
	encoded, err := json.Marshal(object)
	if err != nil {
		return string(raw)
	}
	return string(encoded)
}

// ensureRuntimeRollout materializes the thread's rollout (seeded from the
// current record) so a later append extends it instead of re-seeding.
func (r *RuntimeRouter) ensureRuntimeRollout(threadID string) {
	_ = r.withRuntimeRollout(threadID, func(*rollout.Recorder) error { return nil })
}

// appendRuntimeAgentCommunication writes the boundary and the communication to
// the thread's rollout in Rust's order.
func (r *RuntimeRouter) appendRuntimeAgentCommunication(threadID string, triggerTurn bool, item session.Item, now time.Time) error {
	return r.withRuntimeRollout(threadID, func(recorder *rollout.Recorder) error {
		return rollout.AppendInterAgentCommunication(recorder, triggerTurn, item, now)
	})
}
