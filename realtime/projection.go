package realtime

// Rust #62251 keeps one copy of an accepted handoff or saved transcript chunk:
// the ordinary model user message. Its realtime receipt is projected from that
// message plus the provenance sidecar the host attaches to harness metadata, so
// a restarted thread reports the same receipts without a second persisted copy.

import (
	"encoding/json"
	"strings"
)

// RealtimeHandoffMetadata is Rust's `CodexHarnessMetadata::realtime_handoff`:
// the facts connecting an ordinary accepted input to its realtime frames.
type RealtimeHandoffMetadata struct {
	EventID           string              `json:"event_id"`
	RealtimeSessionID string              `json:"realtime_session_id"`
	ObservedAtMS      int64               `json:"observed_at_ms"`
	HandoffID         string              `json:"handoff_id"`
	Mode              RealtimeHandoffMode `json:"mode"`
	SourceEventIDs    []string            `json:"source_event_ids"`
	// TurnID is Go's carrier for the turn the handoff entered. Rust reads it
	// from the response item's metadata passthrough; Go records it beside the
	// provenance when the host assigns the turn.
	TurnID string `json:"turn_id,omitempty"`
}

// RealtimeSessionInputMetadata is Rust's
// `CodexHarnessMetadata::realtime_session_input`: the provenance of a transcript
// chunk saved in ordinary model history. The message owns its text.
type RealtimeSessionInputMetadata struct {
	EventID           string   `json:"event_id"`
	RealtimeSessionID string   `json:"realtime_session_id"`
	ObservedAtMS      int64    `json:"observed_at_ms"`
	SourceEventIDs    []string `json:"source_event_ids"`
}

// RealtimeProvenance is the decoded realtime sidecar of one persisted response
// item. A message carries at most the provenance of the receipt it produced.
type RealtimeProvenance struct {
	Handoff      *RealtimeHandoffMetadata
	SessionInput *RealtimeSessionInputMetadata
}

// DecodeRealtimeProvenance reads the two realtime members of a harness metadata
// object. Anything else in the sidecar is left to other consumers.
func DecodeRealtimeProvenance(metadata json.RawMessage) RealtimeProvenance {
	provenance := RealtimeProvenance{}
	if len(strings.TrimSpace(string(metadata))) == 0 || string(metadata) == "null" {
		return provenance
	}
	var sidecar struct {
		Handoff      *RealtimeHandoffMetadata      `json:"realtime_handoff"`
		SessionInput *RealtimeSessionInputMetadata `json:"realtime_session_input"`
	}
	if err := json.Unmarshal(metadata, &sidecar); err != nil {
		return RealtimeProvenance{}
	}
	provenance.Handoff = sidecar.Handoff
	provenance.SessionInput = sidecar.SessionInput
	return provenance
}

// ObservedAtMS reports the stored observation time of either receipt kind so a
// restored realtime clock never steps backwards across a restart.
func (p RealtimeProvenance) ObservedAtMS() *int64 {
	var observed int64
	switch {
	case p.Handoff != nil:
		observed = p.Handoff.ObservedAtMS
	case p.SessionInput != nil:
		observed = p.SessionInput.ObservedAtMS
	default:
		return nil
	}
	return &observed
}

// realtimeMessage is the persisted user message a receipt is projected from.
type realtimeMessage struct {
	ID      string
	TurnID  string
	Text    string
	Present bool
}

// decodeRealtimeMessage accepts exactly the shape Rust projects: a user message
// with an id and a single input-text part. Any other item yields no receipt, so
// the model message that carried the text stays the only authority for it.
func decodeRealtimeMessage(payload json.RawMessage) realtimeMessage {
	var item struct {
		Type    string `json:"type"`
		ID      string `json:"id"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Passthrough *struct {
			TurnID string `json:"turn_id"`
		} `json:"internal_chat_message_metadata_passthrough"`
	}
	if err := json.Unmarshal(payload, &item); err != nil {
		return realtimeMessage{}
	}
	if item.Type != "message" || item.Role != "user" || strings.TrimSpace(item.ID) == "" {
		return realtimeMessage{}
	}
	if len(item.Content) != 1 || item.Content[0].Type != "input_text" {
		return realtimeMessage{}
	}
	message := realtimeMessage{ID: item.ID, Text: item.Content[0].Text, Present: true}
	if item.Passthrough != nil {
		message.TurnID = strings.TrimSpace(item.Passthrough.TurnID)
	}
	return message
}

// RealtimeItemFromResponseItem projects the receipt a persisted model message
// carries (Rust `ResponseItemEnvelope::realtime_item`). A handoff needs the turn
// it entered; a saved chunk does not, because it started no turn.
func RealtimeItemFromResponseItem(payload json.RawMessage, metadata json.RawMessage) (*RealtimeItem, bool) {
	provenance := DecodeRealtimeProvenance(metadata)
	message := decodeRealtimeMessage(payload)
	if !message.Present {
		return nil, false
	}
	switch {
	case provenance.Handoff != nil:
		handoff := provenance.Handoff
		turnID := message.TurnID
		if turnID == "" {
			turnID = strings.TrimSpace(handoff.TurnID)
		}
		if turnID == "" || !validHandoffMode(handoff.Mode) {
			return nil, false
		}
		return &RealtimeItem{
			ID:                handoff.EventID,
			RealtimeSessionID: handoff.RealtimeSessionID,
			ObservedAtMS:      provenance.ObservedAtMS(),
			Content: RealtimeItemContent{
				Kind:         RealtimeItemKindTurnInputAccepted,
				HandoffID:    handoff.HandoffID,
				TurnID:       turnID,
				HandoffMode:  handoff.Mode,
				SourceEvents: append([]string(nil), handoff.SourceEventIDs...),
				InputItemID:  message.ID,
				Input:        message.Text,
			},
		}, true
	case provenance.SessionInput != nil:
		saved := provenance.SessionInput
		return &RealtimeItem{
			ID:                saved.EventID,
			RealtimeSessionID: saved.RealtimeSessionID,
			ObservedAtMS:      provenance.ObservedAtMS(),
			Content: RealtimeItemContent{
				Kind:         RealtimeItemKindSessionInput,
				InputReason:  RealtimeSessionInputReasonChunk,
				SourceEvents: append([]string(nil), saved.SourceEventIDs...),
				InputItemID:  message.ID,
				Input:        message.Text,
			},
		}, true
	default:
		return nil, false
	}
}

// RealtimeObservedAtMS reports the stored observation time on a persisted model
// message (Rust `RolloutItem::realtime_observed_at_ms` for a response item). The
// message shape does not matter: the clock floor only needs the host's time.
func RealtimeObservedAtMS(metadata json.RawMessage) *int64 {
	return DecodeRealtimeProvenance(metadata).ObservedAtMS()
}

func validHandoffMode(mode RealtimeHandoffMode) bool {
	return mode == RealtimeHandoffModeStart || mode == RealtimeHandoffModeSteer
}
