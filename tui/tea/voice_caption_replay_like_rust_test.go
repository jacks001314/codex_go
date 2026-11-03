package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

// Rust #44749: a completed voice caption recorded before a later typed turn is
// restored before that turn when the thread is switched back.
func TestRetainedVoiceCaptionsReplayBeforeLaterTurnsLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.State.SetThreadID("thread-a")
	model.VoiceConversation.ThreadID = "thread-a"
	model.VoiceConversation.Accepted = []chatwidget.VoiceTranscriptRecord{
		{Role: chatwidget.VoiceTranscriptUser, Text: "spoken prompt", Complete: true},
	}
	model.refreshVoiceTranscript()
	model.State.AddMessage(codextui.RoleUser, "typed after")

	restored := model.restoreRetainedVoiceCaptions("thread-a", []codextui.Message{
		{Role: codextui.RoleUser, Text: "typed after"},
	})
	// The restored caption keeps its spoken presentation, so compare the
	// visible text rather than the styled cell output.
	visible := ""
	if len(restored) > 0 {
		visible = strings.TrimSpace(utils.StripANSI(restored[0].Text))
	}
	if len(restored) != 2 || !strings.Contains(visible, "spoken prompt") || restored[1].Text != "typed after" {
		t.Fatalf("restored messages = %#v, want caption before typed turn", restored)
	}
	if strings.Contains(visible, "User (voice)") {
		t.Fatalf("restored caption used the emoji preview cell: %q", visible)
	}

	// Repeated switches must not duplicate the retained caption.
	again := model.restoreRetainedVoiceCaptions("thread-a", []codextui.Message{
		{Role: codextui.RoleUser, Text: "typed after"},
	})
	if len(again) != 2 {
		t.Fatalf("repeated restore messages = %#v", again)
	}
}

// Captions from another thread must not leak into the selected history.
func TestRetainedVoiceCaptionsRemainThreadScopedLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 36})
	model.retainVoiceCaptionForThread("thread-a", chatwidget.VoiceTranscriptRecord{
		Role: chatwidget.VoiceTranscriptUser, Text: "thread a caption", Complete: true,
	}, 0)
	if got := model.restoreRetainedVoiceCaptions("thread-b", nil); len(got) != 0 {
		t.Fatalf("thread b inherited captions: %#v", got)
	}
}
