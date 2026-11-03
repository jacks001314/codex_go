package agentsoverview

// Rust parity: codex-rs/tui/src/app/agents_overview_discovery.rs (#49106). The
// command center seeds a recent window of tasks and offers a keyboard-selectable
// "Show more" row that publishes the next page of retained history.

// AgentsOverviewPageSize is the number of tasks one "Show more" activation
// publishes (Rust PAGE_SIZE).
const AgentsOverviewPageSize = 10

// ShowMoreIndex is the sentinel SelectableIndices returns for the "Show more"
// row (Rust uses usize::MAX).
const ShowMoreIndex = int(^uint(0) >> 1)

// SetHistoryPaging switches the command center to a paged task list: only the
// seeded window renders until the user asks for more. Rust always pages the
// list; Go keeps it opt-in so callers that already return a bounded list keep
// their current layout.
func (v *View) SetHistoryPaging(enabled bool) {
	if v == nil {
		return
	}
	v.paged = enabled
	if enabled && v.historyLimit < AgentsOverviewPageSize {
		v.historyLimit = AgentsOverviewPageSize
	}
	v.refreshHistoryPaging()
}

// HistoryPaging reports whether the paged list is active.
func (v *View) HistoryPaging() bool {
	return v != nil && v.paged
}

// HistoryLimit returns the number of task rows currently published.
func (v *View) HistoryLimit() int {
	if v == nil {
		return 0
	}
	return v.historyLimit
}

// SetHistoryLimit publishes a specific number of task rows; it is the resume
// point after a restart.
func (v *View) SetHistoryLimit(limit int) {
	if v == nil {
		return
	}
	if limit < AgentsOverviewPageSize {
		limit = AgentsOverviewPageSize
	}
	v.historyLimit = limit
	v.refreshHistoryPaging()
}

// HasMore reports whether older tasks remain undiscovered behind "Show more".
func (v *View) HasMore() bool {
	return v != nil && v.hasMore
}

// SetHasMore lets the host report that another page exists (Rust has_more).
func (v *View) SetHasMore(hasMore bool) {
	if v == nil {
		return
	}
	v.hasMore = hasMore
	if !hasMore {
		v.showMoreLoading = false
		v.showMoreFailed = false
		if v.Selected == ShowMoreIndex {
			v.fitSelection()
		}
	}
}

// ShowMoreLoading reports an in-flight page request.
func (v *View) ShowMoreLoading() bool {
	return v != nil && v.showMoreLoading
}

// SetShowMoreLoading records that a page request is running; a loading row is
// still selectable so search never hides it (Rust state.loading).
func (v *View) SetShowMoreLoading(loading bool) {
	if v == nil {
		return
	}
	v.showMoreLoading = loading
	if loading {
		v.showMoreFailed = false
	}
}

// ShowMoreFailed reports that the last page request failed; the row renders as
// "Show more (retry)".
func (v *View) ShowMoreFailed() bool {
	return v != nil && v.showMoreFailed
}

// SetShowMoreFailed records a failed page request.
func (v *View) SetShowMoreFailed(failed bool) {
	if v == nil {
		return
	}
	v.showMoreFailed = failed
	if failed {
		v.showMoreLoading = false
	}
}

// ShowMoreLabel is the row text for the current request state.
func (v *View) ShowMoreLabel() string {
	switch {
	case v == nil:
		return ""
	case v.showMoreLoading:
		return "Loading more\u2026"
	case v.showMoreFailed:
		return "Show more (retry)"
	default:
		return "Show more"
	}
}

// ShowMoreSelected reports whether the "Show more" row owns the selection.
func (v *View) ShowMoreSelected() bool {
	return v != nil && v.hasMore && v.Selected == ShowMoreIndex
}

// SelectableIndices returns the visible task indices followed by ShowMoreIndex
// while another page exists (Rust selectable_indices). The row stays selectable
// during search and while a request is loading or failed.
func (v *View) SelectableIndices() []int {
	if v == nil {
		return nil
	}
	indices := v.VisibleIndices()
	if v.hasMore {
		indices = append(indices, ShowMoreIndex)
	}
	return indices
}

// NextBatch publishes up to one more page of retained tasks and reports how
// many rows appeared plus whether another page remains (Rust next_batch).
func (v *View) NextBatch() (int, bool) {
	if v == nil || !v.paged {
		return 0, false
	}
	before := len(v.VisibleIndices())
	v.historyLimit += AgentsOverviewPageSize
	after := len(v.VisibleIndices())
	v.showMoreLoading = false
	v.showMoreFailed = false
	v.refreshHistoryPaging()
	if !v.hasMore && v.Selected == ShowMoreIndex {
		v.fitSelection()
	}
	return after - before, v.hasMore
}

// ShowMoreAgentsOverview requests the next page while the "Show more" row is
// selected (Rust App::show_more_agents_overview / view activate). The row keeps
// rendering "Loading more…" until the host publishes the page or reports a
// failure, and a duplicate request is suppressed while loading.
func (v *View) ShowMoreAgentsOverview() (Action, bool) {
	if v == nil || !v.hasMore {
		return ActionNone, false
	}
	if v.showMoreLoading {
		return ActionNone, false
	}
	v.showMoreLoading = true
	v.showMoreFailed = false
	return ActionShowMore, true
}

// RefreshHistoryPaging recomputes whether another page exists after the host
// refreshed the retained rows (Rust refresh sets view has_more).
func (v *View) RefreshHistoryPaging() {
	if v == nil {
		return
	}
	v.refreshHistoryPaging()
}

func (v *View) refreshHistoryPaging() {
	if v == nil {
		return
	}
	if !v.paged {
		v.hasMore = false
		return
	}
	if v.historyLimit <= 0 {
		v.historyLimit = AgentsOverviewPageSize
	}
	v.hasMore = len(v.visibleIndicesUnbounded()) > v.historyLimit
	if !v.hasMore {
		v.showMoreLoading = false
		v.showMoreFailed = false
	}
	if v.Selected == ShowMoreIndex && !v.hasMore {
		v.fitSelection()
	}
}
