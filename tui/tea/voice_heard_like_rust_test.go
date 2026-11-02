package tea

import (
	"strings"
	"testing"
	"time"

	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: #43651 (4b0d9669cc): a spoken user caption interrupts the
// assistant, briefly shows "heard", and clears the speaker meter samples.
func TestModelVoiceStripShowsHeardAfterInterruptionLikeRust(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	clock := time.Unix(1_700_000_000, 0)
	model.now = func() time.Time { return clock }

	model.VoiceConversation.SpeakerPeak = chatwidget.VoiceMeterFullScale
	model.speakerActiveUntil = clock.Add(chatwidget.VoiceSpeakerActivityHold)
	model.pushVoiceMeterSample(0, chatwidget.VoiceMeterFullScale)
	if lines := model.voiceStripLines(80); len(lines) < 2 || !strings.Contains(lines[0], "speaking") {
		t.Fatalf("pre-interruption strip = %#v", lines)
	}

	model.VoiceConversation.ApplyVoiceTranscriptDone(chatwidget.VoiceTranscriptUser, "hold on a moment")
	model.refreshVoiceTranscript()

	if got := model.voiceStripActivityLabel(); got != "heard" {
		t.Fatalf("activity after the interruption = %q, want heard", got)
	}
	if !model.speakerActiveUntil.IsZero() {
		t.Fatalf("speaker hold survived the interruption: %v", model.speakerActiveUntil)
	}
	if model.VoiceConversation.SpeakerPeak != 0 {
		t.Fatalf("speaker peak survived the interruption: %d", model.VoiceConversation.SpeakerPeak)
	}
	if last := model.audioMeterHistory[len(model.audioMeterHistory)-1]; last.speaker != 0 {
		t.Fatalf("speaker meter sample = %d, want silence", last.speaker)
	}

	// The acknowledgement is transient; the strip returns to listening.
	clock = clock.Add(chatwidget.VoiceInterruptionAcknowledgment)
	if got := model.voiceStripActivityLabel(); got != "listening" {
		t.Fatalf("activity after the acknowledgement = %q, want listening", got)
	}
}

// Typed input also supersedes a voice answer, so the speaker samples clear
// through the same chatwidget boundary (Rust InvalidateVoiceInputForTyping).
func TestModelVoiceTypedInputSuppressesSpeakerLikeRust(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	now := time.Unix(1_700_000_000, 0)
	model.now = func() time.Time { return now }
	model.VoiceConversation.SpeakerPeak = chatwidget.VoiceMeterFullScale
	model.speakerActiveUntil = now.Add(chatwidget.VoiceSpeakerActivityHold)

	if action := model.VoiceConversation.InvalidateVoiceInputForTyping(); action != chatwidget.VoiceSpeakerMute {
		t.Fatalf("typed-input action = %q, want %q", action, chatwidget.VoiceSpeakerMute)
	}
	if model.VoiceConversation.SpeakerLevel != 0 || !model.VoiceConversation.SpeakerActiveUntil.IsZero() {
		t.Fatal("typed input left the speaker active")
	}
}
