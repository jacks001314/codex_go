package appserver

import (
	"testing"

	"codex_go/audioutil"
	"codex_go/turn"
)

// TestInputContentFromTurnUserInputsPreparesAudioLikeRust mirrors the Rust
// utils/audio preparation test: supported data URLs are canonicalized, while
// unsupported or unusable audio becomes placeholder text.
func TestInputContentFromTurnUserInputsPreparesAudioLikeRust(t *testing.T) {
	inputs := []turn.TurnUserInput{
		{Type: "audio", URL: "data:audio/x-wav;base64,YXVkaW8="},
		{Type: "audio", URL: "data:audio/ogg;base64,YXVkaW8="},
		{Type: "audio", URL: "https://example.com/audio.mp3"},
		{Type: "audio", URL: "data:audio/flac;base64,YXVkaW8="},
	}
	content := inputContentFromTurnUserInputs("", inputs)
	want := []map[string]any{
		{"type": "input_audio", "audio_url": "data:audio/wav;base64,YXVkaW8="},
		{"type": "input_audio", "audio_url": "data:audio/ogg;base64,YXVkaW8="},
		{"type": "input_text", "text": audioutil.PlaceholderProcessingError},
		{"type": "input_text", "text": audioutil.PlaceholderUnsupported},
	}
	if len(content) != len(want) {
		t.Fatalf("content length = %d, want %d (%v)", len(content), len(want), content)
	}
	for index := range want {
		if len(content[index]) != len(want[index]) {
			t.Fatalf("content[%d] = %v, want %v", index, content[index], want[index])
		}
		for key, value := range want[index] {
			if content[index][key] != value {
				t.Fatalf("content[%d][%q] = %v, want %v", index, key, content[index][key], value)
			}
		}
	}
}
