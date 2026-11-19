package tui

import (
	"errors"
	"sort"
	"strings"
	"time"

	"codex_go/appserver"
	"codex_go/gitutil"
	"codex_go/rollout"
	"codex_go/session"
)

type SessionSourceOptions struct {
	IncludeArchived       bool
	IncludeNonInteractive bool
	Limit                 int
	CWD                   string
	// CWDs overrides CWD with an explicit directory scope (the picker's
	// ThreadListCwdFilter). nil keeps the single-directory default.
	CWDs []string
	// MatchAllCWDs lists every directory regardless of CWD/CWDs, mirroring the
	// picker's All filter (Rust ThreadListCwdFilter absent).
	MatchAllCWDs  bool
	Search        string
	ModelProvider string
	// Archived selects a single archive status for the listing (Rust
	// ThreadListParams.archived). nil keeps the merged listing that returns
	// both statuses in one page.
	Archived *bool
	// Cursor continues a listing whose earlier page already arrived (Rust
	// ThreadListParams.cursor).
	Cursor string
	// WorktreesEnabled marks local filesystem scope (Rust #54175 retired the
	// `worktrees` feature flag). The listing filter stays the requested
	// directory alone: Rust sends ThreadListCwdFilter::One for the picker's Cwd
	// filter and only expands linked checkouts when the Repo filter is active
	// (resume_picker.rs PageCwdFilter::for_request).
	WorktreesEnabled bool
}

// SessionPickerWorktreeCWDs returns the picker's directory filter: the
// requested directory, plus its linked worktrees' corresponding directories for
// a local session. Rust #54175 retired the `worktrees` feature flag, so the
// expandLinkedWorktrees argument now reflects the session's filesystem scope
// (false for remote sessions) rather than a feature toggle. It falls back to the
// requested directory alone when the checkout identity cannot be validated.
func SessionPickerWorktreeCWDs(cwd string, expandLinkedWorktrees bool) []string {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return nil
	}
	if !expandLinkedWorktrees {
		return []string{cwd}
	}
	cwds, ok := gitutil.LinkedWorktreeCWDs(cwd)
	if !ok || len(cwds) == 0 {
		return []string{cwd}
	}
	return cwds
}

// SessionSourceCWDs resolves a listing's directory scope. The picker's All
// filter wins over any other scope; otherwise an explicit CWDs set is used,
// falling back to the single requested directory.
func SessionSourceCWDs(options SessionSourceOptions) []string {
	if options.MatchAllCWDs {
		return nil
	}
	if len(options.CWDs) > 0 {
		return append([]string(nil), options.CWDs...)
	}
	if cwd := strings.TrimSpace(options.CWD); cwd != "" {
		return []string{cwd}
	}
	return nil
}

func LoadSessionSummariesFromStore(store *session.Store, options SessionSourceOptions) ([]SessionSummary, error) {
	summaries, _, err := LoadSessionSummaryPage(store, options)
	return summaries, err
}

// LoadSessionSummaryPage lists one page from the store's default source (Rust
// PageLoadMode::StoreDefault) and returns the cursor that continues it.
func LoadSessionSummaryPage(store *session.Store, options SessionSourceOptions) ([]SessionSummary, string, error) {
	if store == nil {
		return nil, "", errors.New("session store is nil")
	}
	groups := []bool{false}
	if options.Archived != nil {
		groups = []bool{*options.Archived}
	} else if options.IncludeArchived {
		groups = []bool{false, true}
	}
	var items []SessionSummary
	nextCursor := ""
	for _, archived := range groups {
		pageItems, next, err := loadSessionRecordPageByArchived(store, archived, options)
		if err != nil {
			return nil, "", err
		}
		items = append(items, pageItems...)
		if len(groups) == 1 {
			nextCursor = next
		}
	}
	return MergeSessionSummaryGroups(items, options.Limit), nextCursor, nil
}

// SessionSummaryPageFromRecords turns one archive group of already-projected
// records into a picker page. Eligibility filtering runs before the cursor
// page, so rows the picker never shows cannot consume a page slot (Rust
// thread/list applies source_kinds in the query itself).
func SessionSummaryPageFromRecords(store *session.Store, records []session.Record, options SessionSourceOptions, archived bool) ([]SessionSummary, string, error) {
	eligible := filterSessionRecordsForPicker(records, options)
	page, err := session.ListRecords(eligible, sessionPageListOptions(options, archived))
	if err != nil {
		return nil, "", err
	}
	return SessionSummariesFromRecords(store, page.Records), page.NextCursor, nil
}

// MergeSessionSummaryGroups orders rows from several archive groups by the
// picker's recency key and applies the display limit.
func MergeSessionSummaryGroups(items []SessionSummary, limit int) []SessionSummary {
	sort.SliceStable(items, func(i int, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ThreadID > items[j].ThreadID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	if limit = normalizedSessionSourceLimit(limit); limit > 0 && len(items) > limit {
		items = items[:limit]
	}
	return items
}

func SessionSummariesFromRecords(store *session.Store, records []session.Record) []SessionSummary {
	summaries := make([]SessionSummary, 0, len(records))
	for i := range records {
		summaries = append(summaries, sessionSummaryFromRecord(store, &records[i]))
	}
	return summaries
}

func AppServerThreadListParamsForSessionPicker(options SessionSourceOptions) appserver.ThreadListParams {
	limit := normalizedSessionSourceLimit(options.Limit)
	archived := options.IncludeArchived
	params := appserver.ThreadListParams{
		Limit:         &limit,
		SortKey:       appserver.SortRecencyAt,
		SortDirection: appserver.SortDesc,
		Archived:      &archived,
	}
	if cwds := SessionSourceCWDs(options); len(cwds) > 0 {
		params.CWD = &appserver.ThreadListCwdFilter{Values: cwds}
	}
	if search := strings.TrimSpace(options.Search); search != "" {
		params.SearchTerm = &search
	}
	if provider := strings.TrimSpace(options.ModelProvider); provider != "" {
		params.ModelProviders = []string{provider}
	}
	params.SourceKinds = []appserver.ThreadSourceKind{
		appserver.ThreadSourceKindCli,
		appserver.ThreadSourceKindVsCode,
	}
	if options.IncludeNonInteractive {
		params.SourceKinds = append(params.SourceKinds,
			appserver.ThreadSourceKindExec,
			appserver.ThreadSourceKindAppServer,
		)
	}
	return params
}

func SessionSummariesFromAppServerThreads(threads []appserver.Thread, archived bool) []SessionSummary {
	summaries := make([]SessionSummary, 0, len(threads))
	for i := range threads {
		summaries = append(summaries, sessionSummaryFromAppServerThread(&threads[i], archived))
	}
	return summaries
}

// SessionSummariesFromAppServerThreadsWithReadStates attaches the thread/list
// read receipts so the resume picker can mark unread sessions (Rust #58264).
func SessionSummariesFromAppServerThreadsWithReadStates(threads []appserver.Thread, archived bool, readStates map[string]appserver.ThreadReadState) []SessionSummary {
	summaries := SessionSummariesFromAppServerThreads(threads, archived)
	for i := range summaries {
		state, ok := readStates[summaries[i].ThreadID]
		if !ok || state.FirstUnread == nil {
			continue
		}
		summaries[i].UnreadRevision = strings.TrimSpace(state.Revision)
	}
	return summaries
}

func loadSessionRecordPageByArchived(store *session.Store, archived bool, options SessionSourceOptions) ([]SessionSummary, string, error) {
	listOptions := session.ListOptions{
		SortKey:        session.SortRecencyAt,
		SortDirection:  session.SortDesc,
		Archived:       archived,
		Search:         strings.TrimSpace(options.Search),
		IncludeHistory: false,
	}
	if cwds := SessionSourceCWDs(options); len(cwds) > 0 {
		listOptions.CWDs = cwds
	}
	if provider := strings.TrimSpace(options.ModelProvider); provider != "" {
		listOptions.ModelProviders = []string{provider}
	}
	page, err := store.List(listOptions)
	if err != nil {
		return nil, "", err
	}
	return SessionSummaryPageFromRecords(store, page.Records, options, archived)
}

func sessionPageListOptions(options SessionSourceOptions, archived bool) session.ListOptions {
	listOptions := session.ListOptions{
		PageSize:       options.Limit,
		Cursor:         strings.TrimSpace(options.Cursor),
		SortKey:        session.SortRecencyAt,
		SortDirection:  session.SortDesc,
		Archived:       archived,
		Search:         strings.TrimSpace(options.Search),
		IncludeHistory: false,
	}
	if cwds := SessionSourceCWDs(options); len(cwds) > 0 {
		listOptions.CWDSet = true
		listOptions.CWDs = cwds
	}
	if provider := strings.TrimSpace(options.ModelProvider); provider != "" {
		listOptions.ModelProviders = []string{provider}
	}
	return listOptions
}

func filterSessionRecordsForPicker(records []session.Record, options SessionSourceOptions) []session.Record {
	if len(records) == 0 {
		return nil
	}
	filtered := make([]session.Record, 0, len(records))
	for i := range records {
		if ephemeral, _ := records[i].Metadata.Extra["ephemeral"].(bool); ephemeral {
			continue
		}
		if sessionRecordIsEmptyForPicker(&records[i]) {
			continue
		}
		if sessionRecordIsResumable(&records[i], options.IncludeNonInteractive) {
			filtered = append(filtered, records[i])
		}
	}
	return filtered
}

func sessionSummaryFromRecord(store *session.Store, record *session.Record) SessionSummary {
	if record == nil {
		return SessionSummary{}
	}
	path := ""
	if store != nil {
		if p, err := store.Path(record.ID); err == nil {
			path = p
		}
	}
	return SessionSummary{
		ThreadID: string(record.ID),
		Path:     path,
		Title:    strings.TrimSpace(record.Title),
		Preview:  strings.TrimSpace(record.Preview),
		CWD:      record.Metadata.CWD,
		Branch:   record.Metadata.Git["branch"],
		Provider: strings.TrimSpace(record.Metadata.ModelProvider),
		Model:    strings.TrimSpace(record.Metadata.Model),
		// Rust #61183: the thread's own persisted fork parent.
		ForkedFromID: strings.TrimSpace(string(record.ForkedFromID)),
		// Rust session.daybreak_enabled (#49861): the stored thread preference.
		DaybreakEnabled: record.Metadata.DaybreakEnabled != nil && *record.Metadata.DaybreakEnabled,
		CreatedAt:       record.CreatedAt,
		UpdatedAt:       sessionRecordPickerTime(record),
		Archived:        record.Archived,
	}
}

func sessionSummaryFromAppServerThread(thread *appserver.Thread, archived bool) SessionSummary {
	if thread == nil {
		return SessionSummary{}
	}
	path := ""
	if thread.Path != nil {
		path = strings.TrimSpace(*thread.Path)
	}
	branch := ""
	if thread.GitInfo != nil && thread.GitInfo.Branch != nil {
		branch = strings.TrimSpace(*thread.GitInfo.Branch)
	}
	title := ""
	if thread.Name != nil && strings.TrimSpace(*thread.Name) != "" {
		title = strings.TrimSpace(*thread.Name)
	}
	model := ""
	if thread.Model != nil {
		model = strings.TrimSpace(*thread.Model)
	}
	forkedFromID := ""
	if thread.ForkedFromID != nil {
		forkedFromID = strings.TrimSpace(*thread.ForkedFromID)
	}
	return SessionSummary{
		ThreadID: strings.TrimSpace(thread.ID),
		Path:     path,
		Title:    title,
		Preview:  strings.TrimSpace(thread.Preview),
		CWD:      thread.CWD,
		Branch:   branch,
		Provider: strings.TrimSpace(thread.ModelProvider),
		Model:    model,
		// Rust #61183: the thread's own fork parent, not the primary session's.
		ForkedFromID: forkedFromID,
		// Rust session.daybreak_enabled (#49861): the app-server thread's
		// persisted Daybreak preference.
		DaybreakEnabled: thread.DaybreakEnabled != nil && *thread.DaybreakEnabled,
		CreatedAt:       unixSecondsTime(thread.CreatedAt),
		UpdatedAt:       appServerThreadRecency(thread),
		Archived:        archived,
	}
}

func sessionRecordIsInteractive(record *session.Record) bool {
	return sessionRecordIsResumable(record, false)
}

// sessionRecordIsEmptyForPicker reports whether a thread record has no
// conversation content and no persisted rollout: it was started (for example
// by opening the interactive TUI or an editor panel) but never received a
// message. Rust's resume picker lists threads by scanning rollout files on
// disk, so never-used threads never appear there; the Go JSON store creates a
// record eagerly at thread/start, so we skip them explicitly for parity.
func sessionRecordIsEmptyForPicker(record *session.Record) bool {
	if record == nil {
		return true
	}
	if record.ItemCount > 0 {
		return false
	}
	if strings.TrimSpace(record.Title) != "" || strings.TrimSpace(record.Preview) != "" {
		return false
	}
	rolloutPath, _ := record.Metadata.Extra["rollout_path"].(string)
	_, exists := rollout.ExistingRolloutPath(rolloutPath)
	return !exists
}

func sessionRecordIsResumable(record *session.Record, includeNonInteractive bool) bool {
	if record == nil {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(record.Metadata.Source))
	source = strings.NewReplacer("_", "", "-", "", "/", "", ":", "", " ", "").Replace(source)
	switch source {
	case "", "cli", "vscode":
		return true
	case "exec", "appserver":
		return includeNonInteractive
	default:
		return false
	}
}

func sessionRecordPickerTime(record *session.Record) time.Time {
	if record == nil {
		return time.Time{}
	}
	if !record.RecencyAt.IsZero() {
		return record.RecencyAt
	}
	if !record.UpdatedAt.IsZero() {
		return record.UpdatedAt
	}
	return record.CreatedAt
}

func appServerThreadRecency(thread *appserver.Thread) time.Time {
	if thread == nil {
		return time.Time{}
	}
	if thread.RecencyAt != nil && *thread.RecencyAt > 0 {
		return unixSecondsTime(*thread.RecencyAt)
	}
	if thread.UpdatedAt > 0 {
		return unixSecondsTime(thread.UpdatedAt)
	}
	return unixSecondsTime(thread.CreatedAt)
}

func unixSecondsTime(value int64) time.Time {
	if value <= 0 {
		return time.Time{}
	}
	return time.Unix(value, 0).UTC()
}

func normalizedSessionSourceLimit(limit int) int {
	if limit < 0 {
		return 0
	}
	if limit == 0 {
		return SessionPickerPageSize
	}
	return limit
}
