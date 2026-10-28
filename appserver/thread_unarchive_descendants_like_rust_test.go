package appserver

import (
	"testing"

	"codex_go/rollout"
	"codex_go/session"
)

// TestRouterUnarchiveRestoresSpawnedDescendantsLikeRust covers Rust #57635:
// archiving a thread archives its spawned subtree, so unarchiving restores
// every archived descendant while leaving an independently restored (active)
// descendant alone.
func TestRouterUnarchiveRestoresSpawnedDescendantsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	router := NewRouter(store)
	now := fixedTime()
	for _, spec := range []struct {
		id     session.ThreadID
		parent session.ThreadID
	}{
		{"root", ""},
		{"child", "root"},
		{"grandchild", "child"},
	} {
		record := &session.Record{
			ID:             spec.id,
			SessionID:      string(spec.id),
			ParentThreadID: spec.parent,
			CreatedAt:      now,
			UpdatedAt:      now,
			RecencyAt:      now,
			Metadata: session.Metadata{
				CWD:           "D:/repo",
				ModelProvider: "openai",
				Source:        "cli",
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		}
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", spec.id, err)
		}
		if err := router.createThreadRollout(record, now); err != nil {
			t.Fatalf("createThreadRollout(%s) error = %v", spec.id, err)
		}
	}

	archived := router.Handle(requestWithParams(t, IntID(1), MethodThreadArchive, ThreadArchiveParams{ThreadID: "root"}))
	if archived.Error != nil {
		t.Fatalf("archive = %+v", archived.Error)
	}

	// The child was restored independently while the parent and grandchild
	// stayed archived; unarchiving the root must skip it.
	if _, err := store.Unarchive("child"); err != nil {
		t.Fatalf("Unarchive(child) error = %v", err)
	}
	router.archiveThreadRollout("child", false)

	response := router.Handle(requestWithParams(t, IntID(2), MethodThreadUnarchive, ThreadUnarchiveParams{ThreadID: "root"}))
	if response.Error != nil {
		t.Fatalf("unarchive = %+v", response.Error)
	}
	payload, ok := response.Result.(*ThreadUnarchiveResponse)
	if !ok || payload.Thread == nil || payload.Thread.ID != "root" {
		t.Fatalf("unarchive result = %#v", response.Result)
	}
	if got := payload.RestoredThreadIDs; len(got) != 2 || got[0] != "root" || got[1] != "grandchild" {
		t.Fatalf("RestoredThreadIDs = %v, want [root grandchild]", got)
	}
	for _, threadID := range []session.ThreadID{"root", "child", "grandchild"} {
		record, err := store.Read(threadID, true, false)
		if err != nil {
			t.Fatalf("Read(%s) error = %v", threadID, err)
		}
		if record.Archived {
			t.Fatalf("%s is still archived", threadID)
		}
		if _, err := rollout.FindThreadPath(store.Root(), string(threadID), false); err != nil {
			t.Fatalf("%s rollout was not restored: %v", threadID, err)
		}
	}
}

// TestRuntimeRouterUnarchiveNotifiesEveryRestoredThreadLikeRust ensures the
// response stays root-only while `thread/unarchived` is emitted for each
// restored thread in restoration order.
func TestRuntimeRouterUnarchiveNotifiesEveryRestoredThreadLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter})
	sink := &targetedNotificationTestSink{}
	router.SetNotificationSink(sink)
	now := fixedTime()
	for _, spec := range []struct {
		id     session.ThreadID
		parent session.ThreadID
	}{
		{"root", ""},
		{"child", "root"},
	} {
		record := &session.Record{
			ID:             spec.id,
			SessionID:      string(spec.id),
			ParentThreadID: spec.parent,
			CreatedAt:      now,
			UpdatedAt:      now,
			RecencyAt:      now,
			Metadata: session.Metadata{
				CWD:           "D:/repo",
				ModelProvider: "openai",
				Source:        "cli",
				HistoryMode:   string(ThreadHistoryLegacy),
			},
		}
		if err := store.Create(record); err != nil {
			t.Fatalf("Create(%s) error = %v", spec.id, err)
		}
		if err := threadRouter.createThreadRollout(record, now); err != nil {
			t.Fatalf("createThreadRollout(%s) error = %v", spec.id, err)
		}
	}
	if archived := router.Handle(requestWithParams(t, IntID(1), MethodThreadArchive, ThreadArchiveParams{ThreadID: "root"})); archived.Error != nil {
		t.Fatalf("archive = %+v", archived.Error)
	}
	if restored := router.Handle(requestWithParams(t, IntID(2), MethodThreadUnarchive, ThreadUnarchiveParams{ThreadID: "root"})); restored.Error != nil {
		t.Fatalf("unarchive = %+v", restored.Error)
	}

	var ids []string
	for _, sent := range sink.List() {
		if sent.notification == nil || sent.notification.Method != NotificationThreadUnarchived {
			continue
		}
		params, ok := sent.notification.Params.(*ThreadIDNotification)
		if !ok {
			t.Fatalf("unarchived params = %#v", sent.notification.Params)
		}
		ids = append(ids, params.ThreadID)
	}
	if len(ids) != 2 || ids[0] != "root" || ids[1] != "child" {
		t.Fatalf("thread/unarchived ids = %v, want [root child]", ids)
	}
}
