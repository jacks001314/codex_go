package appserver

import (
	"testing"
	"time"

	"codex_go/session"
)

// TestResumeRefreshesStaleHistoryUnderWriterLikeRust pins Rust #49599: a
// resume snapshot read before writer acquisition must be refreshed after the
// writer is held, so a previous writer's late append is restored.
func TestResumeRefreshesStaleHistoryUnderWriterLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	threadID := session.ThreadID("thread-resume-authoritative")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{HistoryMode: "legacy"},
		Items: []session.Item{{
			ID:        "before-snapshot",
			Type:      "message",
			Role:      "user",
			Text:      "before snapshot",
			CreatedAt: now,
			Metadata:  map[string]any{"turnId": "turn-1"},
		}},
	}); err != nil {
		t.Fatalf("Create(%s) error = %v", threadID, err)
	}
	stale, err := store.Read(threadID, true, true)
	if err != nil {
		t.Fatalf("Read(stale snapshot) error = %v", err)
	}
	if len(stale.Items) != 1 {
		t.Fatalf("stale snapshot items = %d, want 1", len(stale.Items))
	}
	if _, err := store.AppendItem(threadID, session.Item{
		ID:        "after-snapshot",
		Type:      "message",
		Role:      "user",
		Text:      "after snapshot",
		CreatedAt: now.Add(time.Second),
		Metadata:  map[string]any{"turnId": "turn-2"},
	}); err != nil {
		t.Fatalf("AppendItem(after snapshot) error = %v", err)
	}

	manager := NewThreadManager(nil)
	retention, err := manager.AcquireLiveThreadRetention(store, stale)
	if err != nil {
		t.Fatalf("AcquireLiveThreadRetention() error = %v", err)
	}
	defer func() { _ = retention.Discard() }()
	refreshed, err := NewRouter(store).refreshedResumeRecord(retention, stale, true)
	if err != nil {
		t.Fatalf("refreshedResumeRecord() error = %v", err)
	}
	if len(refreshed.Items) != 2 || refreshed.Items[1].ID != "after-snapshot" {
		t.Fatalf("refreshed items = %#v, want the late append", refreshed.Items)
	}
	if len(stale.Items) != 1 {
		t.Fatalf("pre-acquisition snapshot was mutated: %#v", stale.Items)
	}
}

// TestResumeRefreshKeepsRolloutFallbackLikeRust keeps the initial rollout-path
// snapshot when no stored record exists to refresh.
func TestResumeRefreshKeepsRolloutFallbackLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-resume-from-rollout")
	snapshot := &session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Metadata:  session.Metadata{HistoryMode: "legacy"},
		Items:     []session.Item{{ID: "from-rollout", Type: "message", Role: "user", Text: "rollout history"}},
	}
	manager := NewThreadManager(nil)
	retention, err := manager.AcquireLiveThreadRetention(store, snapshot)
	if err != nil {
		t.Fatalf("AcquireLiveThreadRetention() error = %v", err)
	}
	defer func() { _ = retention.Discard() }()
	refreshed, err := NewRouter(store).refreshedResumeRecord(retention, snapshot, true)
	if err != nil {
		t.Fatalf("refreshedResumeRecord() error = %v", err)
	}
	if refreshed != snapshot {
		t.Fatalf("refreshed record = %#v, want the rollout fallback snapshot", refreshed)
	}
}
