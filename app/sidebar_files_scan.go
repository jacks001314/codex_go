package app

import (
	"context"
	"strings"
	"sync"

	"github.com/google/uuid"

	"codex_go/appserver"
	"codex_go/tui/markdown"
)

// Rust parity: codex-rs/tui/src/app/sidebar_files.rs
// `start_sidebar_files_scan` and the `AppEvent::SidebarFilesLoaded` handler
// (#61920).
//
// The inventory scans the owning conversation backwards while the chooser is
// open, one page at a time. Each page carries a request id so a reply from a
// replaced connection cannot merge into the newer scan, and a failed page
// retires the scan without advancing the cursor so reopening the chooser can
// retry it.

const sidebarFilesPageLimit = 100

// sidebarFilesPageRequest names one in-flight page.
type sidebarFilesPageRequest struct {
	Parent  string
	Request string
	Cursor  string
}

// sidebarFilesScanResult carries a finished page back to the inventory. A nil
// Page means the request failed.
type sidebarFilesScanResult struct {
	Parent  string
	Request string
	Page    *appserver.ThreadItemsListResponse
}

// sidebarFilesInventory keeps one file inventory per parent conversation.
type sidebarFilesInventory struct {
	mu    sync.Mutex
	panes map[string]*sidebarFiles
}

func newSidebarFilesInventory() *sidebarFilesInventory {
	return &sidebarFilesInventory{panes: map[string]*sidebarFiles{}}
}

// filesFor returns the inventory for a parent, replacing it when the workspace
// changed (Rust `pane.files.cwd != cwd` clears the cached files).
func (i *sidebarFilesInventory) filesFor(parent, cwd string, next *string, inspectLocalPaths bool) *sidebarFiles {
	if i == nil {
		return nil
	}
	parent = strings.TrimSpace(parent)
	cwd = strings.TrimSpace(cwd)
	i.mu.Lock()
	defer i.mu.Unlock()
	if files := i.panes[parent]; files != nil && files.cwd == cwd {
		return files
	}
	files := newSidebarFiles(cwd, next, inspectLocalPaths)
	i.panes[parent] = files
	return files
}

// connectionReplaced drops the incremental window so the resumed thread's
// history cursor becomes the next page (Rust `Files::connection_replaced`).
func (i *sidebarFilesInventory) connectionReplaced(parent string) {
	if i == nil {
		return
	}
	parent = strings.TrimSpace(parent)
	i.mu.Lock()
	defer i.mu.Unlock()
	if files := i.panes[parent]; files != nil {
		files.connectionReplaced()
	}
}

// resumeHistoryCursor restores `next` from the resumed thread's older item
// cursor. Resume may have missed files between the cached and hydrated windows,
// so the scan restarts before that cursor (Rust `needs_history_cursor`).
func (i *sidebarFilesInventory) resumeHistoryCursor(parent string, olderCursor *string) {
	if i == nil {
		return
	}
	parent = strings.TrimSpace(parent)
	i.mu.Lock()
	defer i.mu.Unlock()
	files := i.panes[parent]
	if files == nil || !files.needsHistoryCursor {
		return
	}
	files.needsHistoryCursor = false
	files.next = cloneStringPtrForSidebar(olderCursor)
}

// beginScan reserves the inventory for one page. It reports false when a scan
// is already in flight, history is exhausted, or a replaced connection has not
// restored the resumed cursor yet.
func (i *sidebarFilesInventory) beginScan(parent string) (sidebarFilesPageRequest, bool) {
	if i == nil {
		return sidebarFilesPageRequest{}, false
	}
	parent = strings.TrimSpace(parent)
	i.mu.Lock()
	defer i.mu.Unlock()
	files := i.panes[parent]
	if files == nil || files.scan != "" || files.next == nil || files.needsHistoryCursor {
		return sidebarFilesPageRequest{}, false
	}
	request := uuid.NewString()
	files.scan = request
	return sidebarFilesPageRequest{Parent: parent, Request: request, Cursor: *files.next}, true
}

// completePage merges a loaded page. ok is false for a stale reply that no
// longer owns the inventory, which the caller drops without scheduling a frame.
func (i *sidebarFilesInventory) completePage(result sidebarFilesScanResult) (changed bool, ok bool) {
	if i == nil {
		return false, false
	}
	parent := strings.TrimSpace(result.Parent)
	i.mu.Lock()
	defer i.mu.Unlock()
	files := i.panes[parent]
	if files == nil || files.scan == "" || files.scan != result.Request {
		return false, false
	}
	files.scan = ""
	if result.Page == nil {
		return false, true
	}
	return files.applyPage(*result.Page), true
}

// promoteItem folds a completed patch or generated image into the inventory
// before its page arrives (Rust `notice_sidebar_file_item`).
func (i *sidebarFilesInventory) promoteItem(parent string, item appserver.ThreadItem) bool {
	if i == nil {
		return false
	}
	parent = strings.TrimSpace(parent)
	switch remoteTUINormalizedThreadItemType(item.Type) {
	case "filechange", "imagegeneration":
	default:
		return false
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	files := i.panes[parent]
	if files == nil {
		return false
	}
	entries := sidebarNewestItemFileLinks(files.cwd, item)
	if len(entries) == 0 {
		return false
	}
	return files.promote(entries)
}

// entries snapshots the listed files for rendering.
func (i *sidebarFilesInventory) entries(parent string) []markdown.SidebarFileLink {
	if i == nil {
		return nil
	}
	parent = strings.TrimSpace(parent)
	i.mu.Lock()
	defer i.mu.Unlock()
	files := i.panes[parent]
	if files == nil {
		return nil
	}
	return append([]markdown.SidebarFileLink(nil), files.entries...)
}

// fetchSidebarFilesPage loads one backwards item page for the request (Rust
// `thread_items_page(parent, None, Some(cursor), 100)`).
func fetchSidebarFilesPage(ctx context.Context, client *remoteAppServerTUIClient, request sidebarFilesPageRequest) sidebarFilesScanResult {
	result := sidebarFilesScanResult{Parent: request.Parent, Request: request.Request}
	limit := sidebarFilesPageLimit
	params := appserver.ThreadItemsListParams{
		ThreadID:      request.Parent,
		Cursor:        &appserver.ThreadItemsListCursor{Opaque: request.Cursor},
		Limit:         &limit,
		SortDirection: appserver.SortDesc,
	}
	var page appserver.ThreadItemsListResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodThreadItemsList, params, &page); err != nil {
		return result
	}
	result.Page = &page
	return result
}
