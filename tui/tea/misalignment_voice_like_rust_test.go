package tea

import (
	"testing"

	"codex_go/tui/chatwidget"
)

// Rust #43581: a misalignment policy violation stops the live voice session
// before the chat is cleared, and retiring the helper restores answers that
// never reached the conversation.
func TestModelMisalignmentStopsVoiceLikeRust(t *testing.T) {
	model, _, closes := newVoiceTestModel(t)
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	model.realtimeHelperAttached = true

	model.applyMisalignmentPolicyViolation(`{"error":{"code":"misalignment_policy_violation","message":"policy"}}`)

	if *closes != 1 {
		t.Fatalf("retired helpers = %d, want 1", *closes)
	}
	if model.VoiceConversation.Phase != chatwidget.VoicePhaseStopping {
		t.Fatalf("voice phase after the violation = %q, want stopping", model.VoiceConversation.Phase)
	}
	if model.realtimeHelperAttached {
		t.Fatal("the local helper stayed attached after the violation")
	}
	if !model.misalignmentPolicyStopped {
		t.Fatal("the chat must stay stopped after the violation")
	}
}

// An idle voice session has nothing to retire, so the violation must not send
// a stop request for a conversation that never started.
func TestModelMisalignmentLeavesIdleVoiceAlone(t *testing.T) {
	model, _, closes := newVoiceTestModel(t)
	model.VoiceConversation.ResetVoiceConversation()

	model.applyMisalignmentPolicyViolation("misalignment_policy_violation")

	if *closes != 0 {
		t.Fatalf("an idle violation retired the helper %d times", *closes)
	}
	if !model.misalignmentPolicyStopped {
		t.Fatal("the chat must stay stopped after the violation")
	}
}
