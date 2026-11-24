package guardiancontext

import (
	"testing"
)

// TestCollectAudioTranscriptLikeRust pins Rust #63368's collection rules: a
// completed user-turn transcription becomes its own entry whose role names the
// originating turn, tool audio is admitted only when tool outputs are included,
// and blank transcriptions are dropped.
func TestCollectAudioTranscriptLikeRust(t *testing.T) {
	user := CollectAudioTranscript(AudioTranscript{
		Source: AudioTranscriptUserInput,
		TurnID: "turn-1",
		Text:   "ship the release",
	}, ConversationTranscriptOptions{})
	if user == nil {
		t.Fatal("user audio transcript was dropped")
	}
	if got, want := user.Kind.Role(), `audio transcript (recording submitted on user turn "turn-1")`; got != want {
		t.Fatalf("user audio role = %q, want %q", got, want)
	}
	if user.Text != "ship the release" || user.OriginalBytes != len("ship the release") {
		t.Fatalf("user audio entry = %#v", user)
	}
	if user.Kind.Role() == UserKind().Role() {
		t.Fatal("user audio transcript shares the typed user message role")
	}

	toolAudio := AudioTranscript{Source: AudioTranscriptToolOutput, CallID: "call-9", Text: "done at noon"}
	if got := CollectAudioTranscript(toolAudio, ConversationTranscriptOptions{}); got != nil {
		t.Fatalf("tool audio transcript ignored the tool-output filter: %#v", got)
	}
	tool := CollectAudioTranscript(toolAudio, ConversationTranscriptOptions{IncludeToolOutputs: true})
	if tool == nil {
		t.Fatal("tool audio transcript was dropped with tool outputs included")
	}
	if got, want := tool.Kind.Role(), `tool audio transcript (call "call-9")`; got != want {
		t.Fatalf("tool audio role = %q, want %q", got, want)
	}

	for name, transcript := range map[string]AudioTranscript{
		"blank user audio": {Source: AudioTranscriptUserInput, TurnID: "t", Text: "   \n\t"},
		"empty user audio": {Source: AudioTranscriptUserInput, TurnID: "t"},
		"blank tool audio": {Source: AudioTranscriptToolOutput, CallID: "c", Text: " "},
	} {
		options := ConversationTranscriptOptions{IncludeToolOutputs: true}
		if got := CollectAudioTranscript(transcript, options); got != nil {
			t.Fatalf("%s produced %#v", name, got)
		}
	}
}

// TestAudioTranscriptRoleEscapesLikeRust keeps a quoted identifier from
// breaking the role label a line transcript renders.
func TestAudioTranscriptRoleEscapesLikeRust(t *testing.T) {
	if got, want := UserAudioTranscriptKind(`turn-"quoted"`).Role(), `audio transcript (recording submitted on user turn "turn-\"quoted\"")`; got != want {
		t.Fatalf("role = %q, want %q", got, want)
	}
	if got, want := ToolAudioTranscriptKind(`call\one`).Role(), `tool audio transcript (call "call\\one")`; got != want {
		t.Fatalf("tool role = %q, want %q", got, want)
	}
}
