package chatwidget

import (
	"strings"
	"testing"
)

func TestRealtimeDelegationInputParsesWrapper(t *testing.T) {
	prompt := "<realtime_delegation>\n  <input>inspect the workspace</input>\n</realtime_delegation>"
	input, ok := RealtimeDelegationInput(prompt)
	if !ok || input != "inspect the workspace" {
		t.Fatalf("input = %q / %v", input, ok)
	}
	// Leading and trailing whitespace around the wrapper is tolerated.
	if input, ok := RealtimeDelegationInput("  " + prompt + "  "); !ok || input != "inspect the workspace" {
		t.Fatalf("padded input = %q / %v", input, ok)
	}
	for _, candidate := range []string{
		"",
		"plain user text",
		"<realtime_delegation>missing suffix",
		"<input>only</input>",
		"<realtime_delegation><input>no close</realtime_delegation>",
		"<realtime_delegation></realtime_delegation>",
	} {
		if _, ok := RealtimeDelegationInput(candidate); ok {
			t.Fatalf("%q parsed as a delegation", candidate)
		}
	}
}

func TestVoiceSpeakableFinalTextBudget(t *testing.T) {
	// The budget is one approximate token per four bytes.
	if !VoiceSpeakableFinalText(strings.Repeat("a", VoiceMaxSpeakableFinalTokens*4)) {
		t.Fatal("an answer at the budget was rejected")
	}
	if VoiceSpeakableFinalText(strings.Repeat("a", VoiceMaxSpeakableFinalTokens*4+1)) {
		t.Fatal("an answer beyond the budget was accepted")
	}
	if !VoiceSpeakableFinalText("short") {
		t.Fatal("a short answer was rejected")
	}
}

func activeVoiceState() VoiceConversationState {
	state := VoiceConversationState{}
	state.BeginVoiceConversation("thread-1", 1)
	state.MarkVoiceBackendStarted()
	state.MarkVoiceWebRTCConnected()
	return state
}

func TestVoiceDelegatedTurnSpeaksOnce(t *testing.T) {
	state := activeVoiceState()
	if !state.MarkVoiceDelegatedTurn() {
		t.Fatal("an active session did not accept a delegated turn")
	}
	if !state.DelegatedVoiceTurnSpeakable() {
		t.Fatal("the delegated turn is not speakable")
	}
	text, ok := state.TakeVoiceSpeech("item-1", "the answer")
	if !ok || text != "the answer" {
		t.Fatalf("speech = %q / %v", text, ok)
	}
	if state.DelegatedVoiceTurnSpeakable() {
		t.Fatal("the turn remained speakable after queueing")
	}
	// A second final answer for the same turn is not spoken again.
	if _, ok := state.TakeVoiceSpeech("item-2", "another answer"); ok {
		t.Fatal("a turn was spoken twice")
	}
	if len(state.PendingSpeech) != 1 || state.PendingSpeech[0].ItemID != "item-1" {
		t.Fatalf("pending speech = %#v", state.PendingSpeech)
	}
}

func TestVoiceSpeechRequiresRunningDelegatedTurn(t *testing.T) {
	// No session at all.
	var idle VoiceConversationState
	if idle.MarkVoiceDelegatedTurn() {
		t.Fatal("an inactive session accepted a delegated turn")
	}
	if _, ok := idle.TakeVoiceSpeech("item-1", "answer"); ok {
		t.Fatal("an inactive session spoke")
	}
	// Active session without a delegated turn.
	running := activeVoiceState()
	if _, ok := running.TakeVoiceSpeech("item-1", "answer"); ok {
		t.Fatal("a non-delegated turn was spoken")
	}
}

func TestVoiceSpeechRejectsOversizedAnswerButSpendsTheTurn(t *testing.T) {
	state := activeVoiceState()
	state.MarkVoiceDelegatedTurn()
	if _, ok := state.TakeVoiceSpeech("item-1", strings.Repeat("a", VoiceMaxSpeakableFinalTokens*4+1)); ok {
		t.Fatal("an oversized answer was queued")
	}
	if state.DelegatedVoiceTurnSpeakable() {
		t.Fatal("an oversized answer left the turn speakable")
	}
	if len(state.PendingSpeech) != 0 {
		t.Fatalf("pending speech = %#v", state.PendingSpeech)
	}
	// An empty answer is treated the same way.
	state.MarkVoiceDelegatedTurn()
	if _, ok := state.TakeVoiceSpeech("item-2", "   "); ok {
		t.Fatal("an empty answer was queued")
	}
}

func TestVoiceSpeechAcceptRestoreAndBounds(t *testing.T) {
	state := activeVoiceState()
	for index := 0; index < VoicePendingSpeechCapacity+4; index++ {
		state.MarkVoiceDelegatedTurn()
		state.TakeVoiceSpeech("item", "answer")
	}
	if len(state.PendingSpeech) != VoicePendingSpeechCapacity {
		t.Fatalf("pending speech = %d, want %d", len(state.PendingSpeech), VoicePendingSpeechCapacity)
	}

	state.MarkVoiceDelegatedTurn()
	state.TakeVoiceSpeech("accepted", "delivered")
	state.AcceptVoiceSpeech("accepted")
	if _, ok := state.RestoreVoiceSpeech("accepted"); ok {
		t.Fatal("an accepted answer was restorable")
	}

	state.MarkVoiceDelegatedTurn()
	state.TakeVoiceSpeech("restored", "undelivered")
	text, ok := state.RestoreVoiceSpeech("restored")
	if !ok || text != "undelivered" {
		t.Fatalf("restored = %q / %v", text, ok)
	}
	if _, ok := state.RestoreVoiceSpeech("missing"); ok {
		t.Fatal("an unknown item was restored")
	}

	// Every remaining answer is returned once when the session stops.
	pending := state.TakeUndeliveredVoiceSpeech()
	if len(pending) != len(state.PendingSpeech) && len(state.PendingSpeech) != 0 {
		t.Fatalf("undelivered = %#v, pending = %#v", pending, state.PendingSpeech)
	}
	if len(pending) == 0 {
		t.Fatal("undelivered speech was empty")
	}
	if second := state.TakeUndeliveredVoiceSpeech(); second != nil {
		t.Fatalf("second drain = %#v", second)
	}
}
