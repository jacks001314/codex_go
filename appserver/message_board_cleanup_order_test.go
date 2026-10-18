package appserver

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

// Rust #47029 runs thread_data_cleanup after the reference/writer checks and
// before rollout removal, so a cleanup failure leaves the thread retryable.
func TestThreadDeleteBoardCleanupRunsBeforeRolloutRemovalLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	threadID := session.ThreadID("board-cleanup-root")
	if err := store.Save(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		CreatedAt: time.Unix(1, 0).UTC(),
		UpdatedAt: time.Unix(1, 0).UTC(),
		Metadata:  session.Metadata{CWD: home},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	router := NewRouter(store)
	cleanupErr := errors.New("board cleanup failed")
	var cleaned []session.ThreadID
	router.SetMessageBoardCleanup(func(ids []session.ThreadID) error {
		cleaned = append([]session.ThreadID(nil), ids...)
		return cleanupErr
	})

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadDelete, ThreadDeleteParams{ThreadID: string(threadID)}))
	if response.Error == nil || !strings.Contains(response.Error.Message, cleanupErr.Error()) {
		t.Fatalf("delete response error = %#v, want the cleanup failure", response.Error)
	}
	if len(cleaned) != 1 || cleaned[0] != threadID {
		t.Fatalf("cleanup IDs = %v, want [%s]", cleaned, threadID)
	}
	if _, err := store.Read(threadID, true, false); err != nil {
		t.Fatalf("thread record was removed before cleanup succeeded: %v", err)
	}

	router.SetMessageBoardCleanup(func([]session.ThreadID) error { return nil })
	if response := router.Handle(requestWithParams(t, IntID(2), MethodThreadDelete, ThreadDeleteParams{ThreadID: string(threadID)})); response.Error != nil {
		t.Fatalf("retry delete error = %+v", response.Error)
	}
	if _, err := store.Read(threadID, true, false); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("thread read after retry error = %v, want ErrThreadNotFound", err)
	}
}
