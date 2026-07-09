package bottompane

import (
	"sort"
	"strings"
)

// Rust parity: codex-rs/tui/src/bottom_pane/skills_toggle_view.rs.

const (
	SkillsSearchPlaceholder = "Type to search skills"
	SkillsSearchPrompt      = "> "
	skillNameTruncateLen    = 21
)

type SkillsToggleItem struct {
	Name        string
	SkillName   string
	Description string
	Enabled     bool
	Path        string
}

type SkillsToggleEventKind string

const (
	SkillsToggleEventSetEnabled SkillsToggleEventKind = "set_skill_enabled"
	SkillsToggleEventClosed     SkillsToggleEventKind = "manage_skills_closed"
	SkillsToggleEventReload     SkillsToggleEventKind = "list_skills_reload"
)

type SkillsToggleEvent struct {
	Kind    SkillsToggleEventKind
	Path    string
	Enabled bool
}

type SkillsToggleView struct {
	Enabled  []string
	Disabled []string

	Items           []SkillsToggleItem
	State           ScrollState
	Complete        bool
	SearchQuery     string
	FilteredIndices []int
	Events          []SkillsToggleEvent
}

func NewSkillsToggleView(items []SkillsToggleItem) *SkillsToggleView {
	view := &SkillsToggleView{
		Items: append([]SkillsToggleItem(nil), items...),
		State: NewScrollState(),
	}
	view.ApplyFilter()
	view.syncEnabledDisabled()
	return view
}

func (v *SkillsToggleView) VisibleLen() int {
	if v == nil {
		return 0
	}
	return len(v.FilteredIndices)
}

func (v *SkillsToggleView) ApplyFilter() {
	if v == nil {
		return
	}
	previousActual := -1
	if v.State.HasSelection && v.State.SelectedIdx >= 0 && v.State.SelectedIdx < len(v.FilteredIndices) {
		previousActual = v.FilteredIndices[v.State.SelectedIdx]
	}
	query := strings.TrimSpace(v.SearchQuery)
	if query == "" {
		v.FilteredIndices = make([]int, len(v.Items))
		for idx := range v.Items {
			v.FilteredIndices[idx] = idx
		}
	} else {
		type match struct {
			idx   int
			score int
		}
		matches := []match{}
		for idx, item := range v.Items {
			if score, ok := MatchSkill(query, item.Name, item.SkillName); ok {
				matches = append(matches, match{idx: idx, score: score})
			}
		}
		sort.SliceStable(matches, func(i, j int) bool {
			if matches[i].score != matches[j].score {
				return matches[i].score < matches[j].score
			}
			return v.Items[matches[i].idx].Name < v.Items[matches[j].idx].Name
		})
		v.FilteredIndices = make([]int, 0, len(matches))
		for _, match := range matches {
			v.FilteredIndices = append(v.FilteredIndices, match.idx)
		}
	}
	selectedVisible := -1
	if previousActual >= 0 {
		for visibleIdx, actualIdx := range v.FilteredIndices {
			if actualIdx == previousActual {
				selectedVisible = visibleIdx
				break
			}
		}
	}
	if selectedVisible >= 0 {
		v.State.SelectedIdx = selectedVisible
		v.State.HasSelection = true
	} else {
		v.State.ClampSelection(len(v.FilteredIndices))
	}
	v.State.EnsureVisible(len(v.FilteredIndices), skillsMaxVisibleRows(len(v.FilteredIndices)))
}

func (v *SkillsToggleView) MoveUp() {
	if v == nil {
		return
	}
	length := v.VisibleLen()
	v.State.MoveUpWrap(length)
	v.State.EnsureVisible(length, skillsMaxVisibleRows(length))
}

func (v *SkillsToggleView) MoveDown() {
	if v == nil {
		return
	}
	length := v.VisibleLen()
	v.State.MoveDownWrap(length)
	v.State.EnsureVisible(length, skillsMaxVisibleRows(length))
}

func (v *SkillsToggleView) PageUp() {
	if v == nil {
		return
	}
	length := v.VisibleLen()
	v.State.PageUpClamped(length, skillsMaxVisibleRows(length))
}

func (v *SkillsToggleView) PageDown() {
	if v == nil {
		return
	}
	length := v.VisibleLen()
	v.State.PageDownClamped(length, skillsMaxVisibleRows(length))
}

func (v *SkillsToggleView) JumpTop() {
	if v == nil {
		return
	}
	length := v.VisibleLen()
	v.State.JumpTop(length, skillsMaxVisibleRows(length))
}

func (v *SkillsToggleView) JumpBottom() {
	if v == nil {
		return
	}
	length := v.VisibleLen()
	v.State.JumpBottom(length, skillsMaxVisibleRows(length))
}

func (v *SkillsToggleView) InsertSearchRune(r rune) {
	if v == nil {
		return
	}
	v.SearchQuery += string(r)
	v.ApplyFilter()
}

func (v *SkillsToggleView) BackspaceSearch() {
	if v == nil || v.SearchQuery == "" {
		return
	}
	runes := []rune(v.SearchQuery)
	v.SearchQuery = string(runes[:len(runes)-1])
	v.ApplyFilter()
}

func (v *SkillsToggleView) ToggleSelected() {
	if v == nil {
		return
	}
	actualIdx, ok := v.selectedActualIdx()
	if !ok {
		return
	}
	v.Items[actualIdx].Enabled = !v.Items[actualIdx].Enabled
	v.Events = append(v.Events, SkillsToggleEvent{
		Kind:    SkillsToggleEventSetEnabled,
		Path:    v.Items[actualIdx].Path,
		Enabled: v.Items[actualIdx].Enabled,
	})
	v.syncEnabledDisabled()
}

func (v *SkillsToggleView) Close() {
	if v == nil || v.Complete {
		return
	}
	v.Complete = true
	v.Events = append(v.Events,
		SkillsToggleEvent{Kind: SkillsToggleEventClosed},
		SkillsToggleEvent{Kind: SkillsToggleEventReload},
	)
}

func (v *SkillsToggleView) HandleKey(key string) {
	if v == nil {
		return
	}
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "up", "ctrl+p":
		v.MoveUp()
	case "down", "ctrl+n":
		v.MoveDown()
	case "pageup", "ctrl+u":
		v.PageUp()
	case "pagedown", "ctrl+d":
		v.PageDown()
	case "home", "ctrl+a":
		v.JumpTop()
	case "end", "ctrl+e":
		v.JumpBottom()
	case "backspace":
		v.BackspaceSearch()
	case "space", "enter":
		v.ToggleSelected()
	case "esc", "ctrl+c":
		v.Close()
	default:
		if len([]rune(key)) == 1 {
			v.InsertSearchRune([]rune(key)[0])
		}
	}
}

func (v *SkillsToggleView) Rows(width int) []string {
	if v == nil {
		return nil
	}
	rows := []string{
		"Enable/Disable Skills",
		"Turn skills on or off. Your changes are saved automatically.",
		SkillsSearchPlaceholder,
		SkillsSearchPrompt + v.SearchQuery,
	}
	itemRows := v.itemRows(width)
	rows = append(rows, itemRows...)
	rows = append(rows, "Press space or enter to toggle; esc to close")
	return rows
}

func (v *SkillsToggleView) DesiredHeight(width int) int {
	return len(v.Rows(width))
}

func (v *SkillsToggleView) selectedActualIdx() (int, bool) {
	if v == nil || !v.State.HasSelection || v.State.SelectedIdx < 0 || v.State.SelectedIdx >= len(v.FilteredIndices) {
		return 0, false
	}
	return v.FilteredIndices[v.State.SelectedIdx], true
}

func (v *SkillsToggleView) itemRows(width int) []string {
	if v == nil {
		return nil
	}
	length := len(v.FilteredIndices)
	v.State.ClampSelection(length)
	v.State.EnsureVisible(length, skillsMaxVisibleRows(length))
	return RenderGenericRowsSingleLine(v.displayRows(), v.State, skillsMaxVisibleRows(length), "no matches", max(width-2, 1), ColumnWidthConfig{})
}

func (v *SkillsToggleView) syncEnabledDisabled() {
	if v == nil {
		return
	}
	v.Enabled = nil
	v.Disabled = nil
	for _, item := range v.Items {
		if item.Enabled {
			v.Enabled = append(v.Enabled, item.Name)
		} else {
			v.Disabled = append(v.Disabled, item.Name)
		}
	}
}

func skillsMaxVisibleRows(length int) int {
	if length <= 0 {
		return 1
	}
	return min(MaxPopupRows, length)
}

func TruncateSkillName(name string) string {
	return truncateSkillDescription(name, skillNameTruncateLen)
}

func MatchSkill(filter string, displayName string, skillName string) (int, bool) {
	if score, ok := fuzzySkillScore(displayName, filter); ok {
		return score, true
	}
	if displayName != skillName {
		return fuzzySkillScore(skillName, filter)
	}
	return 0, false
}

func fuzzySkillScore(value string, filter string) (int, bool) {
	valueLower := strings.ToLower(value)
	filterLower := strings.ToLower(strings.TrimSpace(filter))
	if filterLower == "" {
		return 0, true
	}
	if strings.HasPrefix(valueLower, filterLower) {
		return 0, true
	}
	if idx := strings.Index(valueLower, filterLower); idx > 0 {
		return len(valueLower) + idx, true
	}
	score := 0
	cursor := 0
	for idx, r := range valueLower {
		if cursor < len(filterLower) && byte(r) == filterLower[cursor] {
			score += idx
			cursor++
		}
	}
	if cursor == len(filterLower) {
		return score + len(valueLower), true
	}
	return 0, false
}

func truncateSkillDescription(value string, width int) string {
	if width <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= width {
		return value
	}
	if width <= 1 {
		return string(runes[:width])
	}
	return string(runes[:width-1]) + "..."
}

func (v *SkillsToggleView) displayRows() []GenericDisplayRow {
	if v == nil {
		return nil
	}
	rows := make([]GenericDisplayRow, 0, len(v.FilteredIndices))
	for visibleIdx, actualIdx := range v.FilteredIndices {
		if actualIdx < 0 || actualIdx >= len(v.Items) {
			continue
		}
		item := v.Items[actualIdx]
		prefix := " "
		if v.State.HasSelection && v.State.SelectedIdx == visibleIdx {
			prefix = selectedRowMarker
		}
		marker := " "
		if item.Enabled {
			marker = "x"
		}
		rows = append(rows, GenericDisplayRow{
			Name:        prefix + " [" + marker + "] " + TruncateSkillName(item.Name),
			Description: item.Description,
		})
	}
	return rows
}
