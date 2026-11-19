package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
)

// SessionPickerScope aliases the picker's directory scope so callers of
// OnListSessionPickerItems can name it without importing the tui package
// twice. Rust: ThreadListCwdFilter.
type SessionPickerScope = codextui.SessionPickerScope

// SessionPickerItemsFunc lists one page of the resume picker's sessions from
// the backend this TUI is connected to (Rust #58280
// BackendSessions::picker_views). cursor is empty for the first page, and
// nextCursor continues the listing (Rust ThreadListParams.cursor). The notice
// mirrors interactiveRemoteSessionPickerItems' partial-load message.
type SessionPickerItemsFunc func(scope SessionPickerScope, cursor string) (items []codextui.SessionSummary, nextCursor string, notice string, err error)

// SessionPickerRefreshMsg reports a live listing loaded while the picker is
// open. PickerID scopes the result to the modal that requested it.
type SessionPickerRefreshMsg struct {
	PickerID string
	Items    []codextui.SessionSummary
	// NextCursor continues the listing; empty means the last page (Rust
	// PaginationState::complete_page).
	NextCursor string
	Notice     string
	Err        error
	// ListingToken rejects a page requested under an older filter (Rust
	// PickerState::listing_token). Zero accepts every listing for tests.
	ListingToken uint64
	// Reset marks the first page of a new listing, which replaces the rows
	// already shown instead of merging into them (Rust start_initial_load).
	Reset bool
}

// SessionResumeLookupMsg reports a backend listing loaded to resolve
// `/resume <id-or-name>` (Rust lookup_session_target_with_app_server).
type SessionResumeLookupMsg struct {
	Query  string
	Items  []codextui.SessionSummary
	Notice string
	Err    error
}

// SessionPickerRepoFilterMsg reports linked-checkout discovery for the open
// picker. Rust performs the same scan on a blocking worker and applies the
// result to PickerState::repo_filter_available after the first frame
// (resume_picker.rs local_repo_filter_available).
type SessionPickerRepoFilterMsg struct {
	PickerID  string
	Available bool
	CWDs      []string
}

// sessionPickerRefreshCmd lists sessions through the connected backend so an
// in-app picker sees sessions created after startup (Rust #58280).
func (m *Model) sessionPickerRefreshCmd(pickerID string, picker *codextui.SessionPickerState) bubbletea.Cmd {
	if m == nil || m.onListSessionPickerItems == nil {
		return nil
	}
	list := m.onListSessionPickerItems
	scope := picker.ListingScope()
	token := picker.BeginListing()
	return func() bubbletea.Msg {
		items, next, notice, err := list(scope, "")
		return SessionPickerRefreshMsg{
			PickerID:     pickerID,
			Items:        items,
			NextCursor:   next,
			Notice:       notice,
			Err:          err,
			ListingToken: token,
			Reset:        true,
		}
	}
}

// sessionPickerPageCmd fetches the picker's next page. It runs only when the
// current listing can continue and no page is already in flight, mirroring
// Rust PaginationState::next_page.
func (m *Model) sessionPickerPageCmd(pickerID string, picker *codextui.SessionPickerState) bubbletea.Cmd {
	if m == nil || m.onListSessionPickerItems == nil || picker == nil {
		return nil
	}
	cursor := strings.TrimSpace(picker.NextCursor)
	if cursor == "" || picker.Loading {
		return nil
	}
	list := m.onListSessionPickerItems
	scope := picker.ListingScope()
	token := picker.ListingToken()
	picker.BeginPageLoad()
	return func() bubbletea.Msg {
		items, next, notice, err := list(scope, cursor)
		if strings.TrimSpace(next) == cursor {
			// A backend that repeats its cursor would otherwise loop forever.
			next = ""
		}
		return SessionPickerRefreshMsg{
			PickerID:     pickerID,
			Items:        items,
			NextCursor:   next,
			Notice:       notice,
			Err:          err,
			ListingToken: token,
		}
	}
}

// sessionPickerRepoFilterCmd discovers the linked checkouts behind the Repo
// filter off the Update goroutine so opening the picker never blocks on the
// repository walk.
func (m *Model) sessionPickerRepoFilterCmd(pickerID string) bubbletea.Cmd {
	if m == nil || !m.worktreesEnabled {
		return nil
	}
	cwd := strings.TrimSpace(m.sessionCWD)
	if cwd == "" {
		return nil
	}
	return func() bubbletea.Msg {
		cwds := codextui.SessionPickerWorktreeCWDs(cwd, true)
		return SessionPickerRepoFilterMsg{
			PickerID:  pickerID,
			Available: codextui.SessionPickerRepoFilterAvailableFromCWDs(cwds),
			CWDs:      cwds,
		}
	}
}

// applySessionPickerRepoFilter publishes the discovered checkout set to the
// modal that asked for it, ignoring replies for a picker that already closed.
func (m *Model) applySessionPickerRepoFilter(msg SessionPickerRepoFilterMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.sessionPicker == nil {
		return nil
	}
	if strings.TrimSpace(msg.PickerID) != "" && strings.TrimSpace(m.modal.id) != strings.TrimSpace(msg.PickerID) {
		return nil
	}
	picker := m.modal.sessionPicker
	picker.RepoFilterAvailable = msg.Available
	if len(msg.CWDs) > 0 {
		picker.FilterCWDs = msg.CWDs
	}
	if picker.FilterMode == codextui.SessionFilterRepo {
		// The user reached Repo before discovery finished, so the Cwd-scoped
		// listing has to be repeated with the expanded checkout set.
		return m.sessionPickerRefreshCmd(m.modal.id, picker)
	}
	return nil
}

// applySessionResumeLookup merges the backend listing into the cached rows and
// resolves the pending id-or-name lookup against it.
func (m *Model) applySessionResumeLookup(msg SessionResumeLookupMsg) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if msg.Err == nil {
		m.sessionItems = mergeSessionPickerItems(m.sessionItems, msg.Items)
	}
	if strings.TrimSpace(msg.Notice) != "" {
		m.sessionPickerLoadNotice = strings.TrimSpace(msg.Notice)
	}
	return m.applyResumeQueryResult(strings.TrimSpace(msg.Query))
}

// applySessionPickerRefresh merges a live listing into the open picker while
// preserving the selected row and any pending page-down distance (Rust #58280
// levels the picker's rows on the live backend listing).
func (m *Model) applySessionPickerRefresh(msg SessionPickerRefreshMsg) bubbletea.Cmd {
	// Rust drops a page whose picker already closed (the background task is
	// discarded with the picker), so a late listing never lands in the cache.
	if m == nil || m.modal == nil || m.modal.sessionPicker == nil {
		return nil
	}
	if strings.TrimSpace(msg.PickerID) != "" && strings.TrimSpace(m.modal.id) != strings.TrimSpace(msg.PickerID) {
		return nil
	}
	picker := m.modal.sessionPicker
	if msg.ListingToken != 0 && msg.ListingToken != picker.ListingToken() {
		return nil
	}
	picker.Loading = false
	if strings.TrimSpace(msg.Notice) != "" {
		m.modal.sessionPickerNotice = strings.TrimSpace(msg.Notice)
	}
	if msg.Err != nil {
		picker.ClearFrozenFooterPercent()
		if len(picker.Items) > 0 {
			// Rust stops paging a listing that failed a later page instead of
			// retrying it from every keypress.
			picker.NextCursor = ""
			picker.MorePagesAvailable = false
			picker.ResetPendingPageDown()
		}
		if m.sessionPickerLoadNotice == "" {
			m.modal.sessionPicker.Error = strings.TrimSpace(msg.Err.Error())
		}
		return nil
	}
	picker.Error = ""
	if msg.Reset {
		picker.Items = nil
		picker.Selected = 0
	}
	m.sessionItems = mergeSessionPickerItems(m.sessionItems, msg.Items)
	picker.SetNextPageCursor(msg.NextCursor)
	picker.IngestItems(msg.Items)
	m.syncSessionPickerModalSelection()
	if picker.ShouldLoadNextPage() || picker.SearchNeedsMorePages() || picker.NeedsMinimumRowsForView(sessionPickerListHeight(m.height)) {
		return m.sessionPickerPageCmd(m.modal.id, picker)
	}
	return nil
}

// mergeSessionPickerItems appends unseen rows and refreshes the unread receipt
// of existing ones, matching Rust's ingest_page + update_unread ordering.
func mergeSessionPickerItems(existing []codextui.SessionSummary, incoming []codextui.SessionSummary) []codextui.SessionSummary {
	if len(incoming) == 0 {
		return existing
	}
	index := make(map[string]int, len(existing))
	for i := range existing {
		index[sessionPickerItemKey(existing[i])] = i
	}
	for _, item := range incoming {
		key := sessionPickerItemKey(item)
		if i, ok := index[key]; ok && key != "" {
			existing[i].UnreadRevision = strings.TrimSpace(item.UnreadRevision)
			continue
		}
		if key != "" {
			index[key] = len(existing)
		}
		existing = append(existing, item)
	}
	return existing
}

// sessionPickerItemKey mirrors SessionSummary rows that identify by path when
// they carry no thread id.
func sessionPickerItemKey(item codextui.SessionSummary) string {
	if threadID := strings.TrimSpace(item.ThreadID); threadID != "" {
		return "thread\x00" + item.BackendID.String() + "\x00" + threadID
	}
	if path := strings.TrimSpace(item.Path); path != "" {
		return "path\x00" + item.BackendID.String() + "\x00" + path
	}
	return ""
}
