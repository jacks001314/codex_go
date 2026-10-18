package tea

import (
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/chatwidget"
)

func TestVoiceConversationSurvivesThreadNavigationLikeRust(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	model.VoiceConversation.Accepted = []chatwidget.VoiceTranscriptRecord{{
		Role: "assistant",
		Text: "spoken on the voice thread",
	}}

	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-other",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "thread-other"},
			Status: "idle",
		},
	})
	if !model.VoiceConversation.Running() || model.VoiceConversation.ThreadID != "thread-voice" {
		t.Fatalf("voice after thread switch = %#v", model.VoiceConversation)
	}

	model.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-voice",
		Response: AgentThreadSwitchResponse{
			Entry:  codextui.AgentThreadEntry{ThreadID: "thread-voice"},
			Status: "idle",
		},
	})
	if !model.VoiceConversation.Running() || model.VoiceConversation.ThreadID != "thread-voice" {
		t.Fatalf("voice after returning = %#v", model.VoiceConversation)
	}
}
