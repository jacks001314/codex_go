package appserver

import (
	"encoding/json"
	"errors"
	"strings"

	"codex_go/turn"
)

// unsupportedUploadedAudioMessage mirrors Rust #59234
// unsupported_uploaded_audio_error (CodexErr::UnsupportedOperation): uploaded
// audio is accepted for audio-capable models, and a model without audio input
// reports this error after the accompanying text is recorded.
const unsupportedUploadedAudioMessage = "The selected model does not support uploaded audio. Choose an audio-capable model or remove the attachment."

// errUnsupportedUploadedAudio mirrors Rust #59234 unsupported_uploaded_audio_error.
// The turn runner reports it after recording the accompanying text, so the turn
// completes as failed instead of the RPC being refused.
var errUnsupportedUploadedAudio = errors.New(unsupportedUploadedAudioMessage)

// turnStartParamsWithoutUploadedAudio returns a copy of the turn parameters
// whose input drops uploaded audio file references, mirroring Rust
// run_hooks_and_record_inputs: the accompanying text and every other input are
// still recorded before the turn fails. It reports whether any uploaded audio
// was present.
func turnStartParamsWithoutUploadedAudio(params *turn.TurnStartParams) (*turn.TurnStartParams, bool) {
	if params == nil {
		return nil, false
	}
	input, rejected := turnUserInputsWithoutUploadedAudio(params.Input)
	if !rejected {
		return params, false
	}
	filtered := *params
	filtered.Input = input
	return &filtered, true
}

// turnUserInputsWithoutUploadedAudio drops uploaded audio file references and
// reports whether any were present. Rust #58430 keeps the remaining content so
// the accompanying text is still recorded before the turn fails.
func turnUserInputsWithoutUploadedAudio(inputs []turn.TurnUserInput) ([]turn.TurnUserInput, bool) {
	filtered := make([]turn.TurnUserInput, 0, len(inputs))
	rejected := false
	for i := range inputs {
		if turnUserInputAudioFile(inputs[i]) {
			rejected = true
			continue
		}
		filtered = append(filtered, inputs[i])
	}
	if !rejected {
		return inputs, false
	}
	return filtered, true
}

// validateSteerInputAudioLikeRust mirrors Rust run_turn: steered input carrying
// an uploaded audio file is rejected after it is recorded when the model has no
// audio input, so the running turn fails instead of the steer being refused.
func validateSteerInputAudioLikeRust(items []any) error {
	for _, item := range items {
		if valueContainsUploadedAudioFile(item) {
			return errUnsupportedUploadedAudio
		}
	}
	return nil
}

// uploadedAudioInjectionMessage mirrors Rust validate_injected_response_item
// (CodexErr::InvalidRequest).
const uploadedAudioInjectionMessage = "Uploaded audio files must be submitted as turn input."

// turnUserInputAudioFile reports whether the input carries an uploaded audio
// file reference instead of an inline payload (Rust AudioReference::File). The
// untagged wire shape prefers the inline arm, so a non-empty url wins over a
// stray fileId.
func turnUserInputAudioFile(input turn.TurnUserInput) bool {
	if !strings.EqualFold(strings.TrimSpace(input.Type), "audio") {
		return false
	}
	return strings.TrimSpace(input.URL) == "" && strings.TrimSpace(input.FileID) != ""
}


// validateResponseItemAudioFiles rejects raw history injection that contains an
// uploaded audio reference, mirroring Rust validate_injected_response_item: the
// whole item is rejected because typed turn input owns admission checks.
func validateResponseItemAudioFiles(items []json.RawMessage) error {
	for i := range items {
		var value any
		if err := json.Unmarshal(items[i], &value); err != nil {
			return err
		}
		if valueContainsUploadedAudioFile(value) {
			return jsonRPCInvalidRequest(uploadedAudioInjectionMessage)
		}
	}
	return nil
}

func valueContainsUploadedAudioFile(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		itemType := strings.TrimSpace(stringFromAny(typed["type"]))
		if itemType == "input_audio" || itemType == "inputAudio" || itemType == "audio" {
			if strings.TrimSpace(stringFromAny(typed["audio_url"])) == "" &&
				strings.TrimSpace(stringFromAny(typed["audioUrl"])) == "" &&
				strings.TrimSpace(stringFromAny(typed["url"])) == "" {
				if strings.TrimSpace(stringFromAny(typed["file_id"])) != "" ||
					strings.TrimSpace(stringFromAny(typed["fileId"])) != "" {
					return true
				}
			}
		}
		for _, nested := range typed {
			if valueContainsUploadedAudioFile(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if valueContainsUploadedAudioFile(nested) {
				return true
			}
		}
	case []map[string]any:
		// Message content keeps its typed shape ([]map[string]any) after image
		// preparation, so the steer validator has to descend into it too.
		for _, nested := range typed {
			if valueContainsUploadedAudioFile(nested) {
				return true
			}
		}
	}
	return false
}
