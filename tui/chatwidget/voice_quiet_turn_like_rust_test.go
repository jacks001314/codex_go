package chatwidget

import (
	"testing"
	"time"
)

// Rust #44925: a new user caption on a quiet turn must not suppress response
// audio that may arrive before captions.
func TestVoiceQuietTurnDoesNotSuppressSpeakerLikeRust(t *testing.T) {
	now := time.Unix(100, 0)
	delta := VoiceConversationState{Phase: VoicePhaseActive}
	if action := delta.VoiceCaptionDelta(VoiceTranscriptUser, "hello", true, now); action != VoiceSpeakerNone {
		t.Fatalf("quiet delta speaker action = %q, want none", action)
	}
	if delta.SpeakerSuppressionGeneration != nil || delta.TranscriptInputGeneration == nil || delta.InputGeneration != 1 {
		t.Fatalf("quiet delta state = %#v", delta)
	}

	finalOnly := VoiceConversationState{Phase: VoicePhaseActive}
	if action := finalOnly.VoiceCaptionDone(VoiceTranscriptUser, "hello", true, now); action != VoiceSpeakerNone {
		t.Fatalf("quiet final-only speaker action = %q, want none", action)
	}
	if finalOnly.SpeakerSuppressionGeneration != nil || !finalOnly.LatestInputWasVoice {
		t.Fatalf("quiet final-only state = %#v", finalOnly)
	}
}

func TestVoiceUserCaptionInterruptsOlderOutputLikeRust(t *testing.T) {
	now := time.Unix(100, 0)
	generation := uint64(1)
	tests := []struct {
		name  string
		state VoiceConversationState
	}{
		{
			name:  "assistant caption",
			state: VoiceConversationState{Phase: VoicePhaseActive, AssistantTranscriptGeneration: &generation},
		},
		{
			name: "pending speech",
			state: VoiceConversationState{
				Phase:         VoicePhaseActive,
				PendingSpeech: []PendingVoiceSpeech{{ItemID: "answer-1", Text: "older answer"}},
			},
		},
		{
			name:  "speaker level",
			state: VoiceConversationState{Phase: VoicePhaseActive, SpeakerLevel: 10},
		},
		{
			name:  "speaker active window",
			state: VoiceConversationState{Phase: VoicePhaseActive, SpeakerActiveUntil: now.Add(time.Second)},
		},
		{
			name:  "already suppressed",
			state: VoiceConversationState{Phase: VoicePhaseActive, SpeakerSuppressionGeneration: &generation},
		},
	}
	for _, testCase := range tests {
		state := testCase.state
		if action := state.VoiceCaptionDelta(VoiceTranscriptUser, "hello", true, now); action != VoiceSpeakerMute {
			t.Fatalf("%s: speaker action = %q, want mute", testCase.name, action)
		}
		if state.SpeakerSuppressionGeneration == nil || *state.SpeakerSuppressionGeneration != state.InputGeneration {
			t.Fatalf("%s: suppression generation = %#v input=%d", testCase.name, state.SpeakerSuppressionGeneration, state.InputGeneration)
		}
	}
}

// Typed input still supersedes voice output unconditionally.
func TestTypedInputStillSuppressesVoiceSpeakerLikeRust(t *testing.T) {
	state := VoiceConversationState{Phase: VoicePhaseActive}
	if action := state.InvalidateVoiceInputForTyping(); action != VoiceSpeakerMute {
		t.Fatalf("typed input action = %q, want mute", action)
	}
	if state.SpeakerSuppressionGeneration == nil {
		t.Fatal("typed input did not record suppression")
	}
}
