package bottompane

// PickerRect is a terminal-cell rectangle used by the shared picker layout.
type PickerRect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// PickerLayoutSizes describes the preferred picker chrome. Optional gaps and
// indicators yield before the header, tabs, search, and minimum result row.
type PickerLayoutSizes struct {
	Header    int
	HeaderGap int
	Tabs      int
	Search    int
	Rows      int
	Side      int
}

// PickerAreas mirrors Rust's selection_picker_layout::picker_areas. The ten
// entries are header, header gap, tabs, tab gap, search, above indicator, rows,
// below indicator, side gap and side content.
func PickerAreas(area PickerRect, sizes PickerLayoutSizes) [10]PickerRect {
	tabs := min(sizes.Tabs, area.Height)
	search := min(sizes.Search, max(area.Height-tabs, 0))
	minimumRows := min(sizes.Rows, boolInt(tabs > 0, 1, 3))
	minimumHeader := 0
	if sizes.Header > 0 && area.Height-(tabs+search) > boolInt(sizes.Rows > 0, 1, 0) {
		minimumHeader = 1
	}
	header := min(sizes.Header, max(area.Height-(tabs+search+minimumRows), 0))
	if header < minimumHeader {
		header = minimumHeader
	}
	remaining := max(area.Height-(header+tabs+search), 0)
	indicators := boolInt(remaining >= minimumRows+2, 1, 0)
	remaining = max(remaining-indicators*2, 0)
	minimumRows = min(min(sizes.Rows, 3), remaining)
	headerGap := 0
	if header > 0 {
		headerGap = min(sizes.HeaderGap, max(remaining-minimumRows, 0))
	}
	remaining = max(remaining-headerGap, 0)
	tabGap := boolInt(tabs > 0 && search > 0 && remaining > minimumRows, 1, 0)
	remaining = max(remaining-tabGap, 0)
	side := min(sizes.Side, max(remaining-(minimumRows+1), 0))
	sideGap := boolInt(side > 0, 1, 0)
	rows := min(sizes.Rows, max(remaining-(side+sideGap), 0))

	heights := [10]int{
		header,
		headerGap,
		tabs,
		tabGap,
		search,
		indicators,
		rows,
		indicators,
		sideGap,
		side,
	}
	var out [10]PickerRect
	y := area.Y
	for index, height := range heights {
		out[index] = PickerRect{X: area.X, Y: y, Width: area.Width, Height: height}
		y += height
	}
	return out
}

func boolInt(condition bool, whenTrue int, whenFalse int) int {
	if condition {
		return whenTrue
	}
	return whenFalse
}
