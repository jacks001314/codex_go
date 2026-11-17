package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/session"
)

// Rust side_fork_config (#60319) forks with a config whose `daybreak` override
// disables Daybreak, so the fork persists that preference instead of the
// source thread's saved value. A fork without the override still inherits.
func TestThreadForkConfigDaybreakOverridePersistsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    filepath.ToSlash(t.TempDir()),
		Prompt: "hello",
	}))
	if start.Error != nil {
		t.Fatalf("thread/start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	enabled := true
	update := router.Handle(requestWithParams(t, IntID(2), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID:        threadID,
		DaybreakEnabled: &enabled,
	}))
	if update.Error != nil {
		t.Fatalf("thread/metadata/update error: %+v", update.Error)
	}
	materializeThreadRolloutForTest(t, router, store, threadID)

	fork := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{
		ThreadID: threadID,
		Config:   map[string]any{"daybreak": false},
	}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error: %+v", fork.Error)
	}
	forkedThread := fork.Result.(*ThreadForkResponse).Thread
	if forkedThread.DaybreakEnabled == nil || *forkedThread.DaybreakEnabled {
		t.Fatalf("forked daybreakEnabled = %#v, want false", forkedThread.DaybreakEnabled)
	}
	forkedRecord, err := session.NewStore(store.Root()).Load(session.ThreadID(forkedThread.ID))
	if err != nil {
		t.Fatalf("reload fork: %v", err)
	}
	if forkedRecord.Metadata.DaybreakEnabled == nil || *forkedRecord.Metadata.DaybreakEnabled {
		t.Fatalf("persisted fork daybreakEnabled = %#v, want false", forkedRecord.Metadata.DaybreakEnabled)
	}
	configValues, ok := forkedRecord.Metadata.Extra["config"].(map[string]any)
	if !ok {
		t.Fatalf("fork config = %#v", forkedRecord.Metadata.Extra["config"])
	}
	if got, ok := configValues["daybreak"]; !ok || got != false {
		t.Fatalf("fork config daybreak = %#v, want false", got)
	}

	plain := router.Handle(requestWithParams(t, IntID(4), MethodThreadFork, ThreadForkParams{ThreadID: threadID}))
	if plain.Error != nil {
		t.Fatalf("thread/fork(plain) error: %+v", plain.Error)
	}
	plainThread := plain.Result.(*ThreadForkResponse).Thread
	if plainThread.DaybreakEnabled == nil || !*plainThread.DaybreakEnabled {
		t.Fatalf("plain fork daybreakEnabled = %#v, want inherited true", plainThread.DaybreakEnabled)
	}
}
