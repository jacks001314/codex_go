package compact

import "testing"

// Mirrors Rust #58430 estimate_audio_reference_bytes: uploaded file durations
// are unknown locally, so the Responses 20-minute maximum is budgeted at the
// existing 10 audio tokens per second.
func TestFileBackedAudioUsesMaximumDurationBudgetLikeRust(t *testing.T) {
	if FileAudioTokensEstimate != 20*60*10 {
		t.Fatalf("file audio token estimate = %d, want 12000", FileAudioTokensEstimate)
	}
	item := Item{
		Type: "message",
		Role: "user",
		Content: []ContentPart{
			{Type: "input_audio", FileID: "file_audio"},
		},
	}
	if got := EstimateItemTokens(&item); got != FileAudioTokensEstimate {
		t.Fatalf("file-backed audio tokens = %d, want %d", got, FileAudioTokensEstimate)
	}
	// Mixed inline and file-backed audio keep their positions and each block is
	// charged: the file reference adds the conservative maximum on top of text.
	mixed := Item{
		Type: "message",
		Role: "user",
		Content: []ContentPart{
			{Type: "input_text", Text: "listen"},
			{Type: "input_audio", FileID: "file_audio"},
			{Type: "input_text", Text: "then this"},
		},
	}
	if got := EstimateItemTokens(&mixed); got <= FileAudioTokensEstimate {
		t.Fatalf("mixed audio tokens = %d, want more than %d", got, FileAudioTokensEstimate)
	}
}
