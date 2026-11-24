package realtime

// Mirrors Rust #62299: speech captured in a call that never reached a turn is
// saved as session-end context when the call ends, a consumed handoff leaves
// nothing pending, and restored speech precedes newly captured speech.

import "testing"

func TestSessionClosedKeepsPendingSpeechLikeRust(t *testing.T) {
	history := &RealtimeHistoryState{}
	history.SetFramesEnabled(true)
	history.StartSession("session-1")
	history.TranscriptDelta(RealtimeTranscriptRoleUser, "unfinished thought")
	closed := history.SessionClosed()
	if len(closed.Items) != 3 {
		t.Fatalf("closed items = %#v", closed.Items)
	}
	receipt := closed.Items[1]
	if receipt.Content.Kind != RealtimeItemKindSessionInput || receipt.Content.InputReason != RealtimeSessionInputReasonSessionEnd {
		t.Fatalf("pending speech receipt = %#v", receipt.Content)
	}
	if receipt.Content.Input != "unfinished thought" || receipt.ObservedAtMS == nil {
		t.Fatalf("receipt text/observation = %#v", receipt)
	}
	if closed.Items[2].Content.Kind != RealtimeItemKindSessionClosed {
		t.Fatalf("closing marker = %#v", closed.Items[2])
	}
}

func TestSessionClosedSkipsConsumedSpeechLikeRust(t *testing.T) {
	history := &RealtimeHistoryState{}
	history.SetFramesEnabled(true)
	history.StartSession("session-1")
	history.TranscriptDelta(RealtimeTranscriptRoleUser, "go fix the test")
	// A handoff claimed the speech and the receiving turn consumed it.
	if !history.ReserveHandoff("h1") {
		t.Fatal("handoff did not claim the speech")
	}
	history.BindTurnSession("turn-1")
	closed := history.SessionClosed()
	for _, item := range closed.Items {
		if item.Content.Kind == RealtimeItemKindSessionInput {
			t.Fatalf("consumed speech was saved again: %#v", item.Content)
		}
	}
}

func TestSeedSessionEndSpeechPrecedesNewSpeechLikeRust(t *testing.T) {
	history := &RealtimeHistoryState{}
	history.SetFramesEnabled(true)
	history.StartSession("session-1")
	observed := int64(1_700)
	// Without an open session a seed has nowhere to go.
	if (&RealtimeHistoryState{}).SeedSessionEndSpeech("persisted", &observed) {
		t.Fatal("seeded speech without an open session")
	}
	if !history.SeedSessionEndSpeech("persisted ", &observed) {
		t.Fatal("seed rejected an empty session")
	}
	if history.SeedSessionEndSpeech("again", &observed) {
		t.Fatal("seed replaced speech already captured")
	}
	history.TranscriptDelta(RealtimeTranscriptRoleUser, "new speech")
	closed := history.SessionClosed()
	var sealed *RealtimeItem
	for index := range closed.Items {
		content := closed.Items[index].Content
		if content.Role == RealtimeTranscriptRoleUser && content.Text != "" {
			sealed = &closed.Items[index]
			break
		}
	}
	if sealed == nil || sealed.Content.Text != "persisted new speech" {
		t.Fatalf("sealed speech = %#v", sealed)
	}
}
