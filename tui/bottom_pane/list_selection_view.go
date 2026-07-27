package bottompane

import (
	"fmt"
	"strings"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/list_selection_view.rs.

const (
	SelectionToggleOnPrefix          = "[*] "
	SelectionToggleOffPrefix         = "[ ] "
	SelectionToggleUnavailablePrefix = "[-] "
	SelectionToggleBlockedPrefix     = "[!] "
)

type ListSelectionToggle struct {
	On          bool
	Placeholder string
}

type ListSelectionItem struct {
	ID                   string
	Label                string
	Description          string
	SelectedDescription  string
	SearchValue          string
	Disabled             bool
	DisabledReason       string
	DisabledGutterMarker string
	Current              bool
	Default              bool
	DismissOnSelect      bool
	Toggle               *ListSelectionToggle
}

type ListSelectionResult struct {
	Item        ListSelectionItem
	ActualIndex int
	Accepted    bool
	Cancelled   bool
}

type ListSelectionView struct {
	Title             string
	Subtitle          string
	FooterNote        string
	FooterHint        string
	Items             []ListSelectionItem
	Searchable        bool
	SearchPlaceholder string
	SearchQuery       string
	State             ScrollState
	FilteredIndices   []int
	Completion        *ListSelectionResult
	MaxRows           int
	ColumnWidth       ColumnWidthConfig
	RowDisplay        SelectionRowDisplay
	DescriptionLayout SelectionDescriptionLayout
	AllowCancel       bool
	lastSearchable    bool
}

func NewListSelectionView(title string, items []ListSelectionItem) *ListSelectionView {
	view := &ListSelectionView{
		Title:       title,
		Items:       append([]ListSelectionItem(nil), items...),
		MaxRows:     MaxPopupRows,
		RowDisplay:  SelectionRowDisplayWrapped,
		AllowCancel: true,
	}
	view.ApplyFilter()
	return view
}

func (v *ListSelectionView) ApplyFilter() {
	if v == nil {
		return
	}
	searchabilityChanged := v.Searchable != v.lastSearchable
	previousActual := v.SelectedActualIndex()
	if searchabilityChanged {
		previousActual = -1
	}
	v.FilteredIndices = v.FilteredIndices[:0]
	query := strings.ToLower(v.SearchQuery)
	if v.Searchable && query != "" {
		for idx, item := range v.Items {
			if item.SearchValue != "" && strings.Contains(strings.ToLower(item.SearchValue), query) {
				v.FilteredIndices = append(v.FilteredIndices, idx)
			}
		}
	} else {
		for idx := range v.Items {
			v.FilteredIndices = append(v.FilteredIndices, idx)
		}
	}
	if len(v.FilteredIndices) == 0 {
		v.State.Reset()
		v.lastSearchable = v.Searchable
		return
	}
	if previousActual >= 0 {
		if visible := v.visibleIndexForActual(previousActual); visible >= 0 && selectionItemEnabled(v.Items[previousActual]) {
			v.State.SelectedIdx = visible
			v.State.HasSelection = true
			v.State.EnsureVisible(len(v.FilteredIndices), v.maxRows())
			return
		}
	}
	if actual := v.initialActualIndex(); actual >= 0 {
		v.State.SelectedIdx = v.visibleIndexForActual(actual)
		v.State.HasSelection = true
	} else {
		v.State.Reset()
	}
	v.State.EnsureVisible(len(v.FilteredIndices), v.maxRows())
	v.lastSearchable = v.Searchable
}

func (v *ListSelectionView) FilteredItems() []ListSelectionItem {
	if v == nil {
		return nil
	}
	items := make([]ListSelectionItem, 0, len(v.FilteredIndices))
	for _, idx := range v.FilteredIndices {
		items = append(items, v.Items[idx])
	}
	return items
}

func (v *ListSelectionView) SelectedActualIndex() int {
	if v == nil || !v.State.HasSelection || v.State.SelectedIdx < 0 || v.State.SelectedIdx >= len(v.FilteredIndices) {
		return -1
	}
	return v.FilteredIndices[v.State.SelectedIdx]
}

func (v *ListSelectionView) SelectedItem() (ListSelectionItem, bool) {
	actual := v.SelectedActualIndex()
	if actual < 0 || actual >= len(v.Items) {
		return ListSelectionItem{}, false
	}
	return v.Items[actual], true
}

func (v *ListSelectionView) MoveUp() {
	if v == nil {
		return
	}
	v.moveSelection(-1)
}

func (v *ListSelectionView) MoveDown() {
	if v == nil {
		return
	}
	v.moveSelection(1)
}

func (v *ListSelectionView) PageUp() {
	if v == nil {
		return
	}
	for i := 0; i < v.maxRows(); i++ {
		v.MoveUp()
	}
}

func (v *ListSelectionView) PageDown() {
	if v == nil {
		return
	}
	for i := 0; i < v.maxRows(); i++ {
		v.MoveDown()
	}
}

func (v *ListSelectionView) JumpTop() {
	if v == nil {
		return
	}
	v.selectFirstEnabled()
}

func (v *ListSelectionView) JumpBottom() {
	if v == nil {
		return
	}
	for visible := len(v.FilteredIndices) - 1; visible >= 0; visible-- {
		if selectionItemEnabled(v.Items[v.FilteredIndices[visible]]) {
			v.State.SelectedIdx = visible
			v.State.HasSelection = true
			v.State.EnsureVisible(len(v.FilteredIndices), v.maxRows())
			return
		}
	}
	v.State.Reset()
}

func (v *ListSelectionView) ToggleSelected() bool {
	actual := v.SelectedActualIndex()
	if actual < 0 || actual >= len(v.Items) || !selectionItemEnabled(v.Items[actual]) || v.Items[actual].Toggle == nil {
		return false
	}
	v.Items[actual].Toggle.On = !v.Items[actual].Toggle.On
	return true
}

func (v *ListSelectionView) AcceptSelected() (ListSelectionResult, bool) {
	actual := v.SelectedActualIndex()
	if actual < 0 || actual >= len(v.Items) || !selectionItemEnabled(v.Items[actual]) {
		return ListSelectionResult{}, false
	}
	result := ListSelectionResult{Item: v.Items[actual], ActualIndex: actual, Accepted: true}
	v.Completion = &result
	return result, true
}

func (v *ListSelectionView) Cancel() (ListSelectionResult, bool) {
	if v == nil || !v.AllowCancel {
		return ListSelectionResult{}, false
	}
	result := ListSelectionResult{Cancelled: true}
	v.Completion = &result
	return result, true
}

func (v *ListSelectionView) HandleKey(key string) (ListSelectionResult, bool) {
	if v == nil {
		return ListSelectionResult{}, false
	}
	normalized := strings.ToLower(strings.TrimSpace(key))
	switch normalized {
	case "up", "ctrl+p", "shift+tab":
		v.MoveUp()
	case "down", "ctrl+n", "tab":
		v.MoveDown()
	case "pageup", "ctrl+u":
		v.PageUp()
	case "pagedown", "ctrl+d":
		v.PageDown()
	case "home", "ctrl+a":
		v.JumpTop()
	case "end", "ctrl+e":
		v.JumpBottom()
	case "enter":
		return v.AcceptSelected()
	case "space":
		v.ToggleSelected()
	case "esc", "ctrl+c":
		return v.Cancel()
	case "backspace":
		if v.Searchable && v.SearchQuery != "" {
			v.SearchQuery = dropLastRune(v.SearchQuery)
			v.ApplyFilter()
		}
	default:
		if v.Searchable && len([]rune(key)) == 1 {
			v.SearchQuery += key
			v.ApplyFilter()
		} else if len([]rune(key)) == 1 && key[0] >= '1' && key[0] <= '9' {
			visible := int(key[0] - '1')
			if visible >= 0 && visible < len(v.FilteredIndices) && selectionItemEnabled(v.Items[v.FilteredIndices[visible]]) {
				v.State.SelectedIdx = visible
				v.State.HasSelection = true
				v.State.EnsureVisible(len(v.FilteredIndices), v.maxRows())
			}
		}
	}
	return ListSelectionResult{}, false
}

func (v *ListSelectionView) Rows(width int) []string {
	if v == nil {
		return nil
	}
	rows := []string{}
	if strings.TrimSpace(v.Title) != "" {
		rows = append(rows, v.Title)
	}
	if strings.TrimSpace(v.Subtitle) != "" {
		rows = append(rows, v.Subtitle)
	}
	if v.Searchable {
		query := v.SearchQuery
		if query == "" {
			query = firstNonEmptyString(v.SearchPlaceholder, "Search")
		}
		rows = append(rows, "> "+query)
	}
	displayRows := v.DisplayRows()
	state := v.State
	rendered := RenderGenericRowsWithDescriptionLayout(displayRows, state, v.maxRows(), "no matches", width, v.ColumnWidth, v.DescriptionLayout)
	if v.RowDisplay == SelectionRowDisplaySingleLine {
		rendered = RenderGenericRowsSingleLine(displayRows, state, v.maxRows(), "no matches", width, v.ColumnWidth)
	}
	rows = append(rows, rendered...)
	if strings.TrimSpace(v.FooterNote) != "" {
		rows = append(rows, v.FooterNote)
	}
	if strings.TrimSpace(v.FooterHint) != "" {
		rows = append(rows, v.FooterHint)
	}
	return rows
}

func (v *ListSelectionView) DisplayRows() []GenericDisplayRow {
	if v == nil {
		return nil
	}
	rows := make([]GenericDisplayRow, 0, len(v.FilteredIndices))
	enabledNumber := 0
	enabledNumberWidth := len(fmt.Sprintf("%d", max(len(v.FilteredIndices), 1)))
	for visibleIdx, actualIdx := range v.FilteredIndices {
		item := v.Items[actualIdx]
		label := firstNonEmptyString(item.Label, item.ID)
		isDisabled := !selectionItemEnabled(item)
		prefix := ""
		if isDisabled {
			if item.DisabledGutterMarker != "" {
				markerWidth := lenColumnsSelection(item.DisabledGutterMarker)
				prefix = strings.Repeat(" ", max(enabledNumberWidth-markerWidth, 0)) + item.DisabledGutterMarker + "  "
			} else {
				prefix = strings.Repeat(" ", enabledNumberWidth+2)
			}
		} else {
			enabledNumber++
			prefix = fmt.Sprintf("%*d. ", enabledNumberWidth, enabledNumber)
		}
		if item.Toggle != nil {
			if item.Toggle.Placeholder != "" {
				prefix += item.Toggle.Placeholder
			} else if item.Toggle.On {
				prefix += SelectionToggleOnPrefix
			} else {
				prefix += SelectionToggleOffPrefix
			}
		}
		description := item.Description
		if v.State.HasSelection && v.State.SelectedIdx == visibleIdx && item.SelectedDescription != "" {
			description = item.SelectedDescription
		}
		tag := ""
		if item.Current {
			tag = "current"
		} else if item.Default {
			tag = "default"
		}
		var wrapIndent *int
		if description == "" {
			wrapIndent = intPtr(len(prefix))
		}
		rows = append(rows, GenericDisplayRow{
			Name:           label,
			NamePrefix:     prefix,
			Description:    description,
			CategoryTag:    tag,
			DisabledReason: item.DisabledReason,
			IsDisabled:     isDisabled,
			WrapIndent:     wrapIndent,
		})
	}
	return rows
}

func (v *ListSelectionView) DesiredHeight(width int) int {
	return len(v.Rows(width))
}

func (v *ListSelectionView) maxRows() int {
	if v == nil || v.MaxRows <= 0 {
		return MaxPopupRows
	}
	return v.MaxRows
}

func (v *ListSelectionView) moveSelection(direction int) {
	if len(v.FilteredIndices) == 0 {
		v.State.Reset()
		return
	}
	if !v.State.HasSelection {
		v.selectFirstEnabled()
		return
	}
	length := len(v.FilteredIndices)
	next := v.State.SelectedIdx
	for i := 0; i < length; i++ {
		next = (next + direction + length) % length
		if selectionItemEnabled(v.Items[v.FilteredIndices[next]]) {
			v.State.SelectedIdx = next
			v.State.HasSelection = true
			v.State.EnsureVisible(length, v.maxRows())
			return
		}
	}
	v.State.Reset()
}

func (v *ListSelectionView) selectFirstEnabled() {
	for visible, actual := range v.FilteredIndices {
		if selectionItemEnabled(v.Items[actual]) {
			v.State.SelectedIdx = visible
			v.State.HasSelection = true
			v.State.EnsureVisible(len(v.FilteredIndices), v.maxRows())
			return
		}
	}
	v.State.Reset()
}

func (v *ListSelectionView) initialActualIndex() int {
	if !v.Searchable {
		for idx, item := range v.Items {
			if item.Current && selectionItemEnabled(item) && v.visibleIndexForActual(idx) >= 0 {
				return idx
			}
		}
		for idx, item := range v.Items {
			if item.Default && selectionItemEnabled(item) && v.visibleIndexForActual(idx) >= 0 {
				return idx
			}
		}
	}
	for _, idx := range v.FilteredIndices {
		if selectionItemEnabled(v.Items[idx]) {
			return idx
		}
	}
	return -1
}

func (v *ListSelectionView) visibleIndexForActual(actual int) int {
	for visible, idx := range v.FilteredIndices {
		if idx == actual {
			return visible
		}
	}
	return -1
}

func selectionItemEnabled(item ListSelectionItem) bool {
	return !item.Disabled && item.DisabledReason == ""
}

func selectionItemSearchValue(item ListSelectionItem) string {
	return item.SearchValue
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func dropLastRune(value string) string {
	runes := []rune(value)
	if len(runes) == 0 {
		return ""
	}
	return string(runes[:len(runes)-1])
}

func intPtr(value int) *int {
	return &value
}
