package appserver

import (
	"path/filepath"
	"testing"

	"codex_go/session"
)

// Mirrors Rust #42854: daybreakEnabled is a persisted thread preference that
// survives metadata updates, cold reads, resumes, and forks while forks keep
// independent copies.
func TestRuntimeRouterDaybreakPreferenceUpdateForkAndResumeLikeRust(t *testing.T) {
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
	updatedThread := update.Result.(*ThreadMetadataUpdateResponse).Thread
	if updatedThread.DaybreakEnabled == nil || !*updatedThread.DaybreakEnabled {
		t.Fatalf("updated daybreakEnabled = %#v, want true", updatedThread.DaybreakEnabled)
	}
	persisted, err := session.NewStore(store.Root()).Load(session.ThreadID(threadID))
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	if persisted.Metadata.DaybreakEnabled == nil || !*persisted.Metadata.DaybreakEnabled {
		t.Fatalf("persisted daybreakEnabled = %#v, want true", persisted.Metadata.DaybreakEnabled)
	}
	materializeThreadRolloutForTest(t, router, store, threadID)

	fork := router.Handle(requestWithParams(t, IntID(3), MethodThreadFork, ThreadForkParams{ThreadID: threadID}))
	if fork.Error != nil {
		t.Fatalf("thread/fork error: %+v", fork.Error)
	}
	forkedThread := fork.Result.(*ThreadForkResponse).Thread
	if forkedThread.DaybreakEnabled == nil || !*forkedThread.DaybreakEnabled {
		t.Fatalf("forked daybreakEnabled = %#v, want inherited true", forkedThread.DaybreakEnabled)
	}

	disabled := false
	update = router.Handle(requestWithParams(t, IntID(4), MethodThreadMetadataUpdate, ThreadMetadataUpdateParams{
		ThreadID:        threadID,
		DaybreakEnabled: &disabled,
	}))
	if update.Error != nil {
		t.Fatalf("thread/metadata/update(disable) error: %+v", update.Error)
	}
	forkedRecord, err := session.NewStore(store.Root()).Load(session.ThreadID(forkedThread.ID))
	if err != nil {
		t.Fatalf("reload fork: %v", err)
	}
	if forkedRecord.Metadata.DaybreakEnabled == nil || !*forkedRecord.Metadata.DaybreakEnabled {
		t.Fatalf("fork preference changed with the source: %#v", forkedRecord.Metadata.DaybreakEnabled)
	}

	// Resume uses the persisted preference instead of any access-program default.
	resumedRouter := NewRouter(session.NewStore(store.Root()))
	resume := resumedRouter.Handle(requestWithParams(t, IntID(5), MethodThreadResume, ThreadResumeParams{
		ThreadID: forkedThread.ID,
	}))
	if resume.Error != nil {
		t.Fatalf("thread/resume error: %+v", resume.Error)
	}
	resumedThread := resume.Result.(*ThreadResumeResponse).Thread
	if resumedThread.DaybreakEnabled == nil || !*resumedThread.DaybreakEnabled {
		t.Fatalf("resumed daybreakEnabled = %#v, want true", resumedThread.DaybreakEnabled)
	}
}

// A thread without a first turn must still persist the metadata-only choice.
func TestRuntimeRouterDaybreakPreferencePersistsWithoutFirstTurnLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: filepath.ToSlash(t.TempDir()),
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
	record, err := session.NewStore(store.Root()).Load(session.ThreadID(threadID))
	if err != nil {
		t.Fatalf("reload thread: %v", err)
	}
	if record.Metadata.DaybreakEnabled == nil || !*record.Metadata.DaybreakEnabled {
		t.Fatalf("persisted daybreakEnabled = %#v, want true", record.Metadata.DaybreakEnabled)
	}
}
