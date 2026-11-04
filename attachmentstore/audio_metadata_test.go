package attachmentstore

import "testing"

func uint64PtrForAudioMetadataTest(value uint64) *uint64 { return &value }

// TestAudioFormatSpecificMetadataLikeRust pins Rust #59268: an audio attachment
// reports its measured duration through the tagged format-specific metadata.
func TestAudioFormatSpecificMetadataLikeRust(t *testing.T) {
	metadata := AudioFormatSpecificMetadata(&AudioMetadata{DurationMs: uint64PtrForAudioMetadataTest(1500)})
	if metadata.Kind != "audio" {
		t.Fatalf("kind = %q, want audio", metadata.Kind)
	}
	if got := metadata.AudioOrZero(); got.DurationMs == nil || *got.DurationMs != 1500 {
		t.Fatalf("duration = %v, want 1500ms", got.DurationMs)
	}
	if got := metadata.Debug(); got != "Audio(AudioMetadata { duration_ms: Some(1500) })" {
		t.Fatalf("debug = %q", got)
	}
	if got := AudioFormatSpecificMetadata(nil).Debug(); got != "Audio(AudioMetadata { duration_ms: None })" {
		t.Fatalf("empty debug = %q", got)
	}
	attachment := AttachmentMetadata{FormatSpecific: metadata}
	if got := attachment.FormatSpecific.AudioOrZero(); got.DurationMs == nil || *got.DurationMs != 1500 {
		t.Fatalf("attachment duration = %v", got.DurationMs)
	}
}
