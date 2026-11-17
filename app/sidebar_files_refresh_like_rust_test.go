package app

import (
	"fmt"
	"testing"
	"time"

	"codex_go/protocol"
	"codex_go/session"
	codextea "codex_go/tui/tea"
)

// Rust parity: codex-rs/tui/src/app/sidebar_files.rs refresh_sidebar_files and
// start_sidebar_files_scan (#61920).

func createSidebarFilesThread(t *testing.T, store *session.Store, cwd string) session.ThreadID {
	t.Helper()
	now := time.Now().UTC()
	record := &session.Record{
		ID:        "parent-files",
		CreatedAt: now,
		UpdatedAt: now,
		Metadata:  session.Metadata{CWD: cwd, Source: "cli"},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("create thread: %v", err)
	}
	return record.ID
}

func appendSidebarFilesItem(t *testing.T, store *session.Store, threadID session.ThreadID, item session.Item) {
	t.Helper()
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now().UTC()
	}
	if _, err := store.AppendItem(threadID, item); err != nil {
		t.Fatalf("append %s: %v", item.ID, err)
	}
}

// The embedded host reads the stored conversation newest-first, so a completed
// patch heads the list and each message's links follow.
func TestEmbeddedSidebarFilesHostPagesTheStoreLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store := newSessionStore()
	threadID := createSidebarFilesThread(t, store, "/workspace")
	now := time.Now().UTC()
	appendSidebarFilesItem(t, store, threadID, session.Item{
		ID: "user-1", Type: "user_message", Role: "user",
		Text: "look at [first](src/first.ts)", CreatedAt: now,
		Metadata: map[string]any{"turn_id": "turn-1"},
	})
	appendSidebarFilesItem(t, store, threadID, session.Item{
		ID: "agent-1", Type: "agent_message", Role: "assistant",
		Text: "see [second](src/second.ts)", CreatedAt: now.Add(time.Second),
		Metadata: map[string]any{"turn_id": "turn-1"},
	})
	appendSidebarFilesItem(t, store, threadID, session.Item{
		ID: "patch-1", Type: "file_change", Status: "completed",
		CreatedAt: now.Add(2 * time.Second),
		Metadata:  map[string]any{"turn_id": "turn-1"},
		Data: map[string]any{"changes": []any{
			map[string]any{"path": "src/added.ts", "kind": "add"},
			map[string]any{"path": "src/removed.ts", "kind": "delete"},
		}},
	})

	host := newSidebarFilesHost(newEmbeddedSidebarFilesSource(store), func() string { return "/workspace" }, sidebarPreviewUnavailableForEmbedded())
	snapshot := host.snapshot(codextea.SidebarFilesRequest{ParentThreadID: "parent-files", Resume: true})
	if snapshot.Loading {
		t.Fatal("a short thread must finish in one page")
	}
	got := make([]string, 0, len(snapshot.Files))
	for _, file := range snapshot.Files {
		got = append(got, file.Path)
	}
	want := []string{"/workspace/src/added.ts", "/workspace/src/second.ts", "/workspace/src/first.ts"}
	if len(got) != len(want) {
		t.Fatalf("files = %v, want %v", got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("files = %v, want %v", got, want)
		}
	}
	if snapshot.CWD != "/workspace" {
		t.Fatalf("cwd = %q", snapshot.CWD)
	}
	if snapshot.PreviewUnavailable != sidebarPreviewUnavailableForEmbedded() {
		t.Fatalf("preview notice = %q", snapshot.PreviewUnavailable)
	}
}

// A conversation longer than one page keeps Loading set until the oldest page
// arrives, mirroring Rust's chained start_sidebar_files_scan.
func TestEmbeddedSidebarFilesHostChainsPagesLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store := newSessionStore()
	threadID := createSidebarFilesThread(t, store, "/workspace")
	base := time.Now().UTC()
	const itemCount = sidebarFilesPageLimit + 20
	for index := 0; index < itemCount; index++ {
		appendSidebarFilesItem(t, store, threadID, session.Item{
			ID:        fmt.Sprintf("agent-%03d", index),
			Type:      "agent_message",
			Role:      "assistant",
			Text:      fmt.Sprintf("see [item %d](src/file-%03d.ts)", index, index),
			CreatedAt: base.Add(time.Duration(index) * time.Second),
			Metadata:  map[string]any{"turn_id": fmt.Sprintf("turn-%03d", index)},
		})
	}

	host := newSidebarFilesHost(newEmbeddedSidebarFilesSource(store), func() string { return "/workspace" }, sidebarPreviewUnavailableForEmbedded())
	request := codextea.SidebarFilesRequest{ParentThreadID: "parent-files", Resume: true}
	snapshot := host.snapshot(request)
	if !snapshot.Loading {
		t.Fatal("the first page must report older history")
	}
	pages := 0
	for snapshot.Loading {
		if pages > 4 {
			t.Fatal("page chain did not finish")
		}
		snapshot = host.snapshot(codextea.SidebarFilesRequest{ParentThreadID: "parent-files"})
		pages++
	}
	if len(snapshot.Files) != itemCount {
		t.Fatalf("files = %d, want %d", len(snapshot.Files), itemCount)
	}
	if snapshot.Files[0].Path != "/workspace/src/file-119.ts" {
		t.Fatalf("newest = %q", snapshot.Files[0].Path)
	}
	if snapshot.Files[len(snapshot.Files)-1].Path != "/workspace/src/file-000.ts" {
		t.Fatalf("oldest = %q", snapshot.Files[len(snapshot.Files)-1].Path)
	}
}

// Rust notice_sidebar_file_item: the embedded writer promotes completed
// patches and generated images with the thread workspace.
func TestSidebarProtocolPromotionEntriesLikeRust(t *testing.T) {
	patch := &protocol.ThreadItem{
		Type:   "file_change",
		Status: "completed",
		Changes: []protocol.FileChange{
			{Path: "src/added.ts", Kind: "add"},
			{Path: "src/removed.ts", Kind: "delete"},
			{Path: "src/old.ts", Kind: "update", MovePath: "src/moved.ts"},
		},
	}
	entries := sidebarProtocolPromotionEntries("/workspace", patch)
	if len(entries) != 2 {
		t.Fatalf("entries = %+v, want the added and moved files", entries)
	}
	if entries[0].Path != "/workspace/src/moved.ts" || entries[1].Path != "/workspace/src/added.ts" {
		t.Fatalf("entries = %+v, want newest inside the patch first", entries)
	}

	image := &protocol.ThreadItem{Type: "imageGeneration", Status: "completed", SavedPath: "images/out.png"}
	imageEntries := sidebarProtocolPromotionEntries("/workspace", image)
	if len(imageEntries) != 1 || imageEntries[0].Path != "/workspace/images/out.png" {
		t.Fatalf("image entries = %+v", imageEntries)
	}
	incomplete := &protocol.ThreadItem{Type: "imageGeneration", Status: "inProgress", SavedPath: "images/out.png"}
	if entries := sidebarProtocolPromotionEntries("/workspace", incomplete); len(entries) != 0 {
		t.Fatalf("an unfinished image promoted %+v", entries)
	}
}

// Rust refresh_sidebar_files runs again whenever the chooser opens, so files
// mentioned since the previous open head the inventory.
func TestEmbeddedSidebarFilesHostRescansOnReopenLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store := newSessionStore()
	threadID := createSidebarFilesThread(t, store, "/workspace")
	now := time.Now().UTC()
	appendSidebarFilesItem(t, store, threadID, session.Item{
		ID: "agent-1", Type: "agent_message", Role: "assistant",
		Text: "see [first](src/first.ts)", CreatedAt: now,
		Metadata: map[string]any{"turn_id": "turn-1"},
	})
	host := newSidebarFilesHost(newEmbeddedSidebarFilesSource(store), func() string { return "/workspace" }, "")
	first := host.snapshot(codextea.SidebarFilesRequest{ParentThreadID: "parent-files", Resume: true})
	if len(first.Files) != 1 || first.Files[0].Path != "/workspace/src/first.ts" {
		t.Fatalf("first open = %+v", first.Files)
	}
	appendSidebarFilesItem(t, store, threadID, session.Item{
		ID: "agent-2", Type: "agent_message", Role: "assistant",
		Text: "see [second](src/second.ts)", CreatedAt: now.Add(time.Second),
		Metadata: map[string]any{"turn_id": "turn-2"},
	})
	second := host.snapshot(codextea.SidebarFilesRequest{ParentThreadID: "parent-files", Resume: true})
	if len(second.Files) != 2 {
		t.Fatalf("reopen = %+v, want the new mention merged", second.Files)
	}
	if second.Files[0].Path != "/workspace/src/second.ts" || second.Files[1].Path != "/workspace/src/first.ts" {
		t.Fatalf("reopen = %+v, want the newer mention first", second.Files)
	}
}
