package rollout

import (
	"encoding/json"
	"time"

	"codex_go/session"
)

// Rust parity: codex-rs/history/src/rollout_payload.rs's
// RolloutItem::InterAgentCommunicationMetadata and
// core/src/session/mod.rs's record_inter_agent_communication.

// InterAgentCommunicationMetadataLine is the delivery boundary Rust writes
// immediately before the communication itself. Readers that never saw the
// communication's envelope assign the preceding assistant deliveries by
// physical order up to this line.
func InterAgentCommunicationMetadataLine(triggerTurn bool, at time.Time) Line {
	if at.IsZero() {
		at = time.Now().UTC()
	}
	payload, _ := json.Marshal(struct {
		TriggerTurn bool `json:"trigger_turn"`
	}{TriggerTurn: triggerTurn})
	return Line{
		Type:      "inter_agent_communication_metadata",
		Timestamp: at.UTC().Format(time.RFC3339Nano),
		Payload:   payload,
	}
}

// AppendInterAgentCommunication persists one accepted agent communication the
// way Rust's record_inter_agent_communication does: the delivery boundary first,
// then the communication as a response item carrying its harness metadata
// (user_input_order, history_sequence and sampled_through_sequence).
func AppendInterAgentCommunication(recorder *Recorder, triggerTurn bool, item session.Item, at time.Time) error {
	if recorder == nil {
		return nil
	}
	if at.IsZero() {
		at = time.Now().UTC()
	}
	if err := recorder.AppendLine(InterAgentCommunicationMetadataLine(triggerTurn, at)); err != nil {
		return err
	}
	return AppendSessionItems(recorder, []session.Item{item}, at)
}
