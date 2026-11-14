package appserver

import (
	"strings"

	"codex_go/turn"
)

// usedDictationMetadataKey is the per-input provenance marker carried in
// responsesapiClientMetadata. Rust removes it before building the Responses
// request and records it on the accepted user input instead (#61215).
const usedDictationMetadataKey = "codex_app_used_dictation"

type turnDictationState uint8

const (
	turnDictationNoUserInput turnDictationState = iota
	turnDictationUnknown
	turnDictationKnownFalse
	turnDictationKnownTrue
)

// captureTurnStartDictation extracts the marker from both metadata forms. A
// patch wins over a replacement, matching Rust's apply order. The marker never
// remains in either map, including when its value is malformed.
func captureTurnStartDictation(params *turn.TurnStartParams) {
	if params == nil {
		return
	}
	used, ok := extractUsedDictation(params.ResponsesAPIMetadata, params.ResponsesAPIMetadataPatch)
	if ok && turnStartHasUserInput(params) {
		params.UsedDictation = used
	}
}

func captureTurnSteerDictation(params *turn.TurnSteerParams) {
	if params == nil {
		return
	}
	used, ok := extractUsedDictation(params.ResponsesAPIMetadata, params.ResponsesAPIMetadataPatch)
	if ok && (strings.TrimSpace(params.Prompt) != "" || len(params.Input) > 0) {
		params.UsedDictation = used
	}
}

func extractUsedDictation(replacement, patch map[string]string) (*bool, bool) {
	raw, found := patch[usedDictationMetadataKey]
	if !found {
		raw, found = replacement[usedDictationMetadataKey]
	}
	delete(replacement, usedDictationMetadataKey)
	delete(patch, usedDictationMetadataKey)
	if !found {
		return nil, false
	}
	// bool::from_str in Rust accepts exactly these two spellings.
	switch raw {
	case "true":
		value := true
		return &value, true
	case "false":
		value := false
		return &value, true
	default:
		return nil, true
	}
}

func turnStartHasUserInput(params *turn.TurnStartParams) bool {
	if params == nil || params.ToolOutput != nil {
		return false
	}
	return strings.TrimSpace(params.Prompt) != "" || len(params.Input) > 0
}

func mergeTurnDictation(state turnDictationState, value *bool) turnDictationState {
	if state == turnDictationKnownTrue {
		return state
	}
	if value == nil {
		return turnDictationUnknown
	}
	if *value {
		return turnDictationKnownTrue
	}
	if state == turnDictationUnknown {
		return turnDictationUnknown
	}
	return turnDictationKnownFalse
}

func (state turnDictationState) analyticsValue() *bool {
	if state != turnDictationKnownFalse && state != turnDictationKnownTrue {
		return nil
	}
	value := state == turnDictationKnownTrue
	return &value
}
