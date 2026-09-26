package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/session"
)

// heartbeatSessionItem builds a scheduled-heartbeat instruction item the way the
// turn runtime records one: a message whose classifications are exactly the
// heartbeat kind.
func heartbeatSessionItem(t *testing.T, id string, instructions string) session.Item {
	t.Helper()
	text := "<heartbeat>\n  <automation_id>nightly</automation_id>\n  <current_time_iso>2026-01-02T03:04:05Z</current_time_iso>\n  <instructions>\n" +
		instructions + "\n  </instructions>\n</heartbeat>\n"
	raw, err := json.Marshal(map[string]any{
		"type":  "message",
		"role":  "user",
		"phase": "commentary",
		"content": []map[string]any{
			{"type": "input_text", "text": text},
		},
		"internal_chat_message_metadata_passthrough": map[string]any{
			"content_item_kinds": []string{retainedctx.HeartbeatContentKind},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return session.Item{
		ID: id, Type: "message", Role: "user", Text: text,
		Metadata: map[string]any{"turnId": "turn-1"},
		Raw:      raw,
	}
}

// Mirrors Rust's `UserInputOrigin::from_message` and the phase it persists with a
// retained record: an unchanged scheduled heartbeat coalesces into the existing
// evidence, changed instructions add one, and the message phase survives.
func TestRuntimeRouterDerivesHeartbeatOriginAndPhaseLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-heartbeat")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Items: []session.Item{
			heartbeatSessionItem(t, "heartbeat-1", "Run the nightly report."),
			heartbeatSessionItem(t, "heartbeat-2", "Run the nightly report."),
			heartbeatSessionItem(t, "heartbeat-3", "Run the weekly report."),
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	entries := context.OrderedEntries()
	if len(entries) != 2 {
		t.Fatalf("retained entries = %d, want the two distinct heartbeats", len(entries))
	}
	for index, entry := range entries {
		message := entry.Entry.UserMessage
		if message == nil {
			t.Fatalf("entry %d = %#v", index, entry)
		}
		if message.Origin != retainedctx.UserInputOriginHeartbeat {
			t.Fatalf("entry %d origin = %q, want heartbeat", index, message.Origin)
		}
		if message.Phase == nil || *message.Phase != "commentary" {
			t.Fatalf("entry %d phase = %v", index, message.Phase)
		}
	}
	if first := entries[0].Entry.UserMessage.Text; !strings.Contains(first, "Run the nightly report.") {
		t.Fatalf("first heartbeat text = %q", first)
	}
	if second := entries[1].Entry.UserMessage.Text; !strings.Contains(second, "Run the weekly report.") {
		t.Fatalf("second heartbeat text = %q", second)
	}

	// An ordinary user message keeps the default origin.
	ordinary := sessionItemForRetainedTest("user-1", "Keep the repository private.", []string{"user.text"}, []string{"input_text"})
	record, ok := retainedRecordForSessionItem(&ordinary, 0, nil, true)
	if !ok {
		t.Fatal("the ordinary instruction was not retained")
	}
	if record.message.Origin != retainedctx.UserInputOriginUser {
		t.Fatalf("ordinary origin = %q, want user", record.message.Origin)
	}
}
