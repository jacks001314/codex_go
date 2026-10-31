package appserver

import (
	"encoding/json"
	"strings"

	"codex_go/turn"
)

// uploadedAudioDisabledMessage mirrors Rust #58430 uploaded_audio_disabled_error
// (CodexErr::UnsupportedOperation): this build has no runtime integration for
// uploaded audio, so file-backed audio must be rejected explicitly instead of
// being forwarded to the Responses API.
const uploadedAudioDisabledMessage = "Uploaded audio file inputs are not enabled in this build."

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

// validateTurnUserInputAudioFiles rejects uploaded audio before a turn starts,
// steers, or is queued (Rust #58430 keeps uploaded audio disabled at runtime).
func validateTurnUserInputAudioFiles(inputs []turn.TurnUserInput) error {
	for i := range inputs {
		if turnUserInputAudioFile(inputs[i]) {
			return jsonRPCInvalidRequest(uploadedAudioDisabledMessage)
		}
	}
	return nil
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
	}
	return false
}
