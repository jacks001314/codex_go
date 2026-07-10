package tui

import (
	"errors"
	"sort"
	"strings"
	"time"

	"codex_go/internal/appserver"
	"codex_go/internal/session"
)

type SessionSourceOptions struct {
	IncludeArchived       bool
	IncludeNonInteractive bool
	Limit                 int
	CWD                   string
	Search                string
	ModelProvider         string
}

func LoadSessionSummariesFromStore(store *session.Store, options SessionSourceOptions) ([]SessionSummary, error) {
	if store == nil {
		return nil, errors.New("session store is nil")
	}
	records, err := loadSessionRecordsByArchived(store, false, options)
	if err != nil {
		return nil, err
	}
	if options.IncludeArchived {
		archived, err := loadSessionRecordsByArchived(store, true, options)
		if err != nil {
			return nil, err
		}
		records = append(records, archived...)
	}
	records = filterSessionRecordsForPicker(records, options)
	sort.SliceStable(records, func(i int, j int) bool {
		left := sessionRecordPickerTime(&records[i])
		right := sessionRecordPickerTime(&records[j])
		if left.Equal(right) {
			return records[i].ID > records[j].ID
		}
		return left.After(right)
	})
	if limit := normalizedSessionSourceLimit(options.Limit); limit > 0 && len(records) > limit {
		records = records[:limit]
	}
	return SessionSummariesFromRecords(store, records), nil
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
	if cwd := strings.TrimSpace(options.CWD); cwd != "" {
		params.CWD = &appserver.ThreadListCwdFilter{Values: []string{cwd}}
	}
	if search := strings.TrimSpace(options.Search); search != "" {
		params.SearchTerm = &search
	}
	if provider := strings.TrimSpace(options.ModelProvider); provider != "" {
		params.ModelProviders = []string{provider}
	}
	if !options.IncludeNonInteractive {
		params.SourceKinds = []appserver.ThreadSourceKind{
			appserver.ThreadSourceKindCli,
			appserver.ThreadSourceKindVsCode,
		}
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

func loadSessionRecordsByArchived(store *session.Store, archived bool, options SessionSourceOptions) ([]session.Record, error) {
	listOptions := session.ListOptions{
		SortKey:        session.SortRecencyAt,
		SortDirection:  session.SortDesc,
		Archived:       archived,
		Search:         strings.TrimSpace(options.Search),
		IncludeHistory: false,
	}
	if cwd := strings.TrimSpace(options.CWD); cwd != "" {
		listOptions.CWDs = []string{cwd}
	}
	if provider := strings.TrimSpace(options.ModelProvider); provider != "" {
		listOptions.ModelProviders = []string{provider}
	}
	page, err := store.List(listOptions)
	if err != nil {
		return nil, err
	}
	return append([]session.Record(nil), page.Records...), nil
}

func filterSessionRecordsForPicker(records []session.Record, options SessionSourceOptions) []session.Record {
	if len(records) == 0 {
		return nil
	}
	if options.IncludeNonInteractive {
		return append([]session.Record(nil), records...)
	}
	filtered := make([]session.Record, 0, len(records))
	for i := range records {
		if sessionRecordIsInteractive(&records[i]) {
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
		ThreadID:  string(record.ID),
		Path:      path,
		Title:     strings.TrimSpace(record.Title),
		Preview:   strings.TrimSpace(record.Preview),
		CWD:       record.Metadata.CWD,
		Branch:    record.Metadata.Git["branch"],
		Provider:  firstNonEmpty(record.Metadata.ModelProvider, record.Metadata.Model),
		CreatedAt: record.CreatedAt,
		UpdatedAt: sessionRecordPickerTime(record),
		Archived:  record.Archived,
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
	return SessionSummary{
		ThreadID:  strings.TrimSpace(thread.ID),
		Path:      path,
		Title:     title,
		Preview:   strings.TrimSpace(thread.Preview),
		CWD:       thread.CWD,
		Branch:    branch,
		Provider:  thread.ModelProvider,
		CreatedAt: unixSecondsTime(thread.CreatedAt),
		UpdatedAt: appServerThreadRecency(thread),
		Archived:  archived,
	}
}

func sessionRecordIsInteractive(record *session.Record) bool {
	if record == nil {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(record.Metadata.Source))
	source = strings.ReplaceAll(source, "_", "")
	source = strings.ReplaceAll(source, "-", "")
	switch source {
	case "", "cli", "vscode":
		return true
	case "exec", "appserver", "subagent", "subagentreview", "subagentcompact", "subagentthreadspawn", "subagentother", "unknown":
		return false
	default:
		return true
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
