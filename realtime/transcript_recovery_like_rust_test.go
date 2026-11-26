package realtime

// Mirrors Rust #62288 recover_session and prepare_recorded_chunk: a V3
// reconnect replays only durable speech no input consumed, in persisted order,
// ahead of newly captured speech.

import "testing"

func recoveryFrame(id string, role RealtimeTranscriptRole, text string) RealtimeItem {
	observed := int64(1)
	return RealtimeItem{
		ID:                id,
		RealtimeSessionID: "call",
		ObservedAtMS:      &observed,
		Content: RealtimeItemContent{
			Kind: RealtimeItemKindFrame,
			Role: role,
			Text: text,
		},
	}
}

func TestRecoveredV3SpeechExcludesConsumedFramesAndPrecedesNewChunksLikeRust(t *testing.T) {
	started := RealtimeItem{ID: "start", RealtimeSessionID: "call", Content: RealtimeItemContent{Kind: RealtimeItemKindSessionStarted}}
	items := []RealtimeItem{
		started,
		recoveryFrame("a1", RealtimeTranscriptRoleUser, "first"),
		recoveryFrame("b1", RealtimeTranscriptRoleAssistant, "pending"),
		recoveryFrame("a2", RealtimeTranscriptRoleUser, "last"),
		{
			ID:                "chunk",
			RealtimeSessionID: "call",
			Content: RealtimeItemContent{
				Kind:         RealtimeItemKindSessionInput,
				InputReason:  RealtimeSessionInputReasonChunk,
				SourceEvents: []string{"a1", "a2"},
				InputItemID:  "input",
				Input:        "first last",
			},
		},
	}
	var state RealtimeHistoryState
	if status := state.RecoverSession("call", items); status != RealtimeSessionRecoveryOpen {
		t.Fatalf("recovered status = %q", status)
	}
	if ids := state.RecoveredFrameIDs(); len(ids) != 1 || ids[0] != "b1" {
		t.Fatalf("recovered frames = %#v", ids)
	}
	if !state.PreserveTranscript() {
		t.Fatal("recovered session did not preserve its transcript order")
	}
	if state.ActiveSessionID() != "call" {
		t.Fatalf("active session = %q", state.ActiveSessionID())
	}
	combined := state.PrepareRecordedEntries([]TranscriptEntry{{Role: "user", Text: "new", ID: "new-1"}})
	if len(combined) != 2 {
		t.Fatalf("combined entries = %#v", combined)
	}
	if combined[0].ID != "b1" || combined[0].Role != "assistant" || combined[0].Text != "pending" {
		t.Fatalf("recovered row = %#v", combined[0])
	}
	if combined[1].ID != "new-1" || combined[1].Text != "new" {
		t.Fatalf("new row = %#v", combined[1])
	}
	if consumed := state.ConsumeRecoveredFrames(); consumed != 1 {
		t.Fatalf("consumed = %d", consumed)
	}
	if ids := state.RecoveredFrameIDs(); len(ids) != 0 {
		t.Fatalf("frames survived consumption: %#v", ids)
	}
	if combined := state.PrepareRecordedEntries([]TranscriptEntry{{Role: "user", Text: "next"}}); len(combined) != 1 {
		t.Fatalf("consumed speech replayed: %#v", combined)
	}
}

func TestRecoveredSessionStatusAndCommittedHandoffsLikeRust(t *testing.T) {
	items := []RealtimeItem{
		{ID: "start", RealtimeSessionID: "call", Content: RealtimeItemContent{Kind: RealtimeItemKindSessionStarted}},
		recoveryFrame("b1", RealtimeTranscriptRoleAssistant, "pending"),
		{
			ID:                "accepted",
			RealtimeSessionID: "call",
			Content: RealtimeItemContent{
				Kind:         RealtimeItemKindTurnInputAccepted,
				HandoffID:    "handoff-1",
				TurnID:       "turn-1",
				HandoffMode:  RealtimeHandoffModeStart,
				SourceEvents: []string{"a1"},
			},
		},
	}
	var state RealtimeHistoryState
	state.SetFramesEnabled(true)
	if status := state.RecoverSession("call", items); status != RealtimeSessionRecoveryOpen {
		t.Fatalf("status = %q", status)
	}
	if !state.HandoffCommitted("handoff-1") {
		t.Fatal("recovered handoff was not recorded as committed")
	}
	if state.ReserveHandoff("handoff-1") {
		t.Fatal("a committed handoff claimed its speech again")
	}
	if !state.ReserveHandoff("handoff-2") {
		t.Fatal("a fresh handoff could not claim the recovered speech")
	}

	ending := append(append([]RealtimeItem(nil), items...), RealtimeItem{
		ID:                "end",
		RealtimeSessionID: "call",
		Content: RealtimeItemContent{
			Kind:         RealtimeItemKindSessionInput,
			InputReason:  RealtimeSessionInputReasonSessionEnd,
			SourceEvents: []string{"b1"},
			InputItemID:  "tail",
			Input:        "pending",
		},
	})
	var ended RealtimeHistoryState
	ended.SetFramesEnabled(true)
	if status := ended.RecoverSession("call", ending); status != RealtimeSessionRecoveryEnding {
		t.Fatalf("session-end status = %q", status)
	}
	if ids := ended.RecoveredFrameIDs(); len(ids) != 0 {
		t.Fatalf("session-end receipt left speech pending: %#v", ids)
	}

	closed := append(append([]RealtimeItem(nil), items...), RealtimeItem{
		ID:                "close",
		RealtimeSessionID: "call",
		Content:           RealtimeItemContent{Kind: RealtimeItemKindSessionClosed, Outcome: RealtimeSessionOutcomeEnded},
	})
	var done RealtimeHistoryState
	done.StartSession("live")
	if status := done.RecoverSession("call", closed); status != RealtimeSessionRecoveryClosed {
		t.Fatalf("closed status = %q", status)
	}
	if ids := done.RecoveredFrameIDs(); len(ids) != 0 {
		t.Fatalf("closed call replayed speech: %#v", ids)
	}
	if done.ActiveSessionID() != "live" {
		t.Fatalf("closed recovery replaced the live session: %q", done.ActiveSessionID())
	}
}
