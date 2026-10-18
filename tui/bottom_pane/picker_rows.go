package bottompane

// DefaultPickerRowColumns is Rust's picker_rows::COLUMNS: measure names across
// all rows, hide narrow description columns, and paint full-width selections.
func DefaultPickerRowColumns() ColumnWidthConfig {
	return NewColumnWidthConfig(ColumnWidthAutoAllRows, nil).
		WithDescriptionLayout(NewHideWhenNarrowDescriptionLayout(24)).
		WithFullWidthSelection()
}

func pickerRowSelectionPrefix(selected bool) string {
	if selected {
		return selectedRowMarker + " "
	}
	return "  "
}

// MeasurePickerRowsHeight measures the shared dedicated-picker row block,
// including Rust's two overflow spacer rows.
func MeasurePickerRowsHeight(rows []GenericDisplayRow, state ScrollState, maxResults int, width int) int {
	return MeasureGenericRowsHeight(rows, state, maxResults, max(width, 1), DefaultPickerRowColumns()) + 2
}

// RenderPickerRows renders wrapped dedicated-picker rows with shared columns.
func RenderPickerRows(rows []GenericDisplayRow, state ScrollState, maxResults int, emptyMessage string, width int) []string {
	return RenderGenericRows(rows, state, maxResults, emptyMessage, max(width, 1), DefaultPickerRowColumns())
}

// RenderPickerRowsSingleLine renders single-line dedicated-picker rows.
func RenderPickerRowsSingleLine(rows []GenericDisplayRow, state ScrollState, maxResults int, emptyMessage string, width int) []string {
	return RenderGenericRowsSingleLine(rows, state, maxResults, emptyMessage, max(width, 1), DefaultPickerRowColumns())
}
