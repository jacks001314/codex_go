package appserver

import (
	"strings"

	"codex_go/audioutil"
)

// audioFileInputContentBlock is the uploaded-file arm of `input_audio` (Rust
// #58430 `AudioReference::File { file_id }`). The runtime rejects it before
// inference, so it never reaches the Responses API.
func audioFileInputContentBlock(fileID string) map[string]any {
	return map[string]any{"type": "input_audio", "file_id": strings.TrimSpace(fileID)}
}

// audioInputContentBlock canonicalizes one audio data URL into a model
// input_audio block. Unusable audio is replaced with the Rust placeholder text
// so the model still receives the turn, mirroring
// codex_utils_audio::prepare_response_items.
func audioInputContentBlock(audioURL string) map[string]any {
	canonical, err := audioutil.PrepareAudioURL(strings.TrimSpace(audioURL))
	if err != nil {
		return map[string]any{"type": "input_text", "text": audioutil.Placeholder(err)}
	}
	return map[string]any{"type": "input_audio", "audio_url": canonical}
}
