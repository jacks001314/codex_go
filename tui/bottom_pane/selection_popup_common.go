package bottompane

import (
	"strings"

	"codex_go/tui"
)

// Rust parity subset: codex-rs/tui/src/bottom_pane/selection_popup_common.rs.

const (
	menuSurfaceInsetV              = 1
	menuSurfaceInsetH              = 2
	fixedLeftColumnNumerator       = 3
	fixedLeftColumnDenominator     = 10
	selectionPopupAutoDescColRatio = 7
)

type SelectionPopupCommon struct {
	Title       string
	Subtitle    string
	FooterNote  string
	FooterHint  string
	HeaderLines []string
}

type GenericDisplayRow struct {
	Name            string
	NamePrefix      string
	DisplayShortcut string
	MatchIndices    []int
	Description     string
	CategoryTag     string
	DisabledReason  string
	IsDisabled      bool
	WrapIndent      *int
}

type ColumnWidthMode int

const (
	ColumnWidthAutoVisible ColumnWidthMode = iota
	ColumnWidthAutoAllRows
	ColumnWidthFixed
)

type ColumnWidthConfig struct {
	Mode            ColumnWidthMode
	NameColumnWidth *int
}

func NewColumnWidthConfig(mode ColumnWidthMode, nameColumnWidth *int) ColumnWidthConfig {
	return ColumnWidthConfig{Mode: mode, NameColumnWidth: nameColumnWidth}
}

type SelectionRowDisplay int

const (
	SelectionRowDisplayWrapped SelectionRowDisplay = iota
	SelectionRowDisplaySingleLine
)

type SelectionDescriptionLayoutMode int

const (
	SelectionDescriptionColumns SelectionDescriptionLayoutMode = iota
	SelectionDescriptionStackBelowWhenNarrow
)

type SelectionDescriptionLayout struct {
	Mode                SelectionDescriptionLayoutMode
	MinDescriptionWidth int
}

func NewStackBelowWhenNarrowDescriptionLayout(minDescriptionWidth int) SelectionDescriptionLayout {
	return SelectionDescriptionLayout{
		Mode:                SelectionDescriptionStackBelowWhenNarrow,
		MinDescriptionWidth: max(minDescriptionWidth, 0),
	}
}

type MenuSurfaceRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

func MenuSurfaceInset(area MenuSurfaceRect) MenuSurfaceRect {
	width := area.Width - menuSurfaceInsetH*2
	if width < 0 {
		width = 0
	}
	height := area.Height - menuSurfaceInsetV*2
	if height < 0 {
		height = 0
	}
	return MenuSurfaceRect{
		X:      area.X + menuSurfaceInsetH,
		Y:      area.Y + menuSurfaceInsetV,
		Width:  width,
		Height: height,
	}
}

func MenuSurfacePaddingHeight() int {
	return menuSurfaceInsetV * 2
}

func RenderGenericRows(rows []GenericDisplayRow, state ScrollState, maxResults int, emptyMessage string, width int, config ColumnWidthConfig) []string {
	return renderGenericRows(rows, state, maxResults, emptyMessage, width, config, SelectionRowDisplayWrapped, SelectionDescriptionLayout{})
}

func RenderGenericRowsWithDescriptionLayout(rows []GenericDisplayRow, state ScrollState, maxResults int, emptyMessage string, width int, config ColumnWidthConfig, descriptionLayout SelectionDescriptionLayout) []string {
	return renderGenericRows(rows, state, maxResults, emptyMessage, width, config, SelectionRowDisplayWrapped, descriptionLayout)
}

func RenderGenericRowsSingleLine(rows []GenericDisplayRow, state ScrollState, maxResults int, emptyMessage string, width int, config ColumnWidthConfig) []string {
	return renderGenericRows(rows, state, maxResults, emptyMessage, width, config, SelectionRowDisplaySingleLine, SelectionDescriptionLayout{})
}

func MeasureGenericRowsHeight(rows []GenericDisplayRow, state ScrollState, maxResults int, width int, config ColumnWidthConfig) int {
	return measureGenericRowsHeight(rows, state, maxResults, width, config)
}

func BuildGenericDisplayLine(row GenericDisplayRow, descCol int) string {
	return buildGenericDisplayLine(row, descCol, SelectionDescriptionLayout{})
}

func buildGenericDisplayLine(row GenericDisplayRow, descCol int, descriptionLayout SelectionDescriptionLayout) string {
	description := combinedSelectionDescription(row, descriptionLayout)
	nameLimit := -1
	if description != "" {
		nameLimit = max(descCol-lenColumnsSelection(row.NamePrefix)-2, 0)
	}
	nameText := truncateSelectionName(row.Name, nameLimit)
	name := row.NamePrefix + nameText
	if row.DisabledReason != "" {
		name += " (disabled)"
	}
	nameWidth := lenColumnsSelection(name)
	line := name
	if row.DisplayShortcut != "" {
		line += " (" + row.DisplayShortcut + ")"
	}
	if description != "" {
		gap := max(descCol-nameWidth, 0)
		if gap > 0 {
			line += strings.Repeat(" ", gap)
		}
		line += description
	}
	if row.CategoryTag != "" {
		line += "  " + row.CategoryTag
	}
	return line
}

func renderGenericRows(rows []GenericDisplayRow, state ScrollState, maxResults int, emptyMessage string, width int, config ColumnWidthConfig, display SelectionRowDisplay, descriptionLayout SelectionDescriptionLayout) []string {
	if width <= 0 {
		width = 1
	}
	if len(rows) == 0 {
		if emptyMessage == "" {
			return nil
		}
		return []string{emptyMessage}
	}
	if maxResults <= 0 || maxResults > len(rows) {
		maxResults = len(rows)
	}
	start := computeItemWindowStart(len(rows), state, maxResults)
	visible := rows[start:min(start+maxResults, len(rows))]
	descCol := computeGenericDescCol(rows, start, len(visible), width, config)
	stackDescriptions := descriptionLayout.shouldStack(width, descCol)
	out := []string{}
	for offset, row := range visible {
		actualIdx := start + offset
		if display == SelectionRowDisplayWrapped {
			lines := wrapSelectionRowLinesWithDescriptionLayout(row, descCol, width, descriptionLayout, stackDescriptions)
			for lineIdx, line := range lines {
				if lineIdx > 0 && row.IsDisabled {
					line = strings.TrimRight(line, " ")
				}
				if state.HasSelection && state.SelectedIdx == actualIdx && !row.IsDisabled {
					line = tui.RenderSelectedRow(line)
				}
				out = append(out, line)
			}
			continue
		}
		line := buildGenericDisplayLine(row, descCol, descriptionLayout)
		if tui.DisplayWidth(line) > width {
			line = tui.TruncateWithEllipsis(line, width)
		}
		if state.HasSelection && state.SelectedIdx == actualIdx && !row.IsDisabled {
			line = tui.RenderSelectedRow(line)
		}
		out = append(out, line)
	}
	return out
}

func wrapGenericDisplayLine(row GenericDisplayRow, line string, descCol int, width int) []string {
	if shouldWrapNameInColumn(row) {
		if lines := wrapTwoColumnSelectionRow(row, descCol, width); len(lines) > 0 {
			return lines
		}
	}
	indent := 0
	if row.WrapIndent != nil {
		indent = *row.WrapIndent
	} else if row.Description != "" || row.DisabledReason != "" {
		indent = descCol
	}
	if indent < 0 {
		indent = 0
	}
	if indent >= width {
		indent = max(width-1, 0)
	}
	if tui.DisplayWidth(line) <= width {
		return []string{line}
	}
	return tui.AdaptiveWrapLine(line, tui.WrapOptions{
		Width:            width,
		SubsequentIndent: strings.Repeat(" ", indent),
		BreakWords:       true,
	})
}

func wrapSelectionRowLines(row GenericDisplayRow, descCol int, width int) []string {
	return wrapSelectionRowLinesWithDescriptionLayout(row, descCol, width, SelectionDescriptionLayout{}, false)
}

func wrapSelectionRowLinesWithDescriptionLayout(row GenericDisplayRow, descCol int, width int, descriptionLayout SelectionDescriptionLayout, stackDescription bool) []string {
	if width <= 0 {
		width = 1
	}
	if stackDescription {
		return wrapStackedSelectionRow(row, width)
	}
	line := buildGenericDisplayLine(row, descCol, descriptionLayout)
	return wrapGenericDisplayLine(row, line, descCol, width)
}

func wrapStackedSelectionRow(row GenericDisplayRow, width int) []string {
	if width <= 0 {
		width = 1
	}
	prefixWidth := min(lenColumnsSelection(row.NamePrefix), max(width-1, 0))
	indent := strings.Repeat(" ", prefixWidth)
	label := row.Name
	if row.DisabledReason != "" {
		label += " (disabled)"
	}
	if row.DisplayShortcut != "" {
		label += " (" + row.DisplayShortcut + ")"
	}
	if row.CategoryTag != "" {
		label += "  " + row.CategoryTag
	}
	lines := tui.AdaptiveWrapLine(label, tui.WrapOptions{
		Width:            width,
		InitialIndent:    row.NamePrefix,
		SubsequentIndent: indent,
		BreakWords:       true,
	})
	if description := stackedSelectionDescription(row); description != "" {
		lines = append(lines, tui.AdaptiveWrapLine(description, tui.WrapOptions{
			Width:            width,
			InitialIndent:    indent,
			SubsequentIndent: indent,
			BreakWords:       true,
		})...)
	}
	return lines
}

func shouldWrapNameInColumn(row GenericDisplayRow) bool {
	return row.WrapIndent != nil &&
		row.Description != "" &&
		row.DisabledReason == "" &&
		len(row.MatchIndices) == 0 &&
		row.DisplayShortcut == "" &&
		row.CategoryTag == "" &&
		row.NamePrefix == ""
}

func wrapTwoColumnSelectionRow(row GenericDisplayRow, descCol int, width int) []string {
	if row.Description == "" {
		return nil
	}
	if width <= 0 {
		width = 1
	}
	maxDescCol := width - 1
	if maxDescCol == 0 {
		return nil
	}
	descCol = clampSelectionInt(descCol, 1, maxDescCol)
	leftWidth := max(descCol-2, 1)
	rightWidth := max(width-descCol, 1)
	nameIndent := 0
	if row.WrapIndent != nil {
		nameIndent = *row.WrapIndent
	}
	nameIndent = clampSelectionInt(nameIndent, 0, max(leftWidth-1, 0))
	nameLines := tui.AdaptiveWrapLine(row.Name, tui.WrapOptions{
		Width:            leftWidth,
		SubsequentIndent: strings.Repeat(" ", nameIndent),
		BreakWords:       true,
	})
	descLines := tui.AdaptiveWrapLine(row.Description, tui.WrapOptions{
		Width:      rightWidth,
		BreakWords: true,
	})
	count := max(len(nameLines), len(descLines))
	if count == 0 {
		count = 1
	}
	out := make([]string, 0, count)
	for idx := 0; idx < count; idx++ {
		line := ""
		if idx < len(nameLines) {
			line = nameLines[idx]
		}
		if idx < len(descLines) {
			leftUsed := lenColumnsSelection(line)
			gap := descCol
			if leftUsed > 0 {
				gap = max(descCol-leftUsed, 2)
			}
			line += strings.Repeat(" ", gap) + descLines[idx]
		}
		out = append(out, line)
	}
	return out
}

func computeGenericDescCol(rows []GenericDisplayRow, startIdx int, visibleItems int, width int, config ColumnWidthConfig) int {
	if width <= 1 {
		return 0
	}
	maxDescCol := width - 1
	switch config.Mode {
	case ColumnWidthFixed:
		return clampSelectionInt((width*fixedLeftColumnNumerator)/fixedLeftColumnDenominator, 1, maxDescCol)
	case ColumnWidthAutoAllRows, ColumnWidthAutoVisible:
		source := rows
		if config.Mode == ColumnWidthAutoVisible {
			end := min(startIdx+visibleItems, len(rows))
			if startIdx < end {
				source = rows[startIdx:end]
			} else {
				source = nil
			}
		}
		maxName := 0
		for _, row := range source {
			name := row.NamePrefix + row.Name
			if row.DisabledReason != "" {
				name += " (disabled)"
			}
			maxName = max(maxName, tui.DisplayWidth(name))
		}
		if config.NameColumnWidth != nil {
			maxName = max(maxName, *config.NameColumnWidth)
		}
		maxAuto := min(maxDescCol, max(1, width*selectionPopupAutoDescColRatio/10))
		return min(maxName+2, maxAuto)
	default:
		return min(maxDescCol, 1)
	}
}

func computeItemWindowStart(length int, state ScrollState, maxItems int) int {
	if length == 0 || maxItems <= 0 {
		return 0
	}
	start := min(max(state.ScrollTop, 0), length-1)
	if state.HasSelection {
		if state.SelectedIdx < start {
			start = state.SelectedIdx
		} else {
			bottom := start + maxItems - 1
			if state.SelectedIdx > bottom {
				start = state.SelectedIdx + 1 - maxItems
			}
		}
	}
	return min(max(start, 0), length-1)
}

func truncateSelectionName(name string, limit int) string {
	if limit < 0 || tui.DisplayWidth(name) <= limit {
		return name
	}
	if limit == 0 {
		return tui.TruncateWithEllipsis(name, 1)
	}
	return tui.TruncateWithEllipsis(name, limit)
}

func combinedSelectionDescription(row GenericDisplayRow, descriptionLayout SelectionDescriptionLayout) string {
	switch {
	case row.Description != "" && row.DisabledReason != "":
		return row.Description + " (disabled: " + row.DisabledReason + ")"
	case row.Description != "":
		return row.Description
	case row.DisabledReason != "":
		if descriptionLayout.Mode == SelectionDescriptionStackBelowWhenNarrow {
			return row.DisabledReason
		}
		return "disabled: " + row.DisabledReason
	default:
		return ""
	}
}

func stackedSelectionDescription(row GenericDisplayRow) string {
	switch {
	case row.Description != "" && row.DisabledReason != "":
		return row.Description + " (disabled: " + row.DisabledReason + ")"
	case row.Description != "":
		return row.Description
	case row.DisabledReason != "":
		return row.DisabledReason
	default:
		return ""
	}
}

func measureGenericRowsHeight(rows []GenericDisplayRow, state ScrollState, maxResults int, width int, config ColumnWidthConfig) int {
	if len(rows) == 0 {
		return 1
	}
	if width <= 0 {
		width = 1
	}
	contentWidth := max(width-1, 1)
	visibleItems := maxResults
	if visibleItems <= 0 || visibleItems > len(rows) {
		visibleItems = len(rows)
	}
	start := computeItemWindowStart(len(rows), state, visibleItems)
	descCol := computeGenericDescCol(rows, start, visibleItems, contentWidth, config)
	total := 0
	for _, row := range rows[start:min(start+visibleItems, len(rows))] {
		total += max(len(wrapSelectionRowLines(row, descCol, contentWidth)), 1)
	}
	return max(total, 1)
}

func (layout SelectionDescriptionLayout) shouldStack(width int, descCol int) bool {
	if layout.Mode != SelectionDescriptionStackBelowWhenNarrow {
		return false
	}
	descCol = min(max(descCol, 0), max(width, 0))
	return max(width-descCol, 0) < layout.MinDescriptionWidth
}

func lenColumnsSelection(text string) int {
	return tui.DisplayWidth(text)
}

func clampSelectionInt(value int, low int, high int) int {
	if value < low {
		return low
	}
	if value > high {
		return high
	}
	return value
}
