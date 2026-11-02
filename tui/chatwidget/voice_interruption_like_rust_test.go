package chatwidget

import (
	"testing"
	"time"
)

// Rust parity: #43651 (4b0d9669cc) recording_controls_tests: a user caption
// that interrupts speech clears the speaker level and its activity hold and
// acknowledges the interruption for 400 ms.
func TestAcknowledgeVoiceInterruptionClearsSpeakerLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-1", 3)
	state.MarkVoiceBackendStarted()
	state.MarkVoiceWebRTCConnected()
	state.SpeakerLevel = 180
	state.SpeakerActiveUntil = now.Add(VoiceSpeakerActivityHold)

	if action := state.AcknowledgeVoiceInterruption(now); action != VoiceSpeakerMute {
		t.Fatalf("interruption action = %q, want %q", action, VoiceSpeakerMute)
	}
	if state.SpeakerLevel != 0 || !state.SpeakerActiveUntil.IsZero() {
		t.Fatalf("speaker samples survived the interruption: level=%d hold=%v", state.SpeakerLevel, state.SpeakerActiveUntil)
	}
	if !state.VoiceInterruptionAcknowledged(now) {
		t.Fatal("the interruption was not acknowledged")
	}
	if state.VoiceInterruptionAcknowledged(now.Add(VoiceInterruptionAcknowledgment)) {
		t.Fatal("the acknowledgement outlived its window")
	}
}

// A quiet turn keeps the speaker ready (Rust #44925) and must not acknowledge
// an interruption that never happened.
func TestAcknowledgeVoiceInterruptionIgnoresQuietTurnLikeRust(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	var state VoiceConversationState
	state.BeginVoiceConversation("thread-2", 4)
	state.MarkVoiceBackendStarted()
	state.MarkVoiceWebRTCConnected()

	if action := state.AcknowledgeVoiceInterruption(now); action != VoiceSpeakerNone {
		t.Fatalf("quiet action = %q, want none", action)
	}
	if state.VoiceInterruptionAcknowledged(now) {
		t.Fatal("a quiet turn acknowledged an interruption")
	}
}
