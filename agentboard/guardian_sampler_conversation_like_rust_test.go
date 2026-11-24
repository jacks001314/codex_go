package agentboard

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"codex_go/utils"
)

func guardianSamplerItem(text string) json.RawMessage {
	encoded, _ := json.Marshal(map[string]any{"type": "message", "role": "assistant", "text": text})
	return encoded
}

// Rust #62135 HousekeeperSampler::commit accounts the batch input plus its
// retained reply, and the history keeps only committed replies.
func TestGuardianSamplerConversationCommitsWholeBatchesLikeRust(t *testing.T) {
	conversation := NewGuardianSamplerConversation()
	input := guardianSamplerItem("batch one")
	if err := conversation.BeginBatch(input, 10, 5, 1000); err != nil {
		t.Fatalf("BeginBatch() error = %v", err)
	}
	history := conversation.History()
	if len(history) != 1 || string(history[0].Item) != string(input) || history[0].Tokens != 10 {
		t.Fatalf("history during batch = %#v", history)
	}
	reply := guardianSamplerItem("routes one")
	if err := conversation.Commit([]json.RawMessage{reply}); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	wantTokens := 10 + utils.ApproxTokenCount(string(reply))
	if got := conversation.RetainedTokens(); got != wantTokens {
		t.Fatalf("RetainedTokens() = %d, want %d", got, wantTokens)
	}
	history = conversation.History()
	if len(history) != 2 || string(history[1].Item) != string(reply) {
		t.Fatalf("history after commit = %#v", history)
	}
}

// Under window pressure only whole completed batches leave the history.
func TestGuardianSamplerConversationEvictsWholeBatchesLikeRust(t *testing.T) {
	conversation := NewGuardianSamplerConversation()
	for index := 0; index < 3; index++ {
		if err := conversation.BeginBatch(guardianSamplerItem("input"), 100, 0, 10_000); err != nil {
			t.Fatalf("BeginBatch(%d) error = %v", index, err)
		}
		if err := conversation.Commit([]json.RawMessage{guardianSamplerItem("reply")}); err != nil {
			t.Fatalf("Commit(%d) error = %v", index, err)
		}
	}
	before := conversation.RetainedTokens()
	if before == 0 {
		t.Fatal("retained tokens = 0 before pressure")
	}
	// Let the in-flight batch leave an empty history budget, which evicts every
	// completed batch.
	if err := conversation.BeginBatch(guardianSamplerItem("next"), 100, 0, 100); err != nil {
		t.Fatalf("BeginBatch(pressure) error = %v", err)
	}
	if got := conversation.RetainedTokens(); got != 0 {
		t.Fatalf("retained tokens after eviction = %d, want 0", got)
	}
	history := conversation.History()
	if len(history) != 1 || history[0].Tokens != 100 {
		t.Fatalf("history after eviction = %#v, want only the in-flight input", history)
	}
	conversation.AbandonBatch()
	if history := conversation.History(); len(history) != 0 {
		t.Fatalf("history after abandon = %#v, want empty", history)
	}
}

// A batch that cannot fit beside the fixed instructions is refused before any
// history changes.
func TestGuardianSamplerConversationRefusesOversizedBatchLikeRust(t *testing.T) {
	conversation := NewGuardianSamplerConversation()
	if err := conversation.BeginBatch(guardianSamplerItem("kept"), 50, 0, 1000); err != nil {
		t.Fatalf("BeginBatch() error = %v", err)
	}
	if err := conversation.Commit([]json.RawMessage{guardianSamplerItem("reply")}); err != nil {
		t.Fatalf("Commit() error = %v", err)
	}
	before := conversation.History()
	err := conversation.BeginBatch(guardianSamplerItem("too big"), 600, 500, 1000)
	if err == nil || err.Error() != "housekeeper batch exceeds model input window" {
		t.Fatalf("BeginBatch(oversized) error = %v", err)
	}
	after := conversation.History()
	if len(after) != len(before) || string(after[len(after)-1].Item) != string(before[len(before)-1].Item) {
		t.Fatalf("history changed after refusal: %#v -> %#v", before, after)
	}
}

// Rust #62135 HousekeeperSampler::account_changed abandons a batch once the
// owning account revision moves, and never fires without an owner.
func TestGuardianSamplerAccountWatchCancelsOnAccountChangeLikeRust(t *testing.T) {
	current := true
	changes := make(chan struct{})
	watch := NewGuardianSamplerAccountWatch(func() bool { return current }, changes)
	done := make(chan bool, 1)
	go func() { done <- watch.Wait(context.Background()) }()
	select {
	case changed := <-done:
		t.Fatalf("watch fired before the account changed: %v", changed)
	case <-time.After(50 * time.Millisecond):
	}
	current = false
	close(changes)
	select {
	case changed := <-done:
		if !changed {
			t.Fatal("watch did not report the account change")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not observe the account change")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if NewGuardianSamplerAccountWatch(nil, nil).Wait(ctx) {
		t.Fatal("watch without an owner reported a change")
	}
	stale := NewGuardianSamplerAccountWatch(func() bool { return true }, make(chan struct{}))
	if stale.Wait(ctx) {
		t.Fatal("watch reported a change after the caller context ended")
	}
}
