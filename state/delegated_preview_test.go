package state

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
)

func delegatedItemCompletedEvent(t *testing.T, text string) json.RawMessage {
	t.Helper()
	raw, err := json.Marshal(map[string]any{
		"type": "item_completed",
		"item": map[string]any{
			"id":        "output-1",
			"type":      "function_call_output",
			"name":      "create_thread",
			"namespace": "codex_app",
			"output":    text,
		},
	})
	if err != nil {
		t.Fatalf("marshal delegated event: %v", err)
	}
	return raw
}

// Rust #50462 (codex-rs/state/src/extract.rs:
// delegated_turn_preview_preserves_user_message_metadata). Only the preview is
// filled from a delegated output; the title and first_user_message stay unset
// until a real user message arrives.
func TestDelegatedTurnPreviewPreservesUserMessageMetadataLikeRust(t *testing.T) {
	metadata := &rolloutThreadMetadata{}
	wantPreview := ""
	for _, text := range []string{"  ", "delegated task", "later output"} {
		applyEventToBackfill(metadata, delegatedItemCompletedEvent(t, text))
		if text == "delegated task" {
			wantPreview = text
		}
		if metadata.preview != wantPreview {
			t.Fatalf("preview after %q = %q, want %q", text, metadata.preview, wantPreview)
		}
		if metadata.firstUserMessage != "" || metadata.title != "" {
			t.Fatalf("delegated output must not set first_user_message/title: %+v", metadata)
		}
	}

	followUp, err := json.Marshal(map[string]any{"type": "user_message", "message": "actual user follow-up"})
	if err != nil {
		t.Fatal(err)
	}
	applyEventToBackfill(metadata, followUp)
	if metadata.firstUserMessage != "actual user follow-up" || metadata.title != "actual user follow-up" {
		t.Fatalf("user follow-up metadata = %q/%q", metadata.firstUserMessage, metadata.title)
	}
	if metadata.preview != "delegated task" {
		t.Fatalf("preview = %q, want delegated task", metadata.preview)
	}
}

// Rust #50462 (codex-rs/thread-store/src/local/tests/delegated_preview_tests.rs:
// delegated_thread_is_listed_before_user_followup). A delegated thread becomes
// discoverable through the state database before any user follow-up, in both
// legacy and paginated history modes.
func TestDelegatedThreadIsListedBeforeUserFollowupLikeRust(t *testing.T) {
	const expectedPreview = "Inspect <main> & report findings."
	for _, historyMode := range []string{"legacy", "paginated"} {
		home := t.TempDir()
		runtime := newBackfillTestRuntimeAt(t, home)
		now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
		recorder, err := rollout.NewRecorder(&rollout.CreateParams{
			CodexHome: home, ThreadID: "delegated-thread", Source: "cli", ThreadSource: "user",
			CWD: "/workspace", ModelProvider: "openai", HistoryMode: historyMode,
			MemoryMode: "disabled", CLIVersion: "1.2.3", Now: now,
		})
		if err != nil {
			t.Fatalf("NewRecorder() error = %v", err)
		}
		payload := delegatedItemCompletedEvent(t, "<codex_delegation>\n  <source_thread_id>source</source_thread_id>\n  <input>Inspect &lt;main&gt; &amp; report findings.</input>\n</codex_delegation>")
		if err := recorder.AppendLine(rollout.Line{
			Type:      "event_msg",
			Timestamp: now.Add(time.Second).Format(time.RFC3339Nano),
			Payload:   payload,
		}); err != nil {
			t.Fatalf("AppendLine() error = %v", err)
		}
		if err := recorder.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if err := runtime.ReconcileRollout(context.Background(), recorder.Path(), false); err != nil {
			t.Fatalf("ReconcileRollout() error = %v", err)
		}

		rows, err := runtime.ListThreadRows(context.Background())
		if err != nil {
			t.Fatalf("ListThreadRows() error = %v", err)
		}
		var found *ThreadListRow
		for i := range rows {
			if rows[i].ID == "delegated-thread" {
				found = &rows[i]
				break
			}
		}
		if found == nil {
			t.Fatalf("history mode %s: delegated thread missing from the state database", historyMode)
		}
		if strings.TrimSpace(found.Preview.String) != expectedPreview {
			t.Fatalf("history mode %s: preview = %q, want %q", historyMode, found.Preview.String, expectedPreview)
		}
		if strings.TrimSpace(found.FirstUserMessage.String) != "" {
			t.Fatalf("history mode %s: first_user_message = %q, want empty", historyMode, found.FirstUserMessage.String)
		}
	}
}
