package bottompane

import (
	"sort"
	"strings"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/multi_select_picker.rs.

const (
	MultiSelectItemNameTruncateLen = 21
	MultiSelectSearchPlaceholder   = "Type to search"
	MultiSelectSearchPromptPrefix  = "> "
	MultiSelectSectionBreakRow     = "  ───────────────────────"
)

type MultiSelectItem struct {
	ID                string
	Name              string
	Description       string
	Selected          bool
	Enabled           bool
	Orderable         bool
	SectionBreakAfter bool
}

type MultiSelectPicker struct {
	Title           string
	Subtitle        string
	Items           []MultiSelectItem
	State           ScrollState
	Complete        bool
	Cancelled       bool
	SearchQuery     string
	FilteredIndices []int
	OrderingEnabled bool
	Preview         string
	ConfirmedIDs    []string
	MaxRows         int
}

func NewMultiSelectPicker(title string, subtitle string, items []MultiSelectItem) *MultiSelectPicker {
	picker := &MultiSelectPicker{
		Title:    title,
		Subtitle: subtitle,
		Items:    normalizeMultiSelectItems(items),
		MaxRows:  MaxPopupRows,
	}
	picker.ApplyFilter()
	picker.UpdatePreview()
	return picker
}

func ToggleMultiSelect(items []MultiSelectItem, id string) []MultiSelectItem {
	out := normalizeMultiSelectItems(items)
	for i := range out {
		if out[i].ID == id {
			setMultiSelectEnabled(&out[i], !multiSelectEnabled(out[i]))
		}
	}
	return out
}

func (p *MultiSelectPicker) ApplyFilter() {
	if p == nil {
		return
	}
	previousActual := p.SelectedActualIndex()
	filter := strings.TrimSpace(p.SearchQuery)
	if filter == "" {
		p.FilteredIndices = make([]int, len(p.Items))
		for i := range p.Items {
			p.FilteredIndices[i] = i
		}
	} else {
		matches := []multiSelectMatch{}
		for idx, item := range p.Items {
			score, ok := multiSelectMatchScore(filter, item)
			if ok {
				matches = append(matches, multiSelectMatch{Index: idx, Score: score, Name: multiSelectDisplayName(item)})
			}
		}
		sort.SliceStable(matches, func(i int, j int) bool {
			if matches[i].Score != matches[j].Score {
				return matches[i].Score < matches[j].Score
			}
			return matches[i].Name < matches[j].Name
		})
		p.FilteredIndices = p.FilteredIndices[:0]
		for _, match := range matches {
			p.FilteredIndices = append(p.FilteredIndices, match.Index)
		}
	}
	if len(p.FilteredIndices) == 0 {
		p.State.Reset()
		return
	}
	if previousActual >= 0 {
		for visible, actual := range p.FilteredIndices {
			if actual == previousActual {
				p.State.SelectedIdx = visible
				p.State.HasSelection = true
				p.State.EnsureVisible(len(p.FilteredIndices), p.maxRows())
				return
			}
		}
	}
	p.State.ClampSelection(len(p.FilteredIndices))
	p.State.EnsureVisible(len(p.FilteredIndices), p.maxRows())
}

func (p *MultiSelectPicker) SelectedActualIndex() int {
	if p == nil || !p.State.HasSelection || p.State.SelectedIdx < 0 || p.State.SelectedIdx >= len(p.FilteredIndices) {
		return -1
	}
	return p.FilteredIndices[p.State.SelectedIdx]
}

func (p *MultiSelectPicker) SelectedItem() (MultiSelectItem, bool) {
	actual := p.SelectedActualIndex()
	if actual < 0 || actual >= len(p.Items) {
		return MultiSelectItem{}, false
	}
	return p.Items[actual], true
}

func (p *MultiSelectPicker) MoveUp() {
	if p == nil {
		return
	}
	p.State.MoveUpWrap(len(p.FilteredIndices))
	p.State.EnsureVisible(len(p.FilteredIndices), p.maxRows())
}

func (p *MultiSelectPicker) MoveDown() {
	if p == nil {
		return
	}
	p.State.MoveDownWrap(len(p.FilteredIndices))
	p.State.EnsureVisible(len(p.FilteredIndices), p.maxRows())
}

func (p *MultiSelectPicker) PageUp() {
	if p == nil {
		return
	}
	p.State.PageUpClamped(len(p.FilteredIndices), p.maxRows())
}

func (p *MultiSelectPicker) PageDown() {
	if p == nil {
		return
	}
	p.State.PageDownClamped(len(p.FilteredIndices), p.maxRows())
}

func (p *MultiSelectPicker) JumpTop() {
	if p == nil {
		return
	}
	p.State.JumpTop(len(p.FilteredIndices), p.maxRows())
}

func (p *MultiSelectPicker) JumpBottom() {
	if p == nil {
		return
	}
	p.State.JumpBottom(len(p.FilteredIndices), p.maxRows())
}

func (p *MultiSelectPicker) ToggleSelected() bool {
	actual := p.SelectedActualIndex()
	if actual < 0 || actual >= len(p.Items) {
		return false
	}
	setMultiSelectEnabled(&p.Items[actual], !multiSelectEnabled(p.Items[actual]))
	p.UpdatePreview()
	return true
}

func (p *MultiSelectPicker) MoveSelectedUp() bool {
	return p.moveSelected(-1)
}

func (p *MultiSelectPicker) MoveSelectedDown() bool {
	return p.moveSelected(1)
}

func (p *MultiSelectPicker) Confirm() []string {
	if p == nil {
		return nil
	}
	p.Complete = true
	p.Cancelled = false
	p.ConfirmedIDs = p.SelectedIDs()
	return append([]string(nil), p.ConfirmedIDs...)
}

func (p *MultiSelectPicker) Cancel() {
	if p != nil {
		p.Complete = true
		p.Cancelled = true
	}
}

func (p *MultiSelectPicker) SelectedIDs() []string {
	if p == nil {
		return nil
	}
	var ids []string
	for _, item := range p.Items {
		if multiSelectEnabled(item) {
			ids = append(ids, item.ID)
		}
	}
	return ids
}

func (p *MultiSelectPicker) UpdatePreview() {
	if p == nil {
		return
	}
	ids := p.SelectedIDs()
	if len(ids) == 0 {
		p.Preview = ""
		return
	}
	p.Preview = "Selected: " + strings.Join(ids, ", ")
}

func (p *MultiSelectPicker) HandleKey(key string) {
	if p == nil {
		return
	}
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "up", "ctrl+p":
		p.MoveUp()
	case "down", "ctrl+n":
		p.MoveDown()
	case "pageup", "ctrl+u":
		p.PageUp()
	case "pagedown", "ctrl+d":
		p.PageDown()
	case "home", "ctrl+a":
		p.JumpTop()
	case "end", "ctrl+e":
		p.JumpBottom()
	case "left", "ctrl+h":
		p.MoveSelectedUp()
	case "right", "ctrl+l":
		p.MoveSelectedDown()
	case "space":
		p.ToggleSelected()
	case "enter":
		p.Confirm()
	case "esc", "ctrl+c":
		p.Cancel()
	case "backspace":
		if p.SearchQuery != "" {
			p.SearchQuery = dropLastRune(p.SearchQuery)
			p.ApplyFilter()
		}
	default:
		if len([]rune(key)) == 1 {
			p.SearchQuery += key
			p.ApplyFilter()
		}
	}
}

func (p *MultiSelectPicker) Rows(width int) []string {
	if p == nil {
		return nil
	}
	rows := []string{}
	if strings.TrimSpace(p.Title) != "" {
		rows = append(rows, p.Title)
	}
	if strings.TrimSpace(p.Subtitle) != "" {
		rows = append(rows, p.Subtitle)
	}
	rows = append(rows, MultiSelectSearchPlaceholder)
	if p.SearchQuery == "" {
		rows = append(rows, MultiSelectSearchPromptPrefix)
	} else {
		rows = append(rows, MultiSelectSearchPromptPrefix+p.SearchQuery)
	}
	displayRows, displayState := p.displayRows()
	rows = append(rows, RenderGenericRowsSingleLine(displayRows, displayState, p.maxRows(), "no matches", max(width-2, 1), ColumnWidthConfig{})...)
	if p.Preview != "" {
		rows = append(rows, p.Preview)
	}
	rows = append(rows, "Space to toggle; Enter to confirm; Esc to close")
	return rows
}

func (p *MultiSelectPicker) DesiredHeight(width int) int {
	return len(p.Rows(width))
}

func (p *MultiSelectPicker) displayRows() ([]GenericDisplayRow, ScrollState) {
	if p == nil {
		return nil, ScrollState{}
	}
	rows := []GenericDisplayRow{}
	visibleToRow := []int{}
	for visible, actual := range p.FilteredIndices {
		if actual < 0 || actual >= len(p.Items) {
			continue
		}
		item := p.Items[actual]
		visibleToRow = append(visibleToRow, len(rows))
		marker := " "
		if multiSelectEnabled(item) {
			marker = "x"
		}
		prefix := " "
		if p.State.HasSelection && p.State.SelectedIdx == visible {
			prefix = selectedRowMarker
		}
		name := prefix + " [" + marker + "] " + truncateMultiSelectText(multiSelectDisplayName(item), MultiSelectItemNameTruncateLen)
		rows = append(rows, GenericDisplayRow{
			Name:        name,
			Description: item.Description,
		})
		if item.SectionBreakAfter && visible+1 < len(p.FilteredIndices) {
			rows = append(rows, GenericDisplayRow{Name: MultiSelectSectionBreakRow, IsDisabled: true})
		}
	}
	state := p.State
	if state.HasSelection && state.SelectedIdx >= 0 && state.SelectedIdx < len(visibleToRow) {
		state.SelectedIdx = visibleToRow[state.SelectedIdx]
	} else {
		state.HasSelection = false
		state.SelectedIdx = 0
	}
	if state.ScrollTop >= 0 && state.ScrollTop < len(visibleToRow) {
		state.ScrollTop = visibleToRow[state.ScrollTop]
	}
	return rows, state
}

func (p *MultiSelectPicker) moveSelected(direction int) bool {
	if p == nil || !p.OrderingEnabled || p.SearchQuery != "" {
		return false
	}
	actual := p.SelectedActualIndex()
	if actual < 0 || actual >= len(p.Items) || !p.Items[actual].Orderable {
		return false
	}
	next := actual + direction
	if next < 0 || next >= len(p.Items) || !p.Items[next].Orderable {
		return false
	}
	p.Items[actual], p.Items[next] = p.Items[next], p.Items[actual]
	p.ApplyFilter()
	for visible, idx := range p.FilteredIndices {
		if idx == next {
			p.State.SelectedIdx = visible
			p.State.HasSelection = true
			break
		}
	}
	p.UpdatePreview()
	return true
}

func (p *MultiSelectPicker) maxRows() int {
	if p == nil || p.MaxRows <= 0 {
		return MaxPopupRows
	}
	return p.MaxRows
}

func multiSelectEnabled(item MultiSelectItem) bool {
	return item.Enabled || item.Selected
}

func setMultiSelectEnabled(item *MultiSelectItem, enabled bool) {
	item.Enabled = enabled
	item.Selected = enabled
}

func multiSelectDisplayName(item MultiSelectItem) string {
	return item.Name
}

func normalizeMultiSelectItems(items []MultiSelectItem) []MultiSelectItem {
	out := append([]MultiSelectItem(nil), items...)
	for i := range out {
		if !out[i].Orderable {
			// Keep explicit false, matching Rust Default only for empty builder-created items.
		}
		if out[i].Enabled {
			out[i].Selected = true
		} else if out[i].Selected {
			out[i].Enabled = true
		}
	}
	return out
}

type multiSelectMatch struct {
	Index int
	Score int
	Name  string
}

func multiSelectMatchScore(filter string, item MultiSelectItem) (int, bool) {
	name := multiSelectDisplayName(item)
	if strings.TrimSpace(filter) == "" {
		return 0, true
	}
	score, ok := fuzzySkillScore(name, filter)
	if ok {
		return score, true
	}
	return 0, false
}

func truncateMultiSelectText(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(value)
	if len(runes) <= maxRunes {
		return value
	}
	if maxRunes < 3 {
		return string(runes[:maxRunes])
	}
	return string(runes[:maxRunes-3]) + "..."
}
