package app

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/appserver"
	"codex_go/tui/markdown"
)

// Rust parity: codex-rs/tui/src/app/sidebar_files.rs `Files` (#61920).

func sidebarFilesMessage(id, turnID, text string) appserver.ThreadItemEntry {
	return appserver.ThreadItemEntry{
		TurnID: turnID,
		Item:   appserver.ThreadItem{ID: id, Type: "agentMessage", Text: text},
	}
}

func sidebarFilesPaths(files *sidebarFiles) []string {
	paths := make([]string, 0, len(files.entries))
	for _, entry := range files.entries {
		paths = append(paths, entry.Path)
	}
	return paths
}

func TestSidebarFilesApplyPageDedupesAndFillsLocationLikeRust(t *testing.T) {
	files := newSidebarFiles("/workspace/repo", nil, false)
	firstPage := appserver.ThreadItemsListResponse{
		Data: []appserver.ThreadItemEntry{
			sidebarFilesMessage("m1", "turn-1", "[a](a.md) and [b](b.md)"),
		},
		NextCursor: stringPointerForSidebar("cursor-2"),
	}
	if files.applyPage(firstPage) {
		t.Fatal("a page with a next cursor must not publish entries yet")
	}
	if len(files.entries) != 0 {
		t.Fatalf("entries = %v, want the turn buffered", sidebarFilesPaths(files))
	}
	secondPage := appserver.ThreadItemsListResponse{
		Data: []appserver.ThreadItemEntry{
			sidebarFilesMessage("m2", "turn-1", "[b](b.md:7:2) and [c](c.md)"),
		},
	}
	if !files.applyPage(secondPage) {
		t.Fatal("the final page must publish entries")
	}
	// Every item is scanned newest-first, so one message's links list in reverse.
	paths := sidebarFilesPaths(files)
	if len(paths) != 3 || paths[0] != "/workspace/repo/b.md" || paths[1] != "/workspace/repo/a.md" || paths[2] != "/workspace/repo/c.md" {
		t.Fatalf("paths = %v", paths)
	}
	if files.entries[0].Location == nil || files.entries[0].Location.Line != 7 || files.entries[0].Location.Column != 2 {
		t.Fatalf("b.md location = %#v", files.entries[0].Location)
	}
	if files.next != nil {
		t.Fatalf("next cursor = %v", *files.next)
	}
}

func TestSidebarFilesStopsOnRepeatedCursorLikeRust(t *testing.T) {
	files := newSidebarFiles("", stringPointerForSidebar("cursor-1"), false)
	page := appserver.ThreadItemsListResponse{
		Data:       []appserver.ThreadItemEntry{sidebarFilesMessage("m1", "turn-1", "[a](a.md)")},
		NextCursor: stringPointerForSidebar("cursor-1"),
	}
	files.applyPage(page)
	if files.next != nil {
		t.Fatalf("repeated cursor accepted: %v", *files.next)
	}
	if paths := sidebarFilesPaths(files); len(paths) != 1 || paths[0] != "a.md" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSidebarFilesSkipsReviewModeUserMessagesLikeRust(t *testing.T) {
	files := newSidebarFiles("", nil, false)
	files.applyPage(appserver.ThreadItemsListResponse{Data: []appserver.ThreadItemEntry{
		{TurnID: "turn-1", Item: appserver.ThreadItem{ID: "exit", Type: "exitedReviewMode"}},
		{TurnID: "turn-1", Item: appserver.ThreadItem{ID: "review-prompt", Type: "userMessage",
			Content: []appserver.ThreadItemContent{{Type: "text", Text: "review [hidden](hidden.md)"}}}},
		{TurnID: "turn-1", Item: appserver.ThreadItem{ID: "enter", Type: "enteredReviewMode"}},
		sidebarFilesMessage("visible", "turn-1", "[visible](visible.md)"),
	}})
	paths := sidebarFilesPaths(files)
	if len(paths) != 1 || paths[0] != "visible.md" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSidebarFilesSkipsPrivateRealtimeItemsBeforeHandoffLikeRust(t *testing.T) {
	files := newSidebarFiles("", nil, false)
	files.applyPage(appserver.ThreadItemsListResponse{Data: []appserver.ThreadItemEntry{
		sidebarFilesMessage("analysis", "turn-voice", "[ANALYSIS] thinking about [secret](secret.md)"),
		{TurnID: "turn-voice", Item: appserver.ThreadItem{ID: "handoff", Type: "userMessage",
			Content: []appserver.ThreadItemContent{{Type: "text",
				Text: "<realtime_delegation><input>spoken</input></realtime_delegation>"}}}},
		sidebarFilesMessage("final", "turn-voice", "[answer](answer.md)"),
	}})
	paths := sidebarFilesPaths(files)
	if len(paths) != 1 || paths[0] != "answer.md" {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSidebarFilesPromoteKeepsConfirmedEntriesLikeRust(t *testing.T) {
	files := newSidebarFiles("", nil, false)
	files.applyPage(appserver.ThreadItemsListResponse{Data: []appserver.ThreadItemEntry{
		sidebarFilesMessage("m1", "turn-1", "[a](a.md) [b](b.md)"),
	}})
	if !files.promote([]markdown.SidebarFileLink{{Path: "x.md"}}) {
		t.Fatal("promoting a new entry must report a change")
	}
	paths := sidebarFilesPaths(files)
	if len(paths) != 3 || paths[0] != "x.md" || paths[1] != "b.md" || paths[2] != "a.md" {
		t.Fatalf("paths = %v", paths)
	}
	if files.confirmed != 3 {
		t.Fatalf("confirmed = %d", files.confirmed)
	}
}

func TestSidebarFilesSkipsLocalDirectoriesLikeRust(t *testing.T) {
	root := t.TempDir()
	directory := filepath.Join(root, "docs")
	if err := os.MkdirAll(directory, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	file := filepath.Join(root, "notes.md")
	if err := os.WriteFile(file, []byte("# notes"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	files := newSidebarFiles(root, nil, true)
	files.applyPage(appserver.ThreadItemsListResponse{Data: []appserver.ThreadItemEntry{
		sidebarFilesMessage("m1", "turn-1", "["+directory+"]("+directory+") ["+file+"]("+file+")"),
	}})
	paths := sidebarFilesPaths(files)
	if len(paths) != 1 || paths[0] != filepath.ToSlash(file) {
		t.Fatalf("paths = %v", paths)
	}
}

func TestSidebarFilesConnectionReplacedResetsScanLikeRust(t *testing.T) {
	files := newSidebarFiles("", nil, false)
	files.applyPage(appserver.ThreadItemsListResponse{Data: []appserver.ThreadItemEntry{
		sidebarFilesMessage("m1", "turn-1", "[a](a.md)"),
	}})
	files.connectionReplaced()
	if !files.needsHistoryCursor || files.confirmed != 0 || files.next != nil ||
		len(files.seenCursors) != 0 || len(files.olderTurn) != 0 || files.reviewMode {
		t.Fatalf("connection reset state = %#v", files)
	}
}

func stringPointerForSidebar(value string) *string {
	return &value
}
