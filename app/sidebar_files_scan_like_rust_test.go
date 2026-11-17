package app

import (
	"testing"

	"codex_go/appserver"
	"codex_go/tui/markdown"
)

// Rust parity: codex-rs/tui/src/app/sidebar_files.rs `start_sidebar_files_scan`
// and the `SidebarFilesLoaded` handler (#61920).

func TestSidebarFilesScanLoadsOnePageAtATimeLikeRust(t *testing.T) {
	inventory := newSidebarFilesInventory()
	inventory.filesFor("parent", "", stringPointerForSidebar("cursor-1"), false)

	request, ok := inventory.beginScan("parent")
	if !ok {
		t.Fatal("the first page request must start")
	}
	if request.Cursor != "cursor-1" || request.Request == "" || request.Parent != "parent" {
		t.Fatalf("request = %+v, want the reserved cursor and an id", request)
	}
	if _, again := inventory.beginScan("parent"); again {
		t.Fatal("a scan already in flight must not start a second page")
	}
	page := appserver.ThreadItemsListResponse{NextCursor: stringPointerForSidebar("cursor-2")}
	changed, ok := inventory.completePage(sidebarFilesScanResult{Parent: "parent", Request: request.Request, Page: &page})
	if !ok {
		t.Fatal("the matching page must be accepted")
	}
	if changed {
		t.Fatal("an empty page must not report a change")
	}
	next, ok := inventory.beginScan("parent")
	if !ok || next.Cursor != "cursor-2" {
		t.Fatalf("next request = %+v, want the page cursor", next)
	}
}

func TestSidebarFilesScanDropsStalePagesAfterReconnectLikeRust(t *testing.T) {
	inventory := newSidebarFilesInventory()
	inventory.filesFor("parent", "", stringPointerForSidebar("cursor-1"), false)
	stale, ok := inventory.beginScan("parent")
	if !ok {
		t.Fatal("the first page request must start")
	}
	inventory.connectionReplaced("parent")
	page := appserver.ThreadItemsListResponse{
		Data: []appserver.ThreadItemEntry{sidebarFilesMessage("m1", "turn-1", "[a](a.md)")},
	}
	if _, ok := inventory.completePage(sidebarFilesScanResult{Parent: "parent", Request: stale.Request, Page: &page}); ok {
		t.Fatal("a page from the replaced connection must be dropped")
	}
	if entries := inventory.entries("parent"); len(entries) != 0 {
		t.Fatalf("entries = %v, want the stale page dropped", entries)
	}
	if _, ok := inventory.beginScan("parent"); ok {
		t.Fatal("a replaced connection must wait for the resumed history cursor")
	}
	inventory.resumeHistoryCursor("parent", stringPointerForSidebar("cursor-9"))
	resumed, ok := inventory.beginScan("parent")
	if !ok || resumed.Cursor != "cursor-9" {
		t.Fatalf("resumed request = %+v, want the older-item cursor", resumed)
	}
}

func TestSidebarFilesScanRetriesAFailedPageLikeRust(t *testing.T) {
	inventory := newSidebarFilesInventory()
	inventory.filesFor("parent", "", stringPointerForSidebar("cursor-1"), false)
	request, ok := inventory.beginScan("parent")
	if !ok {
		t.Fatal("the first page request must start")
	}
	changed, ok := inventory.completePage(sidebarFilesScanResult{Parent: "parent", Request: request.Request})
	if !ok || changed {
		t.Fatalf("failed page = (%v, %v), want (false, true)", changed, ok)
	}
	retry, ok := inventory.beginScan("parent")
	if !ok || retry.Cursor != "cursor-1" {
		t.Fatalf("retry request = %+v, want the unadvanced cursor", retry)
	}
}

func TestSidebarFilesInventoryPromotesCompletedItemsLikeRust(t *testing.T) {
	inventory := newSidebarFilesInventory()
	inventory.filesFor("parent", "", nil, false)
	patch := appserver.ThreadItem{
		Type:   "fileChange",
		Status: "completed",
		Data: map[string]any{"changes": []any{
			map[string]any{"path": "docs/new.md", "kind": "add"},
			map[string]any{"path": "docs/old.md", "kind": "delete"},
		}},
	}
	if !inventory.promoteItem("parent", patch) {
		t.Fatal("a completed patch must promote its added file")
	}
	image := appserver.ThreadItem{
		Type:   "imageGeneration",
		Status: "inProgress",
		Data:   map[string]any{"savedPath": "images/out.png"},
	}
	if inventory.promoteItem("parent", image) {
		t.Fatal("an unfinished image must not be promoted")
	}
	entries := inventory.entries("parent")
	if len(entries) != 1 || entries[0].Path != "docs/new.md" {
		t.Fatalf("entries = %v, want the added file only", entries)
	}
}

func TestSidebarFilesInventoryReplacesWorkspaceLikeRust(t *testing.T) {
	inventory := newSidebarFilesInventory()
	files := inventory.filesFor("parent", "/workspace/one", nil, false)
	files.extend([]markdown.SidebarFileLink{{Path: "a.md"}})
	replaced := inventory.filesFor("parent", "/workspace/two", nil, false)
	if replaced == files {
		t.Fatal("a new workspace must replace the cached inventory")
	}
	if entries := inventory.entries("parent"); len(entries) != 0 {
		t.Fatalf("entries = %v, want the previous workspace dropped", entries)
	}
}
