package appserver

import (
	"testing"

	"codex_go/codexapi"
	"codex_go/turn"
)

// TestActiveTurnClientMetadataCarriesHistoryIngest mirrors Rust #41743: the
// session sets history_ingest_requested in the turn's Responses metadata when
// the token-budget history-notes extension is enabled, omits it when the
// extension is off, and keeps it reserved so caller metadata cannot override or
// introduce the core-owned value.
func TestActiveTurnClientMetadataCarriesHistoryIngest(t *testing.T) {
	enableTurn := func(t *testing.T, router *RuntimeRouter, tokenBudget map[string]any) {
		t.Helper()
		params := &turn.TurnStartParams{
			ThreadID: "thread-1",
			Config:   map[string]any{"features": map[string]any{"token_budget": tokenBudget}},
		}
		if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
			t.Fatalf("RegisterTurn() error = %v", err)
		}
	}
	readHistoryIngest := func(t *testing.T, router *RuntimeRouter, requestMetadata map[string]string) (any, bool) {
		t.Helper()
		client := router.activeTurnClientMetadata("thread-1", "turn-1", requestMetadata)
		document := decodeCompactionTurnMetadata(t, client[codexapi.ClientCodexTurnMetadataHeader])
		value, ok := document[codexapi.HistoryIngestRequestedKey]
		return value, ok
	}

	t.Run("enabled extension requests ingestion", func(t *testing.T) {
		router := newTestRuntimeRouter()
		enableTurn(t, router, map[string]any{"enabled": true, "use_history_notes_extension": true})
		if value, ok := readHistoryIngest(t, router, nil); !ok || value != true {
			t.Fatalf("history_ingest_requested = %#v (present=%v), want true", value, ok)
		}
		if value, _ := readHistoryIngest(t, router, map[string]string{codexapi.HistoryIngestRequestedKey: "false"}); value != true {
			t.Fatalf("caller metadata overrode history_ingest_requested: %#v", value)
		}
	})

	t.Run("disabled extension omits the key", func(t *testing.T) {
		router := newTestRuntimeRouter()
		enableTurn(t, router, map[string]any{"enabled": true})
		if value, ok := readHistoryIngest(t, router, nil); ok {
			t.Fatalf("history_ingest_requested = %#v, want the key omitted", value)
		}
		if value, ok := readHistoryIngest(t, router, map[string]string{codexapi.HistoryIngestRequestedKey: "true"}); ok {
			t.Fatalf("caller metadata introduced history_ingest_requested: %#v", value)
		}
	})
}
