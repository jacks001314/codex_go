package app

import (
	"context"
	"runtime"
	"strings"
	"sync"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/protocol"
	"codex_go/session"
	"codex_go/tui/markdown"
	codextea "codex_go/tui/tea"
)

// Rust parity: codex-rs/tui/src/app/sidebar_files.rs `refresh_sidebar_files`
// and `start_sidebar_files_scan` (#61920).
//
// The inventory itself lives in sidebar_files.go / sidebar_files_scan.go. This
// file adapts it to the TUI: one chooser open restarts the scan at the newest
// page, and every page that still has history asks the model for another one.

// sidebarFilesPageSource loads one backwards item page of a conversation.
// A nil page reports a failed read and leaves the cursor in place so the next
// chooser open retries it (Rust retires the scan without advancing `next`).
type sidebarFilesPageSource interface {
	// Reset drops per-conversation caches before a fresh chooser scan.
	Reset(parent string)
	// Page returns the newest remaining page at `cursor`.
	Page(parent string, cursor string) *appserver.ThreadItemsListResponse
}

// sidebarFilesHost renders the shared inventory as a TUI snapshot.
type sidebarFilesHost struct {
	inventory         *sidebarFilesInventory
	source            sidebarFilesPageSource
	cwd               func() string
	preview           func() string
	inspectLocalPaths bool
}

func newSidebarFilesHost(source sidebarFilesPageSource, cwd func() string, preview func() string) *sidebarFilesHost {
	return &sidebarFilesHost{
		inventory:         newSidebarFilesInventory(),
		source:            source,
		cwd:               cwd,
		preview:           preview,
		inspectLocalPaths: true,
	}
}

// newRemoteSidebarFilesHost keeps unresolved links: a remote workspace path may
// live on another host, so Rust's remote path never stats or drops it.
func newRemoteSidebarFilesHost(source sidebarFilesPageSource, cwd func() string, preview func() string) *sidebarFilesHost {
	host := newSidebarFilesHost(source, cwd, preview)
	host.inspectLocalPaths = false
	return host
}

// snapshot loads one page and reports whether older history remains. The TUI
// chains another request while Loading stays true.
func (h *sidebarFilesHost) snapshot(request codextea.SidebarFilesRequest) codextea.SidebarFilesSnapshot {
	if h == nil {
		return codextea.SidebarFilesSnapshot{}
	}
	parent := strings.TrimSpace(request.ParentThreadID)
	if parent == "" {
		return codextea.SidebarFilesSnapshot{}
	}
	cwd := ""
	if h.cwd != nil {
		cwd = strings.TrimSpace(h.cwd())
	}
	var start *string
	if request.Resume {
		// Rust refresh_sidebar_files rescans from the newest page on open; the
		// inventory merges the repeat pass idempotently.
		empty := ""
		start = &empty
		if h.source != nil {
			h.source.Reset(parent)
		}
	}
	h.inventory.filesFor(parent, cwd, start, h.inspectLocalPaths)
	if request.Resume {
		h.inventory.restart(parent)
	}
	loading := false
	if h.source != nil {
		if pageRequest, ok := h.inventory.beginScan(parent); ok {
			page := h.source.Page(parent, pageRequest.Cursor)
			if _, accepted := h.inventory.completePage(sidebarFilesScanResult{
				Parent:  parent,
				Request: pageRequest.Request,
				Page:    page,
			}); accepted && page != nil {
				loading = h.inventory.hasMore(parent)
			}
		}
	}
	preview := ""
	if h.preview != nil {
		preview = strings.TrimSpace(h.preview())
	}
	return codextea.SidebarFilesSnapshot{
		Files:              h.inventory.entries(parent),
		Loading:            loading,
		CWD:                cwd,
		PreviewUnavailable: preview,
	}
}

// embeddedSidebarFilesSource pages the local store. The embedded TUI has no
// app-server session, so it reads the same record the router would page and
// keeps it for the duration of one scan.
type embeddedSidebarFilesSource struct {
	mu      sync.Mutex
	store   *session.Store
	records map[string]*session.Record
}

func newEmbeddedSidebarFilesSource(store *session.Store) *embeddedSidebarFilesSource {
	return &embeddedSidebarFilesSource{store: store, records: map[string]*session.Record{}}
}

func (s *embeddedSidebarFilesSource) Reset(parent string) {
	if s == nil {
		return
	}
	parent = strings.TrimSpace(parent)
	s.mu.Lock()
	delete(s.records, parent)
	s.mu.Unlock()
}

func (s *embeddedSidebarFilesSource) Page(parent string, cursor string) *appserver.ThreadItemsListResponse {
	if s == nil || s.store == nil {
		return nil
	}
	parent = strings.TrimSpace(parent)
	record := s.record(parent)
	if record == nil {
		return nil
	}
	limit := sidebarFilesPageLimit
	params := appserver.ThreadItemsListParams{
		ThreadID:      parent,
		Limit:         &limit,
		SortDirection: appserver.SortDesc,
	}
	if cursor = strings.TrimSpace(cursor); cursor != "" {
		params.Cursor = &appserver.ThreadItemsListCursor{Opaque: cursor}
	}
	page, err := appserver.BuildItemsResponse(record, &params)
	if err != nil {
		return nil
	}
	for index := range page.Data {
		page.Data[index].Item = normalizeEmbeddedSidebarThreadItem(page.Data[index].Item)
	}
	return page
}

// normalizeEmbeddedSidebarThreadItem maps the legacy store shape onto the
// app-server payload the sidebar scan reads. Persisted user and agent messages
// are stored as `message` with a role, while Rust's payload carries
// `userMessage`/`agentMessage` and a user message's text lives in its content.
func normalizeEmbeddedSidebarThreadItem(item appserver.ThreadItem) appserver.ThreadItem {
	role := remoteTUINormalizedThreadItemRole(item.Role)
	switch remoteTUINormalizedThreadItemType(item.Type) {
	case "message", "usermessage":
		if role != "assistant" && role != "agent" {
			item.Type = "userMessage"
			item.Role = "user"
			if len(item.Content) == 0 && strings.TrimSpace(item.Text) != "" {
				item.Content = []appserver.ThreadItemContent{{Type: "text", Text: item.Text}}
			}
			return item
		}
		item.Type = "agentMessage"
		return item
	case "assistantmessage":
		item.Type = "agentMessage"
		if role == "" {
			item.Role = "assistant"
		}
		return item
	default:
		return item
	}
}

func (s *embeddedSidebarFilesSource) record(parent string) *session.Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.records[parent]; record != nil {
		return record
	}
	record, err := s.store.Read(session.ThreadID(parent), true, true)
	if err != nil || record == nil {
		return nil
	}
	s.records[parent] = record
	return record
}

// sidebarPreviewUnavailableForEmbedded mirrors Rust's file_preview_unavailable
// for a server-local embedded workspace. The preview renderer only draws in a
// POSIX terminal, so a Windows host reports the same notice.
func sidebarPreviewUnavailableForEmbedded() string {
	if runtime.GOOS == "windows" {
		return "Preview unavailable on Windows"
	}
	return ""
}

// sidebarPreviewUnavailableForRemote mirrors Rust's Windows branch for a
// server-owned workspace. This path has no local windows-sandbox host, so the
// remaining Rust condition is the server platform family.
func sidebarPreviewUnavailableForRemote(platformFamily string, usesRemoteWorkspace bool) string {
	if strings.EqualFold(strings.TrimSpace(platformFamily), "windows") ||
		(runtime.GOOS == "windows" && !usesRemoteWorkspace) {
		return "Preview unavailable on Windows"
	}
	return ""
}

// remoteSidebarFilesSource pages the owning thread over an app-server
// connection. One connection serves every page of a single chooser open and
// closes when the scan finishes, fails, or the chooser reopens.
type remoteSidebarFilesSource struct {
	mu             sync.Mutex
	ctx            context.Context
	endpoint       *appserverdaemon.RemoteAppServerEndpoint
	client         *remoteAppServerTUIClient
	platformFamily string
}

func newRemoteSidebarFilesSource(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) *remoteSidebarFilesSource {
	if ctx == nil {
		ctx = context.Background()
	}
	return &remoteSidebarFilesSource{ctx: ctx, endpoint: endpoint}
}

func (s *remoteSidebarFilesSource) Reset(parent string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closeLocked()
}

func (s *remoteSidebarFilesSource) Page(parent string, cursor string) *appserver.ThreadItemsListResponse {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.client == nil {
		client, err := openRemoteSessionClient(s.ctx, s.endpoint)
		if err != nil {
			return nil
		}
		s.client = client
	}
	if family := strings.TrimSpace(s.client.platformFamily); family != "" {
		s.platformFamily = family
	}
	result := fetchSidebarFilesPage(s.ctx, s.client, sidebarFilesPageRequest{Parent: parent, Cursor: cursor})
	if result.Page == nil || result.Page.NextCursor == nil {
		s.closeLocked()
	}
	return result.Page
}

// platformFamilyName reports the server family the last connection announced.
func (s *remoteSidebarFilesSource) platformFamilyName() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.platformFamily
}

func (s *remoteSidebarFilesSource) closeLocked() {
	if s.client == nil {
		return
	}
	s.client.close()
	s.client = nil
}

// sidebarProtocolPromotionEntries is the embedded counterpart of Rust's
// notice_sidebar_file_item: a completed patch or generated image becomes a
// chooser row before the page scan reaches it.
func sidebarProtocolPromotionEntries(cwd string, item *protocol.ThreadItem) []markdown.SidebarFileLink {
	if item == nil {
		return nil
	}
	switch remoteTUINormalizedThreadItemType(item.Type) {
	case "filechange":
		if !strings.EqualFold(strings.TrimSpace(item.Status), "completed") {
			return nil
		}
		changes := make([]any, 0, len(item.Changes))
		for _, change := range item.Changes {
			entry := map[string]any{"path": change.Path}
			if strings.TrimSpace(change.MovePath) != "" {
				entry["kind"] = map[string]any{"type": change.Kind, "move_path": change.MovePath}
			} else {
				entry["kind"] = change.Kind
			}
			changes = append(changes, entry)
		}
		return sidebarNewestItemFileLinks(cwd, appserver.ThreadItem{
			Type:   "fileChange",
			Status: item.Status,
			Data:   map[string]any{"changes": changes},
		})
	case "imagegeneration":
		if !strings.EqualFold(strings.TrimSpace(item.Status), "completed") {
			return nil
		}
		return sidebarNewestItemFileLinks(cwd, appserver.ThreadItem{
			Type:   "imageGeneration",
			Status: item.Status,
			Data:   map[string]any{"savedPath": item.SavedPath},
		})
	}
	return nil
}
