package tui

import (
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/resume_picker.rs.

const (
	SessionPickerPageSize          = 25
	SessionPickerLoadNearThreshold = 5
	sessionPickerDateWidth         = 12
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
	Path     string
	ThreadID string
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
}

type SessionFilterMode int

const (
	SessionFilterCWD SessionFilterMode = iota
	SessionFilterAll
)

func SessionFilterModeFromShowAll(showAll bool, filterCWD string) SessionFilterMode {
	if showAll || strings.TrimSpace(filterCWD) == "" {
		return SessionFilterAll
	}
	return SessionFilterCWD
}

func (m SessionFilterMode) Toggle(filterCWD string) SessionFilterMode {
	switch m {
	case SessionFilterCWD:
		return SessionFilterAll
	case SessionFilterAll:
		if strings.TrimSpace(filterCWD) != "" {
			return SessionFilterCWD
		}
	}
	return SessionFilterAll
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
	ThreadID  string
	Path      string
	Title     string
	Preview   string
	CWD       string
	Branch    string
	Provider  string
	CreatedAt time.Time
	UpdatedAt time.Time
	Archived  bool
}

type SessionPickerState struct {
	Action         SessionPickerAction
	Items          []SessionSummary
	Selected       int
	Query          string
	FilterMode     SessionFilterMode
	FilterCWD      string
	ProviderFilter string
	SortKey        SessionSortKey
	Density        SessionListDensity
	ToolbarFocus   SessionPickerToolbarControl
	Expanded       map[string]bool
	Loading        bool
	Error          string
}

func NewSessionPickerState(action SessionPickerAction, items []SessionSummary, filterCWD string) *SessionPickerState {
	state := &SessionPickerState{
		Action:       action,
		Items:        append([]SessionSummary(nil), items...),
		FilterMode:   SessionFilterModeFromShowAll(false, filterCWD),
		FilterCWD:    filterCWD,
		SortKey:      SessionSortUpdatedAt,
		ToolbarFocus: SessionPickerToolbarFilter,
		Expanded:     map[string]bool{},
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
	filterCWD := cleanPathForCompare(s.FilterCWD)
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
		if s.FilterMode == SessionFilterCWD && filterCWD != "" && cleanPathForCompare(item.CWD) != filterCWD {
			continue
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
	s.Selected = (s.Selected + delta) % len(visible)
	if s.Selected < 0 {
		s.Selected += len(visible)
	}
}

func (s *SessionPickerState) MovePage(delta int) {
	if s == nil {
		return
	}
	visible := s.VisibleItems()
	if len(visible) == 0 {
		s.Selected = 0
		return
	}
	s.Selected += delta
	if s.Selected < 0 {
		s.Selected = 0
	}
	if s.Selected >= len(visible) {
		s.Selected = len(visible) - 1
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
	s.FilterMode = s.FilterMode.Toggle(s.FilterCWD)
	s.clampSelection()
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
	if s == nil {
		return
	}
	switch s.ToolbarFocus {
	case SessionPickerToolbarSort:
		s.ToggleSort()
	default:
		s.ToggleFilter()
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
	selection := SessionSelection{
		Target: SessionTarget{Path: item.Path, ThreadID: item.ThreadID},
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

func (s *SessionPickerState) NeedsNextPage() bool {
	if s == nil {
		return false
	}
	visible := s.VisibleItems()
	return len(visible) > 0 && len(visible)-1-s.Selected <= SessionPickerLoadNearThreshold
}

func (s *SessionPickerState) RenderRows(width int, now time.Time) []string {
	if s == nil {
		return []string{"No sessions yet"}
	}
	if strings.TrimSpace(s.Error) != "" {
		return WrapLine(s.Error, WrapOptions{Width: width, BreakWords: true})
	}
	visible := s.VisibleItems()
	if len(visible) == 0 {
		if s.Loading {
			return []string{"Loading sessions..."}
		}
		return []string{"No sessions yet"}
	}
	rows := make([]string, 0, len(visible)*2)
	for index, item := range visible {
		selected := index == s.Selected
		if s.Density == SessionDensityDense {
			rows = append(rows, renderDenseSessionRow(item, selected, width, now))
			continue
		}
		rows = append(rows, renderComfortableSessionRow(item, selected, width, now)...)
		if s.Expanded[item.ThreadID] {
			rows = append(rows, renderExpandedSessionRows(item, width)...)
		}
	}
	return rows
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
	return "Filter: " +
		toolbarValue(filterModeLabel(SessionFilterCWD), s.FilterMode == SessionFilterCWD, focused) +
		toolbarValue(filterModeLabel(SessionFilterAll), s.FilterMode == SessionFilterAll, focused)
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
	if mode == SessionFilterAll {
		return "All"
	}
	return "Cwd"
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
	visible := s.VisibleItems()
	position := 0
	if len(visible) > 0 {
		position = s.Selected + 1
	}
	percent := 100
	if len(visible) > 1 {
		percent = int(float64(s.Selected) / float64(len(visible)-1) * 100)
	}
	total := FormatInt(int64(len(visible)))
	progress := FormatInt(int64(position)) + " / " + total + " \u00b7 " + FormatInt(int64(percent)) + "%"
	separatorWidth := width
	if separatorWidth <= 0 {
		separatorWidth = 80
	}
	separator := strings.Repeat("\u2500", separatorWidth)
	if DisplayWidth(progress)+2 < separatorWidth {
		start := len([]rune(separator)) - DisplayWidth(progress) - 1
		if start > 0 {
			runes := []rune(separator)
			copy(runes[start:], []rune(" "+progress+" "))
			separator = string(runes)
		}
	}
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
	if width > 0 && width < 120 {
		return []string{
			separator,
			"enter " + s.Action.Label() + "   esc " + escCompact + "   ctrl+c " + ctrlC + "   tab focus   \u2190/\u2192 option",
			"ctrl+o " + densityCompact + "   ctrl+t preview   ctrl+e exp   \u2191/\u2193 browse",
		}
	}
	return []string{
		separator,
		"enter " + s.Action.Label() + "   esc " + escWide + "   ctrl+c " + ctrlC + "   tab focus sort/filter   \u2190/\u2192 change option",
		"ctrl+o " + densityWide + "   ctrl+t transcript   ctrl+e expand   \u2191/\u2193 browse",
	}
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

func renderDenseSessionRow(item SessionSummary, selected bool, width int, now time.Time) string {
	prefix := SelectionPrefix(selected)
	updated := item.UpdatedAt
	if updated.IsZero() {
		updated = item.CreatedAt
	}
	row := prefix + padDisplayRight(relativeTime(updated, now), sessionPickerDateWidth) + item.DisplayPreview()
	if width > 0 {
		row = TruncateWithEllipsis(row, width)
	}
	if selected {
		row = RenderSelectedRow(row)
	}
	return row
}

func renderComfortableSessionRow(item SessionSummary, selected bool, width int, now time.Time) []string {
	prefix := SelectionPrefix(selected)
	title := prefix + item.DisplayPreview()
	lines := AdaptiveWrapLine(title, WrapOptions{
		Width:            width,
		SubsequentIndent: "  ",
		BreakWords:       true,
	})
	if selected {
		for i := range lines {
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

func renderExpandedSessionRows(item SessionSummary, width int) []string {
	details := []string{
		"Thread: " + item.ThreadID,
		"Path: " + item.Path,
	}
	if item.CWD != "" {
		details = append(details, "Directory: "+item.CWD)
	}
	out := []string{}
	for _, detail := range details {
		if strings.TrimSpace(detail) == "" {
			continue
		}
		out = append(out, AdaptiveWrapLine("    "+detail, WrapOptions{
			Width:            width,
			SubsequentIndent: "    ",
			BreakWords:       true,
		})...)
	}
	return out
}

func cleanPathForCompare(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	cleaned, err := filepath.Abs(path)
	if err != nil {
		cleaned = filepath.Clean(path)
	}
	return strings.ToLower(filepath.Clean(cleaned))
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
