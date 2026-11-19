package tui

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/mattn/go-runewidth"

	"codex_go/gitutil"
	"codex_go/utils"
)

// Rust parity: codex-rs/tui/src/resume_picker.rs.

const (
	SessionPickerPageSize          = 25
	SessionPickerLoadNearThreshold = 5
	sessionPickerDateWidth         = 12
	// sessionPickerFooterDefaultHeight stands in for the list viewport before
	// the first render records it (Rust view_rows defaults to None).
	sessionPickerFooterDefaultHeight = 10
)

type SessionPickerAction string

const (
	SessionPickerResume    SessionPickerAction = "resume"
	SessionPickerFork      SessionPickerAction = "fork"
	SessionPickerArchive   SessionPickerAction = "archive"
	SessionPickerUnarchive SessionPickerAction = "unarchive"
	SessionPickerDelete    SessionPickerAction = "delete"
)

func (a SessionPickerAction) Title() string {
	switch a {
	case SessionPickerFork:
		return "Fork a previous session"
	case SessionPickerArchive:
		return "Archive a previous session"
	case SessionPickerUnarchive:
		return "Unarchive a previous session"
	case SessionPickerDelete:
		return "Delete a previous session"
	default:
		return "Resume a previous session"
	}
}

func (a SessionPickerAction) Label() string {
	if strings.TrimSpace(string(a)) != "" {
		return string(a)
	}
	return string(SessionPickerResume)
}

type SessionSelectionKind string

const (
	SessionSelectionStartFresh SessionSelectionKind = "start_fresh"
	SessionSelectionResume     SessionSelectionKind = "resume"
	SessionSelectionFork       SessionSelectionKind = "fork"
	SessionSelectionArchive    SessionSelectionKind = "archive"
	SessionSelectionUnarchive  SessionSelectionKind = "unarchive"
	SessionSelectionDelete     SessionSelectionKind = "delete"
	SessionSelectionExit       SessionSelectionKind = "exit"
)

type SessionTarget struct {
	// BackendID is the logical owner of ThreadID, retained across connection
	// replacement (Rust #54681). A thread ID alone does not identify a backend.
	BackendID BackendID
	Path      string
	ThreadID  string
	// CWD is the working directory reported by thread/list or thread/read at
	// selection time (Rust SessionTarget::cwd).
	CWD string
}

// EnsureBackend rejects a selection that came from a different backend before
// the caller mutates session state or sends resume/fork/unarchive requests.
// Rust parity: resume_picker::SessionTarget::ensure_backend.
func (t SessionTarget) EnsureBackend(backendID BackendID) error {
	if t.BackendID != backendID {
		return errors.New("This session belongs to a different backend. Open the session picker again.")
	}
	return nil
}

func (t SessionTarget) DisplayLabel() string {
	if strings.TrimSpace(t.Path) != "" {
		return t.Path
	}
	if strings.TrimSpace(t.ThreadID) != "" {
		return "thread " + t.ThreadID
	}
	return "thread"
}

type SessionSelection struct {
	Kind   SessionSelectionKind
	Target SessionTarget
	Name   string
	// ResumeCWD is the working directory chosen for this resume after applying
	// `tui.resume_cwd`. An empty value means no override was chosen, so the
	// server keeps the directory saved with the thread.
	ResumeCWD string
}

type SessionFilterMode int

const (
	SessionFilterCWD SessionFilterMode = iota
	// SessionFilterRepo keeps sessions whose working directory is the filter
	// directory or the corresponding directory in a linked worktree of the same
	// repository (Rust #56308).
	SessionFilterRepo
	SessionFilterAll
)

func SessionFilterModeFromShowAll(showAll bool, filterCWD string) SessionFilterMode {
	if showAll || strings.TrimSpace(filterCWD) == "" {
		return SessionFilterAll
	}
	return SessionFilterCWD
}

// SessionFilterDirection selects which way a filter value cycles.
type SessionFilterDirection int

const (
	SessionFilterPrevious SessionFilterDirection = iota
	SessionFilterNext
)

// Change advances the filter in the given direction, mirroring Rust
// SessionFilterMode::change (#56308). Repo is only reachable when linked
// worktrees make it meaningful; cycling backwards from Cwd still moves to All.
func (m SessionFilterMode) Change(direction SessionFilterDirection, filterCWD string, repoAvailable bool) SessionFilterMode {
	hasCWD := strings.TrimSpace(filterCWD) != ""
	switch {
	case m == SessionFilterCWD && direction == SessionFilterNext && repoAvailable:
		return SessionFilterRepo
	case m == SessionFilterCWD:
		return SessionFilterAll
	case m == SessionFilterRepo && direction == SessionFilterNext:
		return SessionFilterAll
	case m == SessionFilterRepo:
		return SessionFilterCWD
	case m == SessionFilterAll && direction == SessionFilterPrevious && repoAvailable:
		return SessionFilterRepo
	case m == SessionFilterAll && hasCWD:
		return SessionFilterCWD
	}
	return SessionFilterAll
}

// Toggle advances the filter forward in the legacy two-state way (no Repo).
func (m SessionFilterMode) Toggle(filterCWD string) SessionFilterMode {
	return m.Change(SessionFilterNext, filterCWD, false)
}

// SessionPickerRepoFilterAvailable reports whether the picker should offer the
// Repo filter: the directory belongs to a repository with more than one
// canonical linked-worktree directory (Rust local_repo_filter_available).
func SessionPickerRepoFilterAvailable(cwd string) bool {
	cwds, ok := gitutil.LinkedWorktreeCWDs(strings.TrimSpace(cwd))
	if !ok {
		return false
	}
	return SessionPickerRepoFilterAvailableFromCWDs(cwds)
}

// SessionPickerRepoFilterAvailableFromCWDs answers the same question for a
// checkout set a caller already expanded, so the picker can discover linked
// worktrees once and still offer the Repo filter (Rust
// local_repo_filter_available).
func SessionPickerRepoFilterAvailableFromCWDs(cwds []string) bool {
	seen := map[string]bool{}
	for _, candidate := range cwds {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		resolved, err := filepath.EvalSymlinks(candidate)
		if err != nil {
			continue
		}
		seen[resolved] = true
	}
	return len(seen) > 1
}

type SessionListDensity int

const (
	SessionDensityComfortable SessionListDensity = iota
	SessionDensityDense
)

func (d SessionListDensity) Toggle() SessionListDensity {
	if d == SessionDensityDense {
		return SessionDensityComfortable
	}
	return SessionDensityDense
}

type SessionSortKey int

const (
	SessionSortCreatedAt SessionSortKey = iota
	SessionSortUpdatedAt
	SessionSortTitle
)

func (s SessionSortKey) Toggle() SessionSortKey {
	switch s {
	case SessionSortCreatedAt:
		return SessionSortUpdatedAt
	case SessionSortUpdatedAt:
		return SessionSortTitle
	default:
		return SessionSortCreatedAt
	}
}

type SessionPickerToolbarControl int

const (
	SessionPickerToolbarFilter SessionPickerToolbarControl = iota
	SessionPickerToolbarSort
)

func (c SessionPickerToolbarControl) Toggle() SessionPickerToolbarControl {
	if c == SessionPickerToolbarSort {
		return SessionPickerToolbarFilter
	}
	return SessionPickerToolbarSort
}

type SessionSummary struct {
	// BackendID is the logical source that listed this row. Zero means the row
	// came from the picker's own backend and inherits SessionPickerState.BackendID
	// when selected (Rust #56543 row.backend_id).
	BackendID BackendID
	ThreadID  string
	Path      string
	Title     string
	Preview   string
	CWD       string
	Branch    string
	Provider  string
	// Model is the thread's restored model from app-server metadata
	// (Rust #43360): resuming or switching applies it so later turns use the
	// thread's model instead of the local default.
	Model string
	// ForkedFromID is the parent thread of a forked conversation (Rust
	// ThreadSessionState.forked_from_id, #61183). It is per selection, so a
	// resumed thread never inherits another thread's parent.
	ForkedFromID string
	// ForkParentTitle caches the parent's display name for the fork event line.
	// An unavailable parent leaves it empty and the line shows only the id.
	ForkParentTitle string
	// DaybreakEnabled is the thread's persisted Daybreak preference, restored
	// on resume (Rust session.daybreak_enabled, #49861).
	DaybreakEnabled bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
	Archived        bool
	// UnreadRevision is the backend read receipt behind an unacknowledged
	// result (Rust #58264 resume_picker unread map). Empty means read.
	UnreadRevision string
}

// Unread reports whether the row carries an unacknowledged result.
func (s SessionSummary) Unread() bool {
	return strings.TrimSpace(s.UnreadRevision) != ""
}

type SessionPickerState struct {
	// BackendID stamps every selection this picker produces so a target can be
	// traced back to the backend that listed it (Rust #54681).
	BackendID BackendID
	// CloudBackends marks the logical backends that host Cloud conversations.
	// The marker is display-only and survives connection replacement (Rust
	// #61742 thread_label::ThreadLabelStyle).
	CloudBackends map[BackendID]bool
	Action        SessionPickerAction
	Items         []SessionSummary
	Selected      int
	Query         string
	FilterMode    SessionFilterMode
	FilterCWD     string
	// FilterCWDs is the expanded directory filter (the requested directory plus
	// linked worktrees' corresponding directories). It backs the Repo filter;
	// the Cwd filter always uses FilterCWD alone (Rust #56308).
	FilterCWDs []string
	// RepoFilterAvailable offers the Repo filter only when the directory has
	// more than one canonical linked-worktree directory (Rust #56308).
	RepoFilterAvailable bool
	ProviderFilter      string
	SortKey             SessionSortKey
	Density             SessionListDensity
	ToolbarFocus        SessionPickerToolbarControl
	Expanded            map[string]bool
	// TranscriptPreviews caches the lazy conversation preview of each expanded
	// session (Rust TranscriptPreviewState).
	TranscriptPreviews map[string]TranscriptPreviewState
	Loading            bool
	Error              string
	// UseThemeColors enables deterministic per-thread identity colors on
	// session titles (Rust #44857). ThemeID selects the accent palette.
	UseThemeColors bool
	ThemeID        string
	// MorePagesAvailable reports whether a page-down past the last row may wait
	// for the catalog to serve another page (Rust PaginationState.next_cursor).
	MorePagesAvailable bool
	// NextCursor is the backend continuation of the current listing (Rust
	// PaginationState::next_cursor). Empty exhausts the listing.
	NextCursor string
	// pendingPageDownTarget keeps a page-down distance while rows are still
	// arriving so a delayed page cannot drop the user's position (Rust
	// PickerState.pending_page_down_target, #57862).
	pendingPageDownTarget int
	hasPendingPageDown    bool
	// ScrollTop is the first visible session (Rust PickerState::scroll_top).
	// RenderWindow advances it just far enough to keep the selection on screen.
	ScrollTop int
	// viewWidth/viewHeight/viewNow record the list viewport of the last render
	// so row heights and the footer percent need no extra measuring pass
	// (Rust PickerState::view_width/view_rows/relative_time_reference).
	viewWidth  int
	viewHeight int
	viewNow    time.Time
	// frozenFooterPercent pins the scroll percent while a page is in flight so
	// the footer does not jump as rows arrive (Rust
	// PickerState::frozen_footer_percent).
	frozenFooterPercent    int
	frozenFooterPercentSet bool
	// listingToken identifies the current listing cycle. Pages carrying an
	// older token belong to a filter the user has already left (Rust
	// PickerState::listing_token).
	listingToken uint64
	// cwdNormalized memoizes directory normalization per raw path. A single
	// render pass reads every row's cwd more than once, and canonicalization
	// walks the filesystem, so normalize each distinct path once. Rust keeps
	// the same per-cwd result in PickerState::local_cwd_matches.
	cwdNormalized map[string]string
	// cwdMatches caches the directory-filter decision per raw row cwd for the
	// active filter. Rust clears local_cwd_matches whenever the listing
	// restarts; cwdMatchKey gives the cache the same lifetime without a reload.
	cwdMatches  map[string]bool
	cwdMatchKey string
	// cwdFilterIdentity caches the filter directory's repository identity so a
	// Repo filter does not re-walk it for every uncached row.
	cwdFilterIdentity    *gitutil.RepositoryIdentity
	cwdFilterIdentitySet bool
}

func NewSessionPickerState(action SessionPickerAction, items []SessionSummary, filterCWD string, filterCWDs ...string) *SessionPickerState {
	return NewSessionPickerStateForBackend(ProcessBackendID(), action, items, filterCWD, filterCWDs...)
}

// NewSessionPickerStateForBackend is NewSessionPickerState bound to an explicit
// backend identity, used when the picker lists sessions from a backend other
// than this process (Rust resume_picker::SessionPickerRunOptions::backend_id).
func NewSessionPickerStateForBackend(backendID BackendID, action SessionPickerAction, items []SessionSummary, filterCWD string, filterCWDs ...string) *SessionPickerState {
	state := &SessionPickerState{
		BackendID:      backendID,
		CloudBackends:  map[BackendID]bool{},
		Action:         action,
		Items:          append([]SessionSummary(nil), items...),
		UseThemeColors: true,
		FilterMode:     SessionFilterModeFromShowAll(false, filterCWD),
		FilterCWD:      filterCWD,
		FilterCWDs:     append([]string(nil), filterCWDs...),
		SortKey:        SessionSortUpdatedAt,
		ToolbarFocus:   SessionPickerToolbarFilter,
		Expanded:       map[string]bool{},
		cwdNormalized:  map[string]string{},
	}
	state.clampSelection()
	return state
}

func (s *SessionPickerState) VisibleItems() []SessionSummary {
	if s == nil {
		return nil
	}
	query := strings.ToLower(strings.TrimSpace(s.Query))
	provider := strings.ToLower(strings.TrimSpace(s.ProviderFilter))
	filterCWD := s.normalizedCWD(s.FilterCWD)
	var filterCWDSet map[string]bool
	if s.FilterMode == SessionFilterRepo {
		filterCWDSet = make(map[string]bool, len(s.FilterCWDs))
		for _, cwd := range s.FilterCWDs {
			if cleaned := s.normalizedCWD(cwd); cleaned != "" {
				filterCWDSet[cleaned] = true
			}
		}
	}
	s.resetCWDFilterCacheIfNeeded(filterCWD)
	items := make([]SessionSummary, 0, len(s.Items))
	for _, item := range s.Items {
		switch s.Action {
		case SessionPickerUnarchive:
			if !item.Archived {
				continue
			}
		case SessionPickerDelete:
		default:
			if item.Archived {
				continue
			}
		}
		switch s.FilterMode {
		case SessionFilterCWD:
			// Rust #56308: Cwd is directory-scoped, so linked worktrees no longer
			// leak into it.
			if !s.rowMatchesCWDFilter(item.CWD, filterCWD, nil) {
				continue
			}
		case SessionFilterRepo:
			if !s.rowMatchesCWDFilter(item.CWD, filterCWD, filterCWDSet) {
				continue
			}
		}
		if provider != "" && strings.ToLower(strings.TrimSpace(item.Provider)) != provider {
			continue
		}
		if query != "" && !sessionMatchesQuery(item, query) {
			continue
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool {
		a := items[i]
		b := items[j]
		switch s.SortKey {
		case SessionSortTitle:
			if cmp := strings.Compare(strings.ToLower(a.DisplayTitle()), strings.ToLower(b.DisplayTitle())); cmp != 0 {
				return cmp < 0
			}
		case SessionSortCreatedAt:
			if !a.CreatedAt.Equal(b.CreatedAt) {
				return a.CreatedAt.After(b.CreatedAt)
			}
		default:
			aTime := a.UpdatedAt
			if aTime.IsZero() {
				aTime = a.CreatedAt
			}
			bTime := b.UpdatedAt
			if bTime.IsZero() {
				bTime = b.CreatedAt
			}
			if !aTime.Equal(bTime) {
				return aTime.After(bTime)
			}
		}
		return a.ThreadID < b.ThreadID
	})
	return items
}

func (s *SessionPickerState) Move(delta int) {
	if s == nil {
		return
	}
	visible := s.VisibleItems()
	if len(visible) == 0 {
		s.Selected = 0
		return
	}
	// Rust stops at the ends instead of wrapping, so pressing down on the last
	// loaded row can page in older sessions (resume_picker.rs move_down).
	s.Selected += delta
	if s.Selected < 0 {
		s.Selected = 0
	}
	if s.Selected >= len(visible) {
		s.Selected = len(visible) - 1
	}
}

func (s *SessionPickerState) MovePage(delta int) {
	if s == nil {
		return
	}
	visible := s.VisibleItems()
	if len(visible) == 0 {
		s.Selected = 0
		s.hasPendingPageDown = false
		return
	}
	target := s.Selected + delta
	maxIndex := len(visible) - 1
	// Rust #57862: a forward page-down that runs past the loaded rows with more
	// pages still on the way keeps its distance until those rows arrive.
	if delta > 0 && target > maxIndex && s.MorePagesAvailable {
		s.pendingPageDownTarget = target
		s.hasPendingPageDown = true
		return
	}
	s.hasPendingPageDown = false
	s.Selected = target
	if s.Selected < 0 {
		s.Selected = 0
	}
	if s.Selected >= len(visible) {
		s.Selected = maxIndex
	}
}

func (s *SessionPickerState) SelectFirst() {
	if s != nil {
		s.Selected = 0
		s.clampSelection()
	}
}

func (s *SessionPickerState) SelectLast() {
	if s == nil {
		return
	}
	visible := s.VisibleItems()
	if len(visible) == 0 {
		s.Selected = 0
		return
	}
	s.Selected = len(visible) - 1
}

func (s *SessionPickerState) Select(index int) {
	if s == nil {
		return
	}
	visible := s.VisibleItems()
	if index >= 0 && index < len(visible) {
		s.Selected = index
	}
}

func (s *SessionPickerState) ToggleFilter() {
	if s == nil {
		return
	}
	s.ChangeFilter(SessionFilterNext)
}

// ChangeFilter cycles the filter in the given direction (Rust #56308).
func (s *SessionPickerState) ChangeFilter(direction SessionFilterDirection) {
	if s == nil {
		return
	}
	next := s.FilterMode.Change(direction, s.FilterCWD, s.RepoFilterAvailable)
	if next == s.FilterMode {
		return
	}
	s.FilterMode = next
	s.clampSelection()
}

// SessionPickerScope is the directory scope a picker listing asks for. It
// mirrors Rust's ThreadListCwdFilter: All requests no directory filter, and
// CWDs carries the filter set (the requested directory alone for Cwd, the
// expanded checkout set for Repo).
type SessionPickerScope struct {
	All  bool
	CWDs []string
	// Archived selects the status the listing pages over (Rust
	// thread_list_params.archived). A nil value keeps the legacy merged
	// listing that returns both statuses in one page.
	Archived *bool
	// Limit overrides the page size; zero uses SessionPickerPageSize.
	Limit int
}

// ListingScope is the scope the picker sends to thread/list for its current
// filter (Rust PageCwdFilter::for_request).
func (s *SessionPickerState) ListingScope() SessionPickerScope {
	if s == nil {
		return SessionPickerScope{All: true, Limit: SessionPickerPageSize}
	}
	scope := SessionPickerScope{Archived: s.listingStatus(), Limit: SessionPickerPageSize}
	switch s.FilterMode {
	case SessionFilterCWD:
		if cwd := strings.TrimSpace(s.FilterCWD); cwd != "" {
			scope.CWDs = []string{cwd}
			return scope
		}
	case SessionFilterRepo:
		if len(s.FilterCWDs) > 0 {
			scope.CWDs = append([]string(nil), s.FilterCWDs...)
			return scope
		}
		if cwd := strings.TrimSpace(s.FilterCWD); cwd != "" {
			scope.CWDs = []string{cwd}
			return scope
		}
	}
	scope.All = true
	return scope
}

// listingStatus reports the archive status the picker's action pages over.
// Rust drills into one SessionStatus at a time and toggles it from the
// toolbar; Go's Delete picker keeps listing both so a session of either kind
// can be removed.
func (s *SessionPickerState) listingStatus() *bool {
	if s == nil {
		return nil
	}
	switch s.Action {
	case SessionPickerUnarchive:
		archived := true
		return &archived
	case SessionPickerDelete:
		return nil
	default:
		archived := false
		return &archived
	}
}

// BeginListing starts a fresh cursor cycle and invalidates pages from an older
// filter (Rust PickerState::start_initial_load -> PaginationState::reset).
func (s *SessionPickerState) BeginListing() uint64 {
	if s == nil {
		return 0
	}
	s.listingToken++
	s.NextCursor = ""
	s.MorePagesAvailable = false
	s.hasPendingPageDown = false
	s.pendingPageDownTarget = 0
	s.ScrollTop = 0
	s.Error = ""
	s.Loading = true
	s.clearFrozenFooterPercent()
	return s.listingToken
}

// BeginPageLoad marks the next page in flight and freezes the footer percent
// at the position the user left (Rust load_more_if_needed ->
// freeze_footer_percent + PaginationState::start_load).
func (s *SessionPickerState) BeginPageLoad() {
	if s == nil {
		return
	}
	if !s.Loading {
		s.freezeFooterPercent()
	}
	s.Loading = true
}

// ListingToken identifies the current listing cycle.
func (s *SessionPickerState) ListingToken() uint64 {
	if s == nil {
		return 0
	}
	return s.listingToken
}

// SetNextPageCursor records the continuation returned by the current listing.
func (s *SessionPickerState) SetNextPageCursor(cursor string) {
	if s == nil {
		return
	}
	s.NextCursor = strings.TrimSpace(cursor)
	s.MorePagesAvailable = s.NextCursor != ""
}

// ShouldLoadNextPage reports whether the selection sits within
// LOAD_NEAR_THRESHOLD rows of the end of a listing that can serve another page
// (Rust maybe_load_more_for_scroll).
func (s *SessionPickerState) ShouldLoadNextPage() bool {
	if s == nil || s.Loading || !s.MorePagesAvailable || strings.TrimSpace(s.NextCursor) == "" {
		return false
	}
	visible := s.VisibleItems()
	if len(visible) == 0 {
		return false
	}
	return len(visible)-1-s.Selected <= SessionPickerLoadNearThreshold
}

// PendingPageDown reports a page-down that is waiting for older rows
// (Rust PickerState::pending_page_down_target).
func (s *SessionPickerState) PendingPageDown() bool {
	return s != nil && s.hasPendingPageDown
}

// SearchNeedsMorePages reports a typed query with no matching loaded rows
// while the listing can still serve pages. Rust keeps paging until the search
// finds a row or the listing is exhausted (resume_picker.rs set_query /
// continue_search_if_needed).
func (s *SessionPickerState) SearchNeedsMorePages() bool {
	if s == nil || strings.TrimSpace(s.Query) == "" || s.Loading || strings.TrimSpace(s.NextCursor) == "" {
		return false
	}
	return len(s.VisibleItems()) == 0
}

// rowSeparatorHeight is the blank line Rust paints between comfortable rows
// (resume_picker.rs row_separator_height).
func (s *SessionPickerState) rowSeparatorHeight() int {
	if s != nil && s.Density == SessionDensityDense {
		return 0
	}
	return 1
}

// renderedHeightBetween sums the rendered line count from start through
// endInclusive, including the comfortable separators between them (Rust
// PickerState::rendered_height_between).
func (s *SessionPickerState) renderedHeightBetween(start int, endInclusive int) int {
	if s == nil {
		return 0
	}
	visible := s.VisibleItems()
	if start < 0 {
		start = 0
	}
	if endInclusive >= len(visible) {
		endInclusive = len(visible) - 1
	}
	if start > endInclusive {
		return 0
	}
	height := 0
	for index := start; index <= endInclusive; index++ {
		height += len(s.renderSessionRows(index, visible[index], s.viewWidth, s.viewNow))
	}
	return height + s.rowSeparatorHeight()*(endInclusive-start)
}

// hasMoreAbove reports rows hidden above the window (Rust has_more_above).
func (s *SessionPickerState) hasMoreAbove() bool {
	return s != nil && s.ScrollTop > 0
}

// availableContentRows is the viewport height minus the rows reserved for the
// up/down more indicators (Rust available_content_rows).
func (s *SessionPickerState) availableContentRows(viewportHeight int) int {
	rows := viewportHeight
	if s.hasMoreAbove() {
		rows--
	}
	if strings.TrimSpace(s.NextCursor) != "" || s.Selected+1 < len(s.VisibleItems()) {
		rows--
	}
	if rows < 1 {
		return 1
	}
	return rows
}

// HasMoreBelow reports rows hidden below the window, either because the
// listing can serve another page or because the loaded rows overflow the
// viewport (Rust has_more_below).
func (s *SessionPickerState) HasMoreBelow(viewportHeight int) bool {
	if s == nil {
		return false
	}
	visible := s.VisibleItems()
	if len(visible) == 0 {
		return false
	}
	if strings.TrimSpace(s.NextCursor) != "" {
		return true
	}
	capacity := s.availableContentRows(viewportHeight)
	used := 0
	for index := s.ScrollTop; index < len(visible); index++ {
		if index > s.ScrollTop {
			used += s.rowSeparatorHeight()
		}
		used += len(s.renderSessionRows(index, visible[index], s.viewWidth, s.viewNow))
		if used > capacity {
			return true
		}
	}
	return false
}

// NeedsMinimumRowsForView reports a first page that does not fill the list
// viewport while older pages remain, so the picker should fetch more instead
// of leaving the window half empty (Rust ensure_minimum_rows_for_view).
func (s *SessionPickerState) NeedsMinimumRowsForView(minimumRows int) bool {
	if s == nil || minimumRows == 0 || s.Loading || strings.TrimSpace(s.NextCursor) == "" {
		return false
	}
	visible := s.VisibleItems()
	rendered := 0
	if len(visible) > 0 {
		rendered = s.renderedHeightBetween(0, len(visible)-1)
	}
	return rendered < s.availableContentRows(minimumRows)
}

func (s *SessionPickerState) ToggleDensity() {
	if s != nil {
		s.Density = s.Density.Toggle()
	}
}

func (s *SessionPickerState) ToggleSort() {
	if s != nil {
		switch s.SortKey {
		case SessionSortCreatedAt:
			s.SortKey = SessionSortUpdatedAt
		default:
			s.SortKey = SessionSortCreatedAt
		}
		s.clampSelection()
	}
}

func (s *SessionPickerState) ToggleToolbarFocus() {
	if s != nil {
		s.ToolbarFocus = s.ToolbarFocus.Toggle()
	}
}

func (s *SessionPickerState) ChangeFocusedToolbarValue() {
	s.ChangeFocusedToolbarValueDirection(SessionFilterNext)
}

// ChangeFocusedToolbarValueDirection changes the focused toolbar control,
// honoring the direction for the filter cycle (Rust #56308).
func (s *SessionPickerState) ChangeFocusedToolbarValueDirection(direction SessionFilterDirection) {
	if s == nil {
		return
	}
	switch s.ToolbarFocus {
	case SessionPickerToolbarSort:
		s.ToggleSort()
	default:
		s.ChangeFilter(direction)
	}
}

func (s *SessionPickerState) ToggleExpanded(threadID string) {
	if s == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	if s.Expanded == nil {
		s.Expanded = map[string]bool{}
	}
	if s.Expanded[threadID] {
		delete(s.Expanded, threadID)
	} else {
		s.Expanded[threadID] = true
	}
}

func (s *SessionPickerState) SelectedItem() (SessionSummary, bool) {
	if s == nil {
		return SessionSummary{}, false
	}
	visible := s.VisibleItems()
	if s.Selected < 0 || s.Selected >= len(visible) {
		return SessionSummary{}, false
	}
	return visible[s.Selected], true
}

func (s *SessionPickerState) Selection() (SessionSelection, bool) {
	item, ok := s.SelectedItem()
	if !ok {
		return SessionSelection{}, false
	}
	backendID := item.BackendID
	if backendID.IsZero() {
		backendID = s.BackendID
	}
	selection := SessionSelection{
		Target: SessionTarget{BackendID: backendID, Path: item.Path, ThreadID: item.ThreadID, CWD: item.CWD},
	}
	switch s.Action {
	case SessionPickerFork:
		selection.Kind = SessionSelectionFork
	case SessionPickerArchive:
		selection.Kind = SessionSelectionArchive
	case SessionPickerUnarchive:
		selection.Kind = SessionSelectionUnarchive
	case SessionPickerDelete:
		selection.Kind = SessionSelectionDelete
	default:
		selection.Kind = SessionSelectionResume
	}
	return selection, true
}

// SelectedTarget returns the identity of the selected session independent of
// its index in the lazily sorted list (Rust Row::seen_key). It survives rows
// arriving later and reordering the picker (#57862).
func (s *SessionPickerState) SelectedTarget() (SessionTarget, bool) {
	item, ok := s.SelectedItem()
	if !ok {
		return SessionTarget{}, false
	}
	return s.itemTarget(item), true
}

// SelectTarget re-selects the session with the given identity after a merge
// reordered the rows. It reports whether the session is still visible.
func (s *SessionPickerState) SelectTarget(target SessionTarget) bool {
	if s == nil {
		return false
	}
	key := sessionTargetRowKey(target)
	visible := s.VisibleItems()
	for index, item := range visible {
		if sessionTargetRowKey(s.itemTarget(item)) == key {
			s.Selected = index
			return true
		}
	}
	return false
}

// IngestItems merges newly-arrived sessions, keeps the list globally ordered by
// the selected timestamp, and restores the selected session plus any pending
// page-down distance (Rust PickerState::ingest_page, #57862).
func (s *SessionPickerState) IngestItems(items []SessionSummary) {
	if s == nil || len(items) == 0 {
		return
	}
	selected, hasSelected := s.SelectedTarget()
	distance := 0
	if s.hasPendingPageDown {
		distance = s.pendingPageDownTarget - s.Selected
		if distance < 0 {
			distance = 0
		}
	}
	seen := make(map[string]bool, len(s.Items)+len(items))
	indexByKey := make(map[string]int, len(s.Items)+len(items))
	for i, item := range s.Items {
		key := sessionTargetRowKey(s.itemTarget(item))
		seen[key] = true
		indexByKey[key] = i
	}
	for _, item := range items {
		key := sessionTargetRowKey(s.itemTarget(item))
		if seen[key] {
			// Rust ingest_page refreshes the unread receipt before skipping a
			// duplicate row (#58264/#58280).
			if i, ok := indexByKey[key]; ok {
				s.Items[i].UnreadRevision = strings.TrimSpace(item.UnreadRevision)
			}
			continue
		}
		seen[key] = true
		indexByKey[key] = len(s.Items)
		s.Items = append(s.Items, item)
	}
	// VisibleItems sorts on demand, so appending already keeps the merged list
	// globally ordered.
	if hasSelected {
		s.SelectTarget(selected)
	}
	if s.hasPendingPageDown {
		visible := s.VisibleItems()
		maxIndex := len(visible) - 1
		target := s.Selected + distance
		if target > maxIndex && s.MorePagesAvailable {
			s.pendingPageDownTarget = target
			return
		}
		s.hasPendingPageDown = false
		if len(visible) > 0 {
			if target < 0 {
				target = 0
			}
			if target > maxIndex {
				target = maxIndex
			}
			s.Selected = target
		}
	}
}

func (s *SessionPickerState) itemTarget(item SessionSummary) SessionTarget {
	backendID := item.BackendID
	if backendID.IsZero() {
		backendID = s.BackendID
	}
	return SessionTarget{BackendID: backendID, Path: item.Path, ThreadID: item.ThreadID, CWD: item.CWD}
}

// sessionTargetRowKey mirrors Rust Row::seen_key: a path identifies the row when
// present, otherwise the thread id does. The backend is part of the key so
// identical ids from different backends stay distinct.
func sessionTargetRowKey(target SessionTarget) string {
	if path := strings.TrimSpace(target.Path); path != "" {
		return "path\x00" + target.BackendID.String() + "\x00" + path
	}
	return "thread\x00" + target.BackendID.String() + "\x00" + target.ThreadID
}

func (s *SessionPickerState) NeedsNextPage() bool {
	if s == nil {
		return false
	}
	visible := s.VisibleItems()
	return len(visible) > 0 && len(visible)-1-s.Selected <= SessionPickerLoadNearThreshold
}

func (s *SessionPickerState) RenderRows(width int, now time.Time) []string {
	rows, _, _ := s.renderRows(width, now, 0)
	return rows
}

// RenderWindow renders at most maxRows lines scrolled so the selected session
// is visible (Rust PickerState::ensure_selected_visible + render_rows).
func (s *SessionPickerState) RenderWindow(width int, now time.Time, maxRows int) []string {
	rows, _, _ := s.renderRows(width, now, maxRows)
	return rows
}

// renderRows returns the rendered lines, the first visible session index, and
// the number of visible sessions. maxRows <= 0 renders the whole list.
func (s *SessionPickerState) renderRows(width int, now time.Time, maxRows int) ([]string, int, int) {
	if s == nil {
		return []string{"No sessions yet"}, 0, 0
	}
	if width > 0 {
		s.viewWidth = width
	}
	if maxRows > 0 {
		s.viewHeight = maxRows
	}
	s.viewNow = now
	if strings.TrimSpace(s.Error) != "" {
		return WrapLine(s.Error, WrapOptions{Width: width, BreakWords: true}), 0, 0
	}
	visible := s.VisibleItems()
	if len(visible) == 0 {
		if strings.TrimSpace(s.Query) != "" {
			// Rust render_empty_state_line: an active search says so while a
			// page is in flight, and otherwise reports no match.
			if s.Loading && strings.TrimSpace(s.NextCursor) != "" {
				return []string{"Searching..."}, 0, 0
			}
			return []string{"No results for your search"}, 0, 0
		}
		if s.Loading {
			if len(s.Items) == 0 {
				return []string{"Loading sessions..."}, 0, 0
			}
			return []string{"Loading older sessions..."}, 0, 0
		}
		return []string{"No sessions yet"}, 0, 0
	}
	start := 0
	capacity := 0
	if maxRows > 0 {
		start = s.ensureSelectedVisible(width, now, visible, maxRows)
		capacity = s.availableContentRows(maxRows)
	}
	rows := make([]string, 0, len(visible)*2+2)
	for index := start; index < len(visible); index++ {
		for _, line := range s.renderSessionRows(index, visible[index], width, now) {
			if capacity > 0 && len(rows) >= capacity {
				break
			}
			rows = append(rows, line)
		}
		if capacity > 0 && len(rows) >= capacity {
			break
		}
		// Rust paints one blank line between comfortable rows while space
		// remains in the content area (resume_picker.rs render_list).
		if s.rowSeparatorHeight() > 0 && index+1 < len(visible) {
			rows = append(rows, "")
		}
	}
	if s.Loading && (capacity <= 0 || len(rows) < capacity) {
		// Rust paints the trailing row while the next page is in flight
		// (resume_picker.rs render_rows).
		rows = append(rows, "  Loading older sessions...")
	}
	if maxRows > 0 {
		if s.hasMoreAbove() {
			rows = append([]string{"\u2191 more"}, rows...)
		}
		if s.HasMoreBelow(maxRows) {
			label := "\u2193 more"
			if s.Loading {
				label = "\u2193 loading more"
			}
			// Rust anchors the downward hint on the last row of the list
			// viewport, below any short content.
			for len(rows) < maxRows-1 {
				rows = append(rows, "")
			}
			rows = append(rows, label)
		}
		if len(rows) > maxRows {
			rows = rows[:maxRows]
		}
	}
	return rows, start, len(visible)
}

func (s *SessionPickerState) renderSessionRows(index int, item SessionSummary, width int, now time.Time) []string {
	selected := index == s.Selected
	var rows []string
	if s.Density == SessionDensityDense {
		rows = append(rows, s.renderDenseSessionRow(item, selected, width, now))
	} else {
		rows = append(rows, s.renderComfortableSessionRow(item, selected, width, now)...)
	}
	if s.Expanded[item.ThreadID] {
		rows = append(rows, renderExpandedSessionRows(item, s.itemCloud(item), width, now)...)
		rows = append(rows, s.renderTranscriptPreviewLines(item.ThreadID, width)...)
	}
	return rows
}

// ensureSelectedVisible scrolls only as far as needed to show the selection,
// mirroring Rust PickerState::ensure_selected_visible.
func (s *SessionPickerState) ensureSelectedVisible(width int, now time.Time, visible []SessionSummary, maxRows int) int {
	if s.Selected >= len(visible) {
		s.Selected = len(visible) - 1
	}
	if s.Selected < 0 {
		s.Selected = 0
	}
	if s.ScrollTop < 0 || s.ScrollTop >= len(visible) {
		s.ScrollTop = 0
	}
	if s.Selected < s.ScrollTop {
		s.ScrollTop = s.Selected
	}
	// Rust scrolls only as far as the content area requires, and the area
	// shrinks by one row once the top indicator appears.
	for s.ScrollTop < s.Selected && s.renderedHeightBetween(s.ScrollTop, s.Selected) > s.availableContentRows(maxRows) {
		s.ScrollTop++
	}
	return s.ScrollTop
}

func (s *SessionPickerState) SearchLine(width int) string {
	if s == nil {
		return ""
	}
	if strings.TrimSpace(s.Error) != "" {
		return TruncateWithEllipsis(strings.TrimSpace(s.Error), width)
	}
	search := "Type to search"
	if strings.TrimSpace(s.Query) != "" {
		search = "Search: " + s.Query
	}
	toolbar := s.ToolbarLine(false)
	if DisplayWidth(toolbar) > width-DisplayWidth(search)-2 {
		toolbar = s.ToolbarLine(true)
	}
	if width <= 0 {
		if toolbar == "" {
			return search
		}
		return search + "  " + toolbar
	}
	spacer := width - DisplayWidth(search) - DisplayWidth(toolbar)
	if spacer < 2 {
		spacer = 2
	}
	line := search + strings.Repeat(" ", spacer) + toolbar
	return TruncateWithEllipsis(line, width)
}

func (s *SessionPickerState) ToolbarLine(compact bool) string {
	if s == nil {
		return ""
	}
	return s.FilterControl(compact) + "   " + s.SortControl(compact)
}

func (s *SessionPickerState) FilterControl(compact bool) string {
	if s == nil {
		return ""
	}
	focused := s.ToolbarFocus == SessionPickerToolbarFilter
	if compact || strings.TrimSpace(s.FilterCWD) == "" {
		return "Filter:" + toolbarValue(filterModeLabel(s.FilterMode), true, focused)
	}
	values := "Filter: " +
		toolbarValue(filterModeLabel(SessionFilterCWD), s.FilterMode == SessionFilterCWD, focused)
	if s.RepoFilterAvailable {
		values += toolbarValue(filterModeLabel(SessionFilterRepo), s.FilterMode == SessionFilterRepo, focused)
	}
	return values + toolbarValue(filterModeLabel(SessionFilterAll), s.FilterMode == SessionFilterAll, focused)
}

func (s *SessionPickerState) SortControl(compact bool) string {
	if s == nil {
		return ""
	}
	focused := s.ToolbarFocus == SessionPickerToolbarSort
	if compact {
		return "Sort:" + toolbarValue(sortKeyLabel(s.SortKey), true, focused)
	}
	return "Sort: " +
		toolbarValue(sortKeyLabel(SessionSortUpdatedAt), s.SortKey == SessionSortUpdatedAt, focused) +
		toolbarValue(sortKeyLabel(SessionSortCreatedAt), s.SortKey == SessionSortCreatedAt, focused)
}

func toolbarValue(label string, active bool, focused bool) string {
	if active {
		value := "[" + label + "]"
		if focused {
			return value
		}
		return value
	}
	return " " + label + " "
}

func filterModeLabel(mode SessionFilterMode) string {
	switch mode {
	case SessionFilterRepo:
		return "Repo"
	case SessionFilterAll:
		return "All"
	}
	return "Cwd"
}

// normalizedCWD canonicalizes a directory once per picker. Rust resume_picker
// canonicalizes on every paths_match call but caches the per-row decision in
// PickerState::local_cwd_matches, which is the behavior mirrored here.
func (s *SessionPickerState) normalizedCWD(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	if s.cwdNormalized == nil {
		s.cwdNormalized = map[string]string{}
	}
	if normalized, ok := s.cwdNormalized[raw]; ok {
		return normalized
	}
	normalized := cleanPathForCompare(raw)
	s.cwdNormalized[raw] = normalized
	return normalized
}

// resetCWDFilterCacheIfNeeded drops cached row decisions when the active
// directory filter changes, matching Rust's clear on every listing restart.
func (s *SessionPickerState) resetCWDFilterCacheIfNeeded(filterCWD string) {
	key := strconv.Itoa(int(s.FilterMode)) + "\x00" + filterCWD
	if s.FilterMode == SessionFilterRepo && len(s.FilterCWDs) > 0 {
		cwds := append([]string(nil), s.FilterCWDs...)
		sort.Strings(cwds)
		key += "\x00" + strings.Join(cwds, "\x00")
	}
	if key == s.cwdMatchKey {
		return
	}
	s.cwdMatchKey = key
	s.cwdMatches = map[string]bool{}
	s.cwdFilterIdentity = nil
	s.cwdFilterIdentitySet = false
}

// rowMatchesCWDFilter reports whether a row is inside the active directory
// filter and memoizes the answer per raw cwd (Rust
// PickerState::row_matches_filter).
func (s *SessionPickerState) rowMatchesCWDFilter(itemCWD string, filterCWD string, filterCWDSet map[string]bool) bool {
	raw := strings.TrimSpace(itemCWD)
	if filterCWD == "" {
		return true
	}
	if raw == "" {
		return false
	}
	if matches, ok := s.cwdMatches[raw]; ok {
		return matches
	}
	normalized := s.normalizedCWD(raw)
	matches := normalized == filterCWD || filterCWDSet[normalized]
	if !matches && s.FilterMode == SessionFilterRepo {
		matches = s.repoFilterMatches(itemCWD)
	}
	if s.cwdMatches == nil {
		s.cwdMatches = map[string]bool{}
	}
	s.cwdMatches[raw] = matches
	return matches
}

// repoFilterMatches covers the Repository scope: the corresponding directory
// of a linked checkout of the same repository (Rust
// PickerState::row_matches_local_cwd for SessionFilterMode::Repo).
func (s *SessionPickerState) repoFilterMatches(itemCWD string) bool {
	rowIdentity, ok := gitutil.RepositoryIdentityForCWD(strings.TrimSpace(itemCWD))
	if !ok {
		return false
	}
	if !s.cwdFilterIdentitySet {
		s.cwdFilterIdentity, _ = gitutil.RepositoryIdentityForCWD(strings.TrimSpace(s.FilterCWD))
		s.cwdFilterIdentitySet = true
	}
	if s.cwdFilterIdentity == nil {
		return false
	}
	return rowIdentity.CommonDir == s.cwdFilterIdentity.CommonDir &&
		rowIdentity.RelativeCWD == s.cwdFilterIdentity.RelativeCWD
}

func sortKeyLabel(key SessionSortKey) string {
	if key == SessionSortCreatedAt {
		return "Created"
	}
	return "Updated"
}

func (s *SessionPickerState) FooterLines(width int, existingSession bool) []string {
	if s == nil {
		return nil
	}
	separatorWidth := width
	if separatorWidth <= 0 {
		separatorWidth = 80
	}
	separator := renderFooterProgress(separatorWidth, s.footerProgressLabel(separatorWidth))
	escWide := "start new"
	escCompact := "new"
	ctrlC := "quit"
	if existingSession {
		escWide = "exit"
		escCompact = "exit"
		ctrlC = "exit"
	}
	if strings.TrimSpace(s.Query) != "" {
		escWide = "clear search"
		escCompact = "clear"
	}
	densityWide := "dense view"
	densityCompact := "dense"
	if s.Density == SessionDensityDense {
		densityWide = "comfortable view"
		densityCompact = "comfy"
	}
	// Rust #49804 (resume_picker.rs): the ctrl chords of the picker footer render
	// through the shared key-label table (^ on Linux, ⌃ on macOS).
	ctrl := ModifierLabelPrefix(ControlKeyLabel())
	if width > 0 && width < 120 {
		return []string{
			separator,
			"enter " + s.Action.Label() + "   esc " + escCompact + "   " + ctrl + "c " + ctrlC + "   tab focus   \u2190/\u2192 option",
			ctrl + "o " + densityCompact + "   " + ctrl + "t preview   " + ctrl + "e exp   \u2191/\u2193 browse",
		}
	}
	return []string{
		separator,
		"enter " + s.Action.Label() + "   esc " + escWide + "   " + ctrl + "c " + ctrlC + "   tab focus sort/filter   \u2190/\u2192 change option",
		ctrl + "o " + densityWide + "   " + ctrl + "t transcript   " + ctrl + "e expand   \u2191/\u2193 browse",
	}
}

// renderFooterProgress draws the dim rule with the progress label at its right
// edge, one column in from the end (Rust render_picker_footer_separator).
func renderFooterProgress(width int, label string) string {
	runes := []rune(strings.Repeat("\u2500", width))
	labelRunes := []rune(label)
	if len(labelRunes) == 0 {
		return string(runes)
	}
	start := width - len(labelRunes) - 1
	if start < 0 {
		return string(runes)
	}
	copy(runes[start:], labelRunes)
	return string(runes)
}

// footerProgressLabel is the "position / total · percent" text Rust fits into
// the separator, trying the wide form first and falling back to just the
// percent (Rust picker_footer_progress_label).
func (s *SessionPickerState) footerProgressLabel(width int) string {
	position := 0
	if len(s.VisibleItems()) > 0 {
		position = s.Selected + 1
	}
	total := FormatInt(int64(len(s.VisibleItems())))
	if s.Loading {
		// The total is a lower bound until the last page lands.
		total += "\u2026"
	}
	percent := FormatInt(int64(s.FooterPercent()))
	labels := []string{
		" " + FormatInt(int64(position)) + " / " + total + " \u00b7 " + percent + "% ",
		" " + FormatInt(int64(position)) + "/" + total + " \u00b7 " + percent + "% ",
		" " + percent + "% ",
	}
	for _, label := range labels {
		if DisplayWidth(label) < width {
			return label
		}
	}
	return ""
}

// FooterPercent is the footer's scroll progress. While a page loads it stays
// at the position the user left (Rust picker_footer_percent).
func (s *SessionPickerState) FooterPercent() int {
	if s == nil {
		return 100
	}
	if s.Loading {
		if s.frozenFooterPercentSet {
			return s.frozenFooterPercent
		}
		if len(s.VisibleItems()) == 0 {
			return 0
		}
	}
	return s.footerScrollPercent()
}

// footerScrollPercent maps the rows scrolled past the top of the viewport onto
// 0-100, reporting 100 whenever the whole listing already fits (Rust
// picker_footer_scroll_percent).
func (s *SessionPickerState) footerScrollPercent() int {
	visible := s.VisibleItems()
	if len(visible) == 0 {
		return 100
	}
	height := s.viewHeight
	if height <= 0 {
		height = sessionPickerFooterDefaultHeight
	}
	contentRows := s.availableContentRows(height)
	total := s.renderedHeightBetween(0, len(visible)-1)
	maxScroll := total - contentRows
	if maxScroll <= 0 {
		return 100
	}
	if s.renderedHeightBetween(s.ScrollTop, len(visible)-1) <= contentRows {
		return 100
	}
	skipped := 0
	if s.ScrollTop > 0 {
		skipped = s.renderedHeightBetween(0, s.ScrollTop-1)
	}
	if skipped > maxScroll {
		skipped = maxScroll
	}
	return (skipped*100 + maxScroll/2) / maxScroll
}

func (s *SessionPickerState) freezeFooterPercent() {
	if s == nil {
		return
	}
	s.frozenFooterPercent = s.footerScrollPercent()
	s.frozenFooterPercentSet = true
}

func (s *SessionPickerState) clearFrozenFooterPercent() {
	if s == nil {
		return
	}
	s.frozenFooterPercent = 0
	s.frozenFooterPercentSet = false
}

// ClearFrozenFooterPercent releases a frozen footer percent, used when a page
// fails and the listing stops growing.
func (s *SessionPickerState) ClearFrozenFooterPercent() {
	s.clearFrozenFooterPercent()
}

// ResetPendingPageDown drops a page-down that is still waiting for rows.
func (s *SessionPickerState) ResetPendingPageDown() {
	if s == nil {
		return
	}
	s.hasPendingPageDown = false
	s.pendingPageDownTarget = 0
}

func (s *SessionPickerState) clampSelection() {
	if s == nil {
		return
	}
	visible := s.VisibleItems()
	if len(visible) == 0 {
		s.Selected = 0
		return
	}
	if s.Selected < 0 {
		s.Selected = 0
	}
	if s.Selected >= len(visible) {
		s.Selected = len(visible) - 1
	}
}

func (s SessionSummary) DisplayTitle() string {
	title := strings.TrimSpace(s.Title)
	if title != "" {
		return title
	}
	if preview := strings.TrimSpace(s.Preview); preview != "" {
		return preview
	}
	if strings.TrimSpace(s.ThreadID) != "" {
		return "Untitled session " + s.ThreadID
	}
	return "Untitled session"
}

func (s SessionSummary) DisplayPreview() string {
	preview := strings.TrimSpace(s.Preview)
	if preview != "" {
		return preview
	}
	title := strings.TrimSpace(s.Title)
	if title != "" {
		return title
	}
	return "(no message yet)"
}

func padDisplayRight(value string, width int) string {
	if width <= 0 {
		return value
	}
	padding := width - DisplayWidth(value)
	if padding <= 0 {
		return value
	}
	return value + strings.Repeat(" ", padding)
}

func sessionMatchesQuery(item SessionSummary, query string) bool {
	haystack := strings.ToLower(strings.Join([]string{
		item.ThreadID,
		item.Path,
		item.DisplayTitle(),
		item.DisplayPreview(),
		item.CWD,
		item.Branch,
		item.Provider,
	}, "\n"))
	return strings.Contains(haystack, query)
}

// sessionTitleColor returns the truecolor SGR prefix for a row title, or ""
// when theme colors are disabled, the thread id is empty, or the theme has no
// accents (Rust #44857: fall back to the terminal default).
func (s *SessionPickerState) sessionTitleColor(item SessionSummary) string {
	if s == nil || !s.UseThemeColors || strings.TrimSpace(item.ThreadID) == "" {
		return ""
	}
	return ThreadColorSGR(ThreadColorForTheme(item.ThreadID, s.ThemeID))
}

// itemCloud reports whether the row belongs to a Cloud backend. The marker is
// separate from the row's backend identity (Rust #61742 PickerState::label_style).
func (s *SessionPickerState) itemCloud(item SessionSummary) bool {
	if s == nil || len(s.CloudBackends) == 0 {
		return false
	}
	backendID := item.BackendID
	if backendID.IsZero() {
		backendID = s.BackendID
	}
	return s.CloudBackends[backendID]
}

func sessionPickerThreadLabel(text string, cloud bool, width int) string {
	prefix := ThreadLabelPrefix(cloud)
	if prefix == "" {
		return TruncateWithEllipsis(text, width)
	}
	if width <= DisplayWidth(prefix) {
		return TruncateWithEllipsis(prefix, width)
	}
	return prefix + TruncateWithEllipsis(text, width-DisplayWidth(prefix))
}

func (s *SessionPickerState) renderDenseSessionRow(item SessionSummary, selected bool, width int, now time.Time) string {
	prefix := SelectionPrefix(selected)
	updated := item.UpdatedAt
	if updated.IsZero() {
		updated = item.CreatedAt
	}
	date := padDisplayRight(relativeTime(updated, now), sessionPickerDateWidth)
	title := item.DisplayPreview()
	cloud := s.itemCloud(item)
	unread := item.Unread()
	markerWidth := 0
	if unread {
		// The marker glyph plus its separating space (Rust session_summary_line).
		markerWidth = 2
	}
	if width > 0 {
		available := width - DisplayWidth(prefix) - DisplayWidth(date) - markerWidth
		if available < 0 {
			available = 0
		}
		title = sessionPickerThreadLabel(title, cloud, available)
	} else if cloud {
		title = ThreadLabelPrefix(true) + title
	}
	row := prefix + date
	if unread {
		row += StyledTaskMarker(true) + " "
	}
	if color := s.sessionTitleColor(item); color != "" {
		// Keep the identity accent on the title only; reset to the terminal
		// default afterwards.
		row += color + title + "\x1b[39m"
	} else {
		row += title
	}
	if selected {
		row = RenderSelectedRow(row)
	}
	return row
}

func (s *SessionPickerState) renderComfortableSessionRow(item SessionSummary, selected bool, width int, now time.Time) []string {
	prefix := SelectionPrefix(selected)
	unread := item.Unread()
	wrapWidth := width
	if unread && wrapWidth > 0 {
		// Reserve the marker glyph plus its separating space (Rust #58264).
		wrapWidth -= 2
	}
	title := prefix
	if unread {
		title += taskMarkerUnreadGlyph + " "
	}
	title += ThreadLabelPrefix(s.itemCloud(item))
	title += item.DisplayPreview()
	lines := AdaptiveWrapLine(title, WrapOptions{
		Width:            wrapWidth,
		SubsequentIndent: "  ",
		BreakWords:       true,
	})
	color := s.sessionTitleColor(item)
	for i := range lines {
		if color != "" {
			// The marker/indent occupies the first two display columns.
			lines[i] = colorThreadTitleLine(lines[i], color)
		}
		if unread && i == 0 {
			// Splice the fixed-color dot in place of the plain glyph so both the
			// selection style and the thread accent leave it blue.
			lines[i] = strings.Replace(lines[i], taskMarkerUnreadGlyph+" ", StyledTaskMarker(true)+" ", 1)
		}
		if selected {
			// Selection bold/blue wraps the whole line; the identity color set
			// on the title survives (Rust keeps thread colors when selected).
			lines[i] = RenderSelectedRow(lines[i])
		}
	}
	meta := []string{}
	updated := item.UpdatedAt
	if updated.IsZero() {
		updated = item.CreatedAt
	}
	if !updated.IsZero() {
		meta = append(meta, relativeTime(updated, now))
	}
	if branch := strings.TrimSpace(item.Branch); branch != "" {
		meta = append(meta, "branch: "+branch)
	}
	if cwd := compactPath(item.CWD); cwd != "" {
		meta = append(meta, "cwd: "+cwd)
	}
	if len(meta) > 0 {
		lines = append(lines, AdaptiveWrapLine("  "+strings.Join(meta, "  "), WrapOptions{
			Width:            width,
			SubsequentIndent: "  ",
			BreakWords:       true,
		})...)
	}
	return lines
}

// colorThreadTitleLine applies the identity color to a row's title text while
// leaving the two-column marker/indent prefix unstyled.
func colorThreadTitleLine(line string, color string) string {
	if color == "" {
		return line
	}
	const prefixWidth = 2
	if DisplayWidth(line) <= prefixWidth {
		return color + line + "\x1b[39m"
	}
	runes := []rune(line)
	used := 0
	index := 0
	for index < len(runes) && used < prefixWidth {
		used += runewidth.RuneWidth(runes[index])
		index++
	}
	return string(runes[:index]) + color + string(runes[index:]) + "\x1b[39m"
}

// sessionMetaBranchIcon is Rust SESSION_META_BRANCH_ICON.
const sessionMetaBranchIcon = "\ue0a0"

// renderExpandedSessionRows mirrors Rust render_expanded_session_details: the
// labeled Session/Created/Updated/Directory/Branch block followed by the
// conversation header the preview lines hang off.
func renderExpandedSessionRows(item SessionSummary, cloud bool, width int, now time.Time) []string {
	threadID := strings.TrimSpace(item.ThreadID)
	title := strings.TrimSpace(item.Title)
	session := "-"
	switch {
	case title != "" && threadID != "":
		session = title + " (" + threadID + ")"
	case title != "":
		session = title
	case threadID != "":
		session = threadID
	}
	branch := strings.TrimSpace(item.Branch)
	if branch == "" {
		branch = "no branch"
	}
	sessionLine := expandedDetailLine("Session:", session, width)
	if cloud && title != "" {
		sessionLine = expandedDetailLabelLine("Session:", session, cloud, width)
	}
	details := []string{
		sessionLine,
		expandedTimeDetailLine("Created:", item.CreatedAt, now, width),
		expandedTimeDetailLine("Updated:", item.UpdatedAt, now, width),
		expandedDetailLine("Directory:", displaySessionDirectory(item.CWD), width),
		expandedDetailLine("Branch:", sessionMetaBranchIcon+" "+branch, width),
		"  \u2502",
		"  \u2502 Conversation:",
	}
	return details
}

// expandedDetailLabelLine is expanded_detail_line with a decorated label that
// keeps Rust's ellipsis truncation (Rust #61742 label_style.render).
func expandedDetailLabelLine(label string, value string, cloud bool, width int) string {
	const labelWidth = 10
	valueWidth := width - 4 - labelWidth - 2
	if valueWidth < 1 {
		valueWidth = 1
	}
	padded := label
	if pad := labelWidth - DisplayWidth(label); pad > 0 {
		padded += strings.Repeat(" ", pad)
	}
	return "  \u2502 " + padded + "  " + sessionPickerThreadLabel(value, cloud, valueWidth)
}

// expandedDetailLine mirrors Rust expanded_detail_line: a fixed-width dim label
// with a truncated value.
func expandedDetailLine(label string, value string, width int) string {
	const labelWidth = 10
	valueWidth := width - 4 - labelWidth - 2
	if valueWidth < 1 {
		valueWidth = 1
	}
	padded := label
	if pad := labelWidth - DisplayWidth(label); pad > 0 {
		padded += strings.Repeat(" ", pad)
	}
	return "  \u2502 " + padded + "  " + TruncateToWidth(value, valueWidth)
}

// expandedTimeDetailLine mirrors Rust expanded_time_detail_line.
func expandedTimeDetailLine(label string, ts time.Time, now time.Time, width int) string {
	if ts.IsZero() {
		return expandedDetailLine(label, "-", width)
	}
	value := relativeTimeLong(now, ts) + " \u00b7 " + SystemClockFormat().FormatDateTime(ts)
	return expandedDetailLine(label, value, width)
}

// relativeTimeLong mirrors Rust format_relative_time_long.
func relativeTimeLong(reference time.Time, ts time.Time) string {
	if reference.IsZero() {
		reference = time.Now()
	}
	seconds := int64(reference.Sub(ts) / time.Second)
	if seconds < 0 {
		seconds = 0
	}
	switch {
	case seconds == 0:
		return "now"
	case seconds < 60:
		return pluralTime(seconds, "second")
	case seconds < 3600:
		return pluralTime(seconds/60, "minute")
	case seconds < 24*3600:
		return pluralTime(seconds/3600, "hour")
	default:
		return pluralTime(seconds/(24*3600), "day")
	}
}

func pluralTime(value int64, unit string) string {
	if value == 1 {
		return "1 " + unit + " ago"
	}
	return FormatInt(value) + " " + unit + "s ago"
}

// displaySessionDirectory mirrors Rust format_directory_display with no max
// width: home-relative paths use "~".
func displaySessionDirectory(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "-"
	}
	if home, err := os.UserHomeDir(); err == nil && strings.TrimSpace(home) != "" {
		if rel, err := filepath.Rel(home, trimmed); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			if rel == "." {
				return "~"
			}
			return "~" + string(filepath.Separator) + rel
		}
	}
	return trimmed
}

func cleanPathForCompare(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if normalized, err := utils.NormalizeForPathComparison(path); err == nil {
		return normalized
	}
	cleaned, err := filepath.Abs(path)
	if err != nil {
		cleaned = filepath.Clean(path)
	}
	return strings.ToLower(filepath.Clean(cleaned))
}

// PathsMatch reports whether two paths identify the same normalized location.
// Rust parity: resume_picker::paths_match -> path_utils::paths_match_after_normalization.
func PathsMatch(left string, right string) bool {
	return utils.PathsMatchAfterNormalization(left, right)
}

func compactPath(path string) string {
	path = filepath.Clean(strings.TrimSpace(path))
	if path == "." || path == "" {
		return ""
	}
	volume := filepath.VolumeName(path)
	trimmed := strings.TrimPrefix(path, volume)
	parts := strings.FieldsFunc(trimmed, func(r rune) bool {
		return r == '/' || r == '\\'
	})
	if len(parts) <= 3 {
		return filepath.ToSlash(path)
	}
	return filepath.ToSlash(filepath.Join(append([]string{volume + string(filepath.Separator), "..."}, parts[len(parts)-2:]...)...))
}

func relativeTime(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	if now.IsZero() {
		now = time.Now()
	}
	d := now.Sub(t)
	if d < 0 {
		d = -d
	}
	switch {
	case d < time.Minute:
		return "now"
	case d < time.Hour:
		return FormatInt(int64(d/time.Minute)) + "m ago"
	case d < 24*time.Hour:
		return FormatInt(int64(d/time.Hour)) + "h ago"
	case d < 30*24*time.Hour:
		return FormatInt(int64(d/(24*time.Hour))) + "d ago"
	default:
		return t.Format("2006-01-02")
	}
}
