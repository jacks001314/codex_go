package tea

import (
	"strings"
	"unicode/utf8"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"codex_go/features"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// keymapPanelMaxResultRows mirrors keymap_setup/picker.rs max_visible_rows.
const keymapPanelMaxResultRows = 24

// keymapPanelState is Rust's ListSelectionView state for the /keymap page:
// the active tab, the selection inside its (optionally filtered) rows, and the
// open search query (is_searchable pickers keep the search field visible).
type keymapPanelState struct {
	tab         int
	selected    int
	scrollTop   int
	visibleRows int
	query       string
}

func (m *Model) keymapPageFilter() codextui.KeymapActionFilter {
	if m == nil {
		return codextui.KeymapActionFilter{}
	}
	return codextui.KeymapActionFilter{FastModeEnabled: features.Enabled(m.featureSettings, "fast_mode")}
}

func (m *Model) keymapPanelTabs() []codextui.KeymapPageTab {
	if m == nil {
		return nil
	}
	return codextui.KeymapPageTabs(codextui.KeymapPageRows(m.keymapPageFilter(), m.keymapConfig))
}

// keymapPanelRows returns the rows of the active tab after the search filter.
func (m *Model) keymapPanelRows() []codextui.KeymapPageRow {
	if m == nil || m.modal == nil || m.modal.keymapPanel == nil {
		return nil
	}
	tabs := m.keymapPanelTabs()
	panel := m.modal.keymapPanel
	if panel.tab < 0 || panel.tab >= len(tabs) {
		return nil
	}
	tab := tabs[panel.tab]
	query := strings.ToLower(strings.TrimSpace(panel.query))
	if query == "" {
		return tab.Rows
	}
	filtered := make([]codextui.KeymapPageRow, 0, len(tab.Rows))
	for _, row := range tab.Rows {
		if strings.Contains(strings.ToLower(row.SearchText()), query) {
			filtered = append(filtered, row)
		}
	}
	return filtered
}

// openKeymapPicker opens Rust's /keymap page. selectedContext/selectedAction
// re-select an edited row after an edit, exactly like
// build_keymap_picker_params_for_selected_action.
func (m *Model) openKeymapPicker(selectedContext string, selectedAction string) {
	if m == nil {
		return
	}
	state := &keymapPanelState{}
	if selectedAction != "" {
		for index, row := range codextui.KeymapPageRows(m.keymapPageFilter(), m.keymapConfig) {
			if row.Context == selectedContext && row.Action == selectedAction {
				state.selected = index
				break
			}
		}
	}
	m.replaceModal(&modalState{
		id:   chatwidget.KeymapPickerViewID,
		kind: ModalKindGeneric,
		// Rust builds this page with PickerSurface::Panel and
		// ViewPresentation::Inline, so it keeps the full terminal width.
		inlinePicker: true,
		title:        "Keymap",
		keymapPanel:  state,
	})
	m.notice = ""
}

func (m *Model) keymapPanelSwitchTab(delta int) {
	panel := m.modal.keymapPanel
	tabs := m.keymapPanelTabs()
	if len(tabs) == 0 {
		return
	}
	panel.tab = (panel.tab + delta + len(tabs)) % len(tabs)
	panel.query = ""
	panel.selected = 0
	panel.scrollTop = 0
}

func keymapPanelMoveSelection(selected, delta, total int) int {
	if total <= 0 {
		return 0
	}
	selected = clampModalInt(selected, 0, total-1)
	selected += delta
	if selected < 0 {
		selected = total - 1
	} else if selected >= total {
		selected = 0
	}
	return selected
}

func (m *Model) updateKeymapPanelKey(message bubbletea.KeyMsg) bubbletea.Cmd {
	if m == nil || m.modal == nil || m.modal.keymapPanel == nil {
		return nil
	}
	panel := m.modal.keymapPanel
	tabs := m.keymapPanelTabs()
	rows := m.keymapPanelRows()
	switch message.Type {
	case bubbletea.KeyEsc, bubbletea.KeyCtrlC:
		m.modal = nil
		m.refreshTranscript()
		return nil
	case bubbletea.KeyLeft:
		m.keymapPanelSwitchTab(-1)
		return nil
	case bubbletea.KeyRight:
		m.keymapPanelSwitchTab(1)
		return nil
	case bubbletea.KeyUp:
		panel.selected = keymapPanelMoveSelection(panel.selected, -1, len(rows))
		return nil
	case bubbletea.KeyDown:
		panel.selected = keymapPanelMoveSelection(panel.selected, 1, len(rows))
		return nil
	case bubbletea.KeyPgUp:
		panel.selected = max(panel.selected-max(panel.visibleRows, 1), 0)
		return nil
	case bubbletea.KeyPgDown:
		panel.selected = min(panel.selected+max(panel.visibleRows, 1), max(len(rows)-1, 0))
		return nil
	case bubbletea.KeyHome:
		panel.selected = 0
		return nil
	case bubbletea.KeyEnd:
		panel.selected = max(len(rows)-1, 0)
		return nil
	case bubbletea.KeyBackspace:
		if panel.query != "" {
			_, size := utf8.DecodeLastRuneInString(panel.query)
			panel.query = panel.query[:len(panel.query)-size]
			panel.selected = 0
			panel.scrollTop = 0
		}
		return nil
	case bubbletea.KeyCtrlU:
		panel.query = ""
		panel.selected = 0
		panel.scrollTop = 0
		return nil
	case bubbletea.KeyEnter:
		if len(tabs) > 0 && panel.tab == len(tabs)-1 {
			// Debug tab: Rust opens the live Keypress Inspector.
			return m.openKeymapInspector()
		}
		if panel.selected >= 0 && panel.selected < len(rows) {
			m.openKeymapActionMenu(rows[panel.selected].Context, rows[panel.selected].Action)
		}
		return nil
	case bubbletea.KeySpace:
		panel.query += " "
		panel.selected = 0
		panel.scrollTop = 0
		return nil
	case bubbletea.KeyRunes:
		if len(message.Runes) == 0 {
			return nil
		}
		panel.query += string(message.Runes)
		panel.selected = 0
		panel.scrollTop = 0
		return nil
	default:
		return nil
	}
}

// renderKeymapTabBar mirrors bottom_pane/selection_tabs.rs::render_filled_tab_bar:
// the window always keeps the active tab and reserves two cells for each hidden
// side arrow.
func renderKeymapTabBar(labels []string, active int, width int) string {
	if len(labels) == 0 || width <= 0 {
		return ""
	}
	active = clampModalInt(active, 0, len(labels)-1)
	widths := make([]int, len(labels))
	for index, label := range labels {
		widths[index] = codextui.DisplayWidth(label) + 2
	}
	occupied := func(start, end int) int {
		total := end - start - 1
		for index := start; index < end; index++ {
			total += widths[index]
		}
		if start > 0 {
			total += 2
		}
		if end < len(labels) {
			total += 2
		}
		return total
	}
	start, end := active, active+1
	for start > 0 && occupied(start-1, end) <= width {
		start--
	}
	for end < len(labels) && occupied(start, end+1) <= width {
		end++
	}
	showLeft := start > 0 && width >= 5
	showRight := end < len(labels) && width >= 7

	var builder strings.Builder
	x := 0
	if showLeft {
		builder.WriteString("\u2039 ")
		x = 2
	}
	right := width
	if showRight {
		right = width - 2
	}
	for index := start; index < end; index++ {
		cell := widths[index]
		if x+cell > right {
			cell = right - x
		}
		if cell <= 0 {
			break
		}
		text := " " + labels[index] + " "
		if codextui.DisplayWidth(text) > cell {
			text = codextui.TruncateWithEllipsis(text, cell)
		}
		builder.WriteString(text)
		x += cell
		if index < end-1 {
			builder.WriteString(" ")
			x++
		}
	}
	if showRight {
		if pad := width - 1 - codextui.DisplayWidth(builder.String()); pad > 0 {
			builder.WriteString(strings.Repeat(" ", pad))
		}
		builder.WriteString("\u203a")
	}
	return builder.String()
}

// keymapPanelDescriptionColumn mirrors compute_desc_col for the keymap picker:
// AutoAllRows over the full catalog, an explicit name_column_width of
// prefix+widest label, and the 70% description cap.
func keymapPanelDescriptionColumn(all []codextui.KeymapPageRow, tabRows []codextui.KeymapPageRow, placeholder *codextui.KeymapPageRow, width int) int {
	nameColumnWidth := 0
	for _, row := range all {
		nameColumnWidth = max(nameColumnWidth, keymapContextLabelWidth+3+codextui.DisplayWidth(row.Label))
	}
	maxNameWidth := 0
	for _, row := range tabRows {
		prefix := keymapContextLabelWidth + 3
		if row.Debug || row.Placeholder {
			prefix = 0
		}
		maxNameWidth = max(maxNameWidth, 2+prefix+codextui.DisplayWidth(row.Label))
	}
	if placeholder != nil {
		maxNameWidth = max(maxNameWidth, 2+codextui.DisplayWidth(placeholder.Label))
	}
	return keymapPanelClampDescCol(max(nameColumnWidth, maxNameWidth)+2, width)
}

// keymapPanelClampDescCol keeps the description column inside the pane and
// under Rust's 70% auto-cap (selection_popup_common compute_desc_col).
func keymapPanelClampDescCol(descCol, width int) int {
	maxDescCol := width - 1
	if autoCap := max(width*70/100, 1); maxDescCol > autoCap {
		maxDescCol = autoCap
	}
	if descCol > maxDescCol {
		descCol = maxDescCol
	}
	if descCol < 0 {
		descCol = 0
	}
	return descCol
}

const keymapContextLabelWidth = 12

// keymapPanelRowLine renders one SingleLine picker row: selection marker,
// context tag, custom/unbound indicator, label and the description column.
func keymapPanelRowLine(row codextui.KeymapPageRow, selected bool, descCol, width int, inactive func(string) string) string {
	var builder strings.Builder
	visible := 2
	if selected {
		builder.WriteString("\u203a ")
	} else {
		builder.WriteString("  ")
	}
	if !row.Debug && !row.Placeholder {
		indicator := " "
		switch {
		case row.Custom:
			indicator = "*"
		case row.Unbound():
			indicator = "-"
		}
		builder.WriteString(inactive(keymapPadLabel(row.ContextLabel, keymapContextLabelWidth) + " " + indicator + " "))
		visible += keymapContextLabelWidth + 3
	}
	builder.WriteString(row.Label)
	visible += codextui.DisplayWidth(row.Label)
	description := row.BindingSummary
	if row.Debug || row.Placeholder {
		description = row.Description
		if selected && row.SelectedDescription != "" {
			description = row.SelectedDescription
		}
	}
	if descCol > 0 && description != "" {
		if gap := descCol - visible; gap > 0 {
			builder.WriteString(strings.Repeat(" ", gap))
		}
		builder.WriteString(description)
	}
	line := codextui.TruncateWithEllipsis(builder.String(), width)
	if selected {
		line = codextui.RenderSelectedRowFilled(line, width)
	}
	return line
}

func keymapPadLabel(label string, width int) string {
	if pad := width - codextui.DisplayWidth(label); pad > 0 {
		return label + strings.Repeat(" ", pad)
	}
	return label
}

// keymapPanelChromeRows counts the rows renderKeymapPanel paints outside
// the scrollable result window.
const keymapPanelChromeRows = 11

func (m *Model) renderKeymapPanel() string {
	if m == nil || m.modal == nil || m.modal.keymapPanel == nil {
		return ""
	}
	panel := m.modal.keymapPanel
	tabs := m.keymapPanelTabs()
	if len(tabs) == 0 {
		return ""
	}
	panel.tab = clampModalInt(panel.tab, 0, len(tabs)-1)
	tab := tabs[panel.tab]
	rowWidth := max(m.width, 1)
	contentWidth := max(rowWidth-2, 1)
	rows := m.keymapPanelRows()
	query := strings.TrimSpace(panel.query)
	var placeholder *codextui.KeymapPageRow
	if empty, ok := codextui.KeymapEmptyRow(tab); ok && query == "" {
		rows = []codextui.KeymapPageRow{empty}
		placeholder = &rows[0]
	}
	if len(rows) == 0 {
		panel.selected = 0
	} else {
		panel.selected = clampModalInt(panel.selected, 0, len(rows)-1)
	}

	allRows := codextui.KeymapPageRows(m.keymapPageFilter(), m.keymapConfig)
	// Rust measures the visible (filtered) rows when a search is active, so the
	// description column shrinks to the matches instead of the whole tab.
	measuredRows := tab.Rows
	if query != "" {
		measuredRows = rows
	}
	descCol := keymapPanelDescriptionColumn(allRows, measuredRows, placeholder, contentWidth)

	inactive := func(text string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Render(text)
	}
	labels := make([]string, len(tabs))
	for index, item := range tabs {
		labels[index] = item.Label
	}
	// The panel's fixed chrome is eleven rows (leading blank, title,
	// description, summary, header gap, tab bar, gap, search row, gap,
	// scroll indicator, footer). Rust lets an inline picker take every row
	// the terminal offers, collapsing the transcript to zero rows, so the
	// result window must use the remaining height rather than holding a row
	// back for the transcript.
	resultRows := keymapPanelMaxResultRows
	if m.height > 0 {
		resultRows = min(resultRows, max(m.height-keymapPanelChromeRows, 1))
	}
	start, visible, above, below := modalOptionWindow(len(rows), panel.selected, panel.scrollTop, resultRows)
	panel.scrollTop = start
	panel.visibleRows = visible

	lines := []string{
		"",
		"  " + m.modal.title,
		"  " + inactive(codextui.TruncateWithEllipsis(tab.Description, contentWidth)),
		"  " + inactive(codextui.TruncateWithEllipsis(tab.Summary, contentWidth)),
		"",
		"  " + renderKeymapTabBar(labels, panel.tab, max(rowWidth-4, 1)),
		"",
	}
	if query == "" {
		lines = append(lines, "  "+inactive("Type to search shortcuts"))
	} else {
		lines = append(lines, "  "+codextui.TruncateWithEllipsis(panel.query, contentWidth))
	}
	lines = append(lines, "")
	if len(rows) == 0 {
		lines = append(lines, "no matches")
	} else {
		for index := start; index < start+visible && index < len(rows); index++ {
			lines = append(lines, keymapPanelRowLine(rows[index], index == panel.selected, descCol, rowWidth, inactive))
		}
	}
	for rendered := max(len(rows), 1); rendered < resultRows; rendered++ {
		lines = append(lines, "")
	}
	indicator := ""
	if below {
		indicator = "\u2193"
	} else if above {
		indicator = "\u2191"
	}
	footer := "left/right group \u00b7 enter edit shortcut \u00b7 * custom \u00b7 - unbound \u00b7 esc close"
	if tab.Debug {
		footer = "enter start inspector \u00b7 esc close"
	}
	lines = append(lines, indicator, "  "+codextui.TruncateWithEllipsis(footer, contentWidth))
	return strings.Join(lines, "\n")
}
