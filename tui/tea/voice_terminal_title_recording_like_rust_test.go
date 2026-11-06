package tea

import (
	"strings"
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: #43651 (4b0d9669cc) status_surfaces.rs: while the realtime
// microphone is listening the terminal title's activity indicator carries a
// recording dot, and the dot is dropped once capture stops.
func TestVoiceTerminalTitleCarriesRecordingDotLikeRust(t *testing.T) {
	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	if !model.voiceMicrophoneListening() {
		t.Fatal("an active unmuted conversation on this thread is not listening")
	}

	var writes []string
	model.terminalTitleWriter = func(sequence string) bubbletea.Cmd {
		writes = append(writes, sequence)
		return nil
	}
	result := chatwidget.StatusSurfaceRefreshResult{TerminalTitleRendered: true, TerminalTitleText: "project"}
	model.applyTerminalTitleResult(result)
	if len(writes) != 1 || !strings.Contains(writes[0], "\u25cf project") {
		t.Fatalf("recording title writes = %#v", writes)
	}

	// Muting stops capture, so the dot disappears on the next refresh.
	model.VoiceConversation.ToggleVoiceMicrophone()
	model.applyTerminalTitleResult(result)
	if len(writes) != 2 || strings.Contains(writes[1], "\u25cf") || !strings.Contains(writes[1], "project") {
		t.Fatalf("muted title writes = %#v", writes)
	}
	if model.voiceMicrophoneListening() {
		t.Fatal("a muted conversation reported listening")
	}
}

// Rust renders a bare marker when the title has no other activity text, and a
// conversation parked on another thread never marks the title.
func TestVoiceTerminalTitleMarkerScopeLikeRust(t *testing.T) {
	if got := chatwidget.RecordingTerminalTitle("", true); got != chatwidget.TerminalTitleRecordingMarker {
		t.Fatalf("empty recording title = %q", got)
	}
	if got := chatwidget.RecordingTerminalTitle("project", false); got != "project" {
		t.Fatalf("idle title = %q", got)
	}

	model, _, _ := newVoiceTestModel(t)
	model.realtimeHelperAttached = true
	model.VoiceConversation.MarkVoiceBackendStarted()
	model.VoiceConversation.MarkVoiceWebRTCConnected()
	model.State.SetThreadID("another-thread")
	if model.voiceMicrophoneListening() {
		t.Fatal("a background conversation marked the current thread's title")
	}
}
