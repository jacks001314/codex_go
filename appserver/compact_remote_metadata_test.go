package appserver

import (
	"context"
	"encoding/json"
	"testing"

	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

func decodeCompactionTurnMetadata(t *testing.T, raw string) map[string]any {
	t.Helper()
	if raw == "" {
		t.Fatal("compaction request carries no turn-metadata document")
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatalf("turn-metadata document = %q (%v)", raw, err)
	}
	return document
}

// TestCompactResponsesClientMetadataCarriesTheTurnDocument mirrors Rust's
// Session::compaction_responses_metadata: a remote compaction request reports
// the compaction turn's metadata document with the compaction request kind and
// the operation's dispatch metadata, plus the current window and fork fields -
// and, unlike a sampling step, it does not apply the captured step settings.
func TestCompactResponsesClientMetadataCarriesTheTurnDocument(t *testing.T) {
	router := &RuntimeRouter{}
	record := &session.Record{ID: "thread-1", SessionID: "session-1"}
	request := &compact.Request{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Trigger:  compact.TriggerAuto,
		Reason:   compact.ReasonContextWindowExceeded,
		Phase:    compact.PhasePreTurn,
	}
	metadata := router.compactResponsesClientMetadata(record, request, "gpt-5")
	if metadata[codexapi.ThreadIDKey] != "thread-1" || metadata[codexapi.TurnIDKey] != "turn-1" {
		t.Fatalf("compaction client metadata = %#v", metadata)
	}
	document := decodeCompactionTurnMetadata(t, metadata[codexapi.ClientCodexTurnMetadataHeader])
	if document["request_kind"] != string(codexapi.ClientRequestCompaction) {
		t.Fatalf("request_kind = %#v, want compaction", document["request_kind"])
	}
	if document["session_id"] != "thread-1" || document["thread_id"] != "thread-1" || document["turn_id"] != "turn-1" {
		t.Fatalf("turn identity = %#v", document)
	}
	// Rust's conversation window identity is `{thread_id}:{window_number}` with a
	// zero-based number (Session::current_window_id), so a thread's first
	// compaction request reports window 0.
	if document["window_id"] != "thread-1:0" {
		t.Fatalf("window_id = %#v", document["window_id"])
	}
	if _, ok := document["window_number"]; !ok {
		t.Fatalf("compaction metadata is missing the window number: %#v", document)
	}
	contextWindowID, _ := document["context_window_id"].(string)
	if contextWindowID == "" {
		t.Fatalf("compaction metadata is missing the context window id: %#v", document)
	}
	compactionMetadata, _ := document[codexapi.CompactionKey].(map[string]any)
	if compactionMetadata == nil {
		t.Fatalf("compaction metadata is missing its dispatch fields: %#v", document)
	}
	for key, want := range map[string]any{
		"trigger":        string(compact.TriggerAuto),
		"reason":         string(compact.ReasonContextWindowExceeded),
		"implementation": compactionImplementationResponses,
		"phase":          string(compact.PhasePreTurn),
		"strategy":       "memento",
	} {
		if compactionMetadata[key] != want {
			t.Fatalf("compaction.%s = %#v, want %#v", key, compactionMetadata[key], want)
		}
	}
	// Rust applies ExecutionMetadata only on the sampling and MCP paths, so the
	// compaction document has no captured model or reasoning effort.
	for _, key := range []string{codexapi.ModelKey, codexapi.ReasoningEffortKey} {
		if value, ok := document[key]; ok {
			t.Fatalf("compaction metadata carries the captured setting %s = %#v", key, value)
		}
	}
}

// TestAgentCompactRunnerSendsTheCompactionTurnDocument mirrors the request a
// remote compaction runner issues: the turn-metadata document carries the
// compaction request kind, and no flat request-kind key is added to the client
// metadata (Rust keeps the kind inside the document).
func TestAgentCompactRunnerSendsTheCompactionTurnDocument(t *testing.T) {
	agent := &compactReasoningEffortAgent{}
	router := &RuntimeRouter{}
	record := &session.Record{ID: "thread-1", SessionID: "session-1"}
	request := &compact.Request{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Trigger:  compact.TriggerManual,
		Reason:   compact.ReasonUserRequested,
		Phase:    compact.PhaseStandaloneTurn,
	}
	runner := &agentCompactRunner{
		agent:          agent,
		model:          "gpt-5",
		clientMetadata: router.compactResponsesClientMetadata(record, request, "gpt-5"),
	}
	if _, err := runner.Compact(context.Background(), request); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("agent requests = %#v", agent.requests)
	}
	metadata := agent.requests[0].ClientMetadata
	if _, ok := metadata[codexapi.RequestKindKey]; ok {
		t.Fatalf("flat request kind leaked into client metadata: %#v", metadata)
	}
	document := decodeCompactionTurnMetadata(t, metadata[codexapi.ClientCodexTurnMetadataHeader])
	if document["request_kind"] != string(codexapi.ClientRequestCompaction) {
		t.Fatalf("request_kind = %#v, want compaction", document["request_kind"])
	}
}

// TestHistoryIngestRequestedForTurn mirrors Rust's with_window_and_fork_metadata:
// the flag follows the turn's effective token-budget config.
func TestHistoryIngestRequestedForTurn(t *testing.T) {
	router := newTestRuntimeRouter()
	enabled := &config.Config{Values: map[string]any{
		"features": map[string]any{
			"token_budget": map[string]any{"enabled": true, "use_history_notes_extension": true},
		},
	}}
	if !router.historyIngestRequestedForTurn(enabled, nil) {
		t.Fatal("history-notes extension did not request ingestion")
	}
	disabled := &config.Config{Values: map[string]any{
		"features": map[string]any{
			"token_budget": map[string]any{"enabled": true},
		},
	}}
	if router.historyIngestRequestedForTurn(disabled, nil) {
		t.Fatal("a plain token budget requested ingestion")
	}
	if router.historyIngestRequestedForTurn(nil, nil) {
		t.Fatal("a missing config requested ingestion")
	}
}

// TestCompactResponsesClientMetadataCarriesHistoryIngest covers the window/fork
// extra on the compaction path: the issuing turn's token-budget config decides
// the flag.
func TestCompactResponsesClientMetadataCarriesHistoryIngest(t *testing.T) {
	router := newTestRuntimeRouter()
	params := &turn.TurnStartParams{
		ThreadID: "thread-1",
		Config: map[string]any{
			"features": map[string]any{
				"token_budget": map[string]any{"enabled": true, "use_history_notes_extension": true},
			},
		},
	}
	if err := router.threads.RegisterTurn("thread-1", "turn-1", nil, 0, params); err != nil {
		t.Fatalf("RegisterTurn() error = %v", err)
	}
	request := &compact.Request{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Trigger:  compact.TriggerAuto,
		Reason:   compact.ReasonTokenLimit,
		Phase:    compact.PhaseMidTurn,
	}
	metadata := router.compactResponsesClientMetadata(&session.Record{ID: "thread-1"}, request, "gpt-5")
	document := decodeCompactionTurnMetadata(t, metadata[codexapi.ClientCodexTurnMetadataHeader])
	if document[codexapi.HistoryIngestRequestedKey] != true {
		t.Fatalf("compaction metadata = %#v, want history_ingest_requested", document)
	}
}
