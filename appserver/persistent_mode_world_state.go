package appserver

import (
	"encoding/json"
	"strings"
	"time"

	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/features"
	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

const persistentModeWorldStateInstructionsKind = "persistent_mode.instructions"

// persistentModeSnapshot mirrors Rust's PersistentModeSnapshot: an
// object is kept even when disabled so a later turn can distinguish "no
// instructions" from an unknown/legacy section.
type persistentModeSnapshot struct {
	Instructions string `json:"instructions,omitempty"`
}

// persistentModeWorldStateInputItem ports Rust PersistentModeState. It emits
// only when the rendered instruction hash changes, prefixes replacements, and
// retires stale guidance when persistent mode is no longer selected.
func (r *RuntimeRouter) persistentModeWorldStateInputItem(
	threadID string,
	params *turn.TurnStartParams,
	turnID string,
	reasoningEffort string,
	modelInfo *model.ModelInfo,
	cfg *config.Config,
) (any, error) {
	if r == nil || cfg == nil || guardianTurnStart(params) {
		return nil, nil
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, true)
	if err != nil {
		return nil, err
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		return nil, err
	}

	instructions := ""
	if features.PersistentModeEnabled(reasoningEffort) {
		var catalogInstructions *string
		if modelInfo != nil && modelInfo.ModelMessages != nil {
			catalogInstructions = modelInfo.ModelMessages.PersistentInstructions
		}
		instructions = contextfrag.PersistentModeInstructionText(
			catalogInstructions,
			r.persistentModeSendUserMessageAsyncAvailable(threadID, cfg, modelInfo),
		)
	}

	var current persistentModeSnapshot
	if fragment := contextfrag.PersistentModeFragment(instructions); fragment != nil {
		rendered := contextfrag.RenderStandalone(fragment)
		current.Instructions = sandbox.WorldStateFragmentHash(contextfrag.RoleDeveloper, rendered.Content)
	}
	snapshot, err := json.Marshal(current)
	if err != nil {
		return nil, err
	}

	previous, previousKind := decodePersistentModeWorldStateSnapshot(state.PersistentMode)
	previousHadInstructions := false
	switch previousKind {
	case "known":
		previousHadInstructions = previous.Instructions != ""
	case "unknown":
		previousHadInstructions = true
	case "absent":
		previousHadInstructions = recordHasRetainedPersistentMode(record)
	}

	if !sameJSONValue(state.PersistentMode, snapshot) {
		state.PersistentMode = snapshot
		record.Metadata.WorldState, err = session.EncodeWorldState(state)
		if err != nil {
			return nil, err
		}
		if err := r.runtimeSaveThreadRecord(record); err != nil {
			return nil, err
		}
	}
	if previousKind == "known" && previous == current {
		return nil, nil
	}

	emitted := ""
	switch {
	case instructions == "" && !previousHadInstructions:
		return nil, nil
	case instructions == "":
		emitted = contextfrag.PersistentModeRemovalNotice
	case previousHadInstructions:
		emitted = contextfrag.PersistentModeReplacementNotice + "\n\n" + instructions
	default:
		emitted = instructions
	}
	fragment := contextfrag.PersistentModeFragment(emitted)
	if fragment == nil {
		return nil, nil
	}
	return buildRenderedMessage(contextfrag.RenderStandalone(fragment)), nil
}

// persistentModeSendUserMessageAsyncAvailable mirrors the approval-channel
// tailoring in Rust PersistentModeState::new.
func (r *RuntimeRouter) persistentModeSendUserMessageAsyncAvailable(threadID string, cfg *config.Config, modelInfo *model.ModelInfo) bool {
	if modelInfo == nil {
		return false
	}
	rootAgent := !r.turnThreadIsSubagent(threadID)
	return requestUserInputAsyncAvailable(rootAgent, cfg.ExperimentalRequestUserInputEnabled(), modelInfo.ExperimentalSupportedTools) ||
		freeformAsyncToolAvailable(rootAgent, modelInfo.ExperimentalSupportedTools, features.Enabled(cfg.FeatureSettings(), "send_message_to_user_async"))
}

func decodePersistentModeWorldStateSnapshot(raw json.RawMessage) (persistentModeSnapshot, string) {
	if len(raw) == 0 {
		return persistentModeSnapshot{}, "absent"
	}
	var object map[string]json.RawMessage
	if err := json.Unmarshal(raw, &object); err != nil || object == nil {
		return persistentModeSnapshot{}, "unknown"
	}
	var snapshot persistentModeSnapshot
	if value, ok := object["instructions"]; ok {
		if err := json.Unmarshal(value, &snapshot.Instructions); err != nil {
			return persistentModeSnapshot{}, "unknown"
		}
		snapshot.Instructions = strings.TrimSpace(snapshot.Instructions)
	}
	return snapshot, "known"
}

func recordHasRetainedPersistentMode(record *session.Record) bool {
	if record == nil {
		return false
	}
	items := session.InputItemsFromRecord(record, &session.HistoryBuildOptions{IncludeToolOutputs: true, CWD: strings.TrimSpace(record.Metadata.CWD)})
	for _, input := range items {
		raw, ok := input.(map[string]any)
		if !ok || strings.TrimSpace(stringFromAny(raw["role"])) != contextfrag.RoleDeveloper {
			continue
		}
		if isRetainedPersistentModeText(textFromInputItemContent(raw["content"])) {
			return true
		}
	}
	return false
}

func isRetainedPersistentModeText(text string) bool {
	return strings.Contains(text, "<persistent_mode>") && strings.Contains(text, "</persistent_mode>")
}

// persistentModeWorldStateSessionItemForTurn keeps emitted persistent-mode
// guidance in the thread history without exposing it as ordinary chat content.
func persistentModeWorldStateSessionItemForTurn(turnID string, input any, createdAt time.Time) (session.Item, bool) {
	raw, ok := input.(map[string]any)
	if !ok || strings.TrimSpace(stringFromAny(raw["type"])) != "message" {
		return session.Item{}, false
	}
	role := strings.TrimSpace(stringFromAny(raw["role"]))
	text := strings.TrimSpace(textFromInputItemContent(raw["content"]))
	if role != contextfrag.RoleDeveloper || !isRetainedPersistentModeText(text) {
		return session.Item{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return session.Item{}, false
	}
	metadata := appTurnMetadata(turnID, map[string]any{
		"kind":             persistentModeWorldStateInstructionsKind,
		"hiddenFromThread": true,
	})
	return session.Item{
		ID:        "persistent-mode-" + safeIdentifier(turnID),
		Type:      "message",
		Role:      role,
		Text:      text,
		Content:   []session.ContentPart{{Type: "input_text", Text: text}},
		CreatedAt: createdAt,
		Data: map[string]any{
			"kind":             persistentModeWorldStateInstructionsKind,
			"hiddenFromThread": true,
		},
		Metadata: metadata,
		Raw:      encoded,
	}, true
}
