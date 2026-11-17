package appserver

import (
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
)

// rustThreadReadFixture retains `record` as a live thread so `thread/read`
// takes the loaded-thread path Rust #61837 rewrote.
func rustThreadReadFixture(t *testing.T, record *session.Record) *Router {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	router := NewRouter(store)
	if err := router.threads.RetainLiveThread(store, record); err != nil {
		t.Fatalf("retain live thread: %v", err)
	}
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func rustThreadReadRecord(id string, mode ThreadHistoryMode, preview string, title string) *session.Record {
	now := time.Now().UTC()
	return &session.Record{
		ID: session.ThreadID(id), SessionID: id,
		Preview:   preview,
		Title:     title,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: filepath.Join("C:", "workspace"), HistoryMode: string(mode)},
	}
}

// TestThreadReadTrimsLoadedStoredTitleLikeRust mirrors Rust #61837's
// `display_thread_title`: metadata-only reads of a loaded thread trim the
// stored title they already loaded instead of rereading the store for it.
func TestThreadReadTrimsLoadedStoredTitleLikeRust(t *testing.T) {
	router := rustThreadReadFixture(t, rustThreadReadRecord("thread-loaded-title", ThreadHistoryLegacy, "hello there", "  Release plan  "))

	read := router.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: "thread-loaded-title"}))
	if read.Error != nil {
		t.Fatalf("thread/read error = %+v", read.Error)
	}
	name := read.Result.(*ThreadReadResponse).Thread.Name
	if name == nil || *name != "Release plan" {
		t.Fatalf("loaded thread name = %+v, want trimmed stored title", name)
	}
}

// TestThreadReadDoesNotPromoteLegacyPreviewLikeRust mirrors the second half of
// Rust's helper: a legacy stored name equal to the preview stays a preview, so
// `name` is absent, while paginated threads keep their stored name.
func TestThreadReadDoesNotPromoteLegacyPreviewLikeRust(t *testing.T) {
	legacy := rustThreadReadFixture(t, rustThreadReadRecord("thread-legacy-preview", ThreadHistoryLegacy, "hello there", " hello there "))
	read := legacy.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: "thread-legacy-preview"}))
	if read.Error != nil {
		t.Fatalf("legacy thread/read error = %+v", read.Error)
	}
	if name := read.Result.(*ThreadReadResponse).Thread.Name; name != nil {
		t.Fatalf("legacy preview-equal title became a name: %q", *name)
	}

	paginated := rustThreadReadFixture(t, rustThreadReadRecord("thread-paginated-preview", ThreadHistoryPaginated, "hello there", " hello there "))
	read = paginated.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: "thread-paginated-preview"}))
	if read.Error != nil {
		t.Fatalf("paginated thread/read error = %+v", read.Error)
	}
	name := read.Result.(*ThreadReadResponse).Thread.Name
	if name == nil || *name != "hello there" {
		t.Fatalf("paginated thread name = %+v, want stored title", name)
	}
}

// TestThreadReadKeepsStoredTitleForUnloadedThreadsLikeRust pins the other side
// of Rust #61837: only the loaded-thread path reuses persisted metadata, so an
// unloaded thread still reports the name exactly as the store returned it.
func TestThreadReadKeepsStoredTitleForUnloadedThreadsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	record := rustThreadReadRecord("thread-unloaded-title", ThreadHistoryLegacy, "hello there", "  Release plan  ")
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	router := NewRouter(store)

	read := router.Handle(requestWithParams(t, IntID(1), MethodThreadRead, ThreadReadParams{ThreadID: "thread-unloaded-title"}))
	if read.Error != nil {
		t.Fatalf("thread/read error = %+v", read.Error)
	}
	name := read.Result.(*ThreadReadResponse).Thread.Name
	if name == nil || *name != "  Release plan  " {
		t.Fatalf("unloaded thread name = %+v, want stored title verbatim", name)
	}
}
