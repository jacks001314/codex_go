// Package footerhint ports Rust tui/src/footer_hint.rs: it packs footer hints
// into rows without separating a shortcut from its label. Callers retain
// ownership of styling and of clipping or wrapping oversized hints.
//
// It is a standalone leaf package because the agents-overview renderer must not
// import the root tui package (the root tui tests import the renderer).
package footerhint

// WrapHintRows packs hints into rows of at most width display columns, using
// separatorWidth between hints on the same row. hintWidth measures one hint's
// display width. A hint wider than width still gets its own row (the caller
// clips or wraps it), and an empty input yields one empty row.
func WrapHintRows[T any](hints []T, width int, separatorWidth int, hintWidth func(T) int) [][]T {
	limit := width
	if limit < 1 {
		limit = 1
	}
	rows := make([][]T, 0, 1)
	row := make([]T, 0, len(hints))
	used := 0
	for _, hint := range hints {
		hintSize := hintWidth(hint)
		if hintSize > limit {
			hintSize = limit
		}
		extra := hintSize
		if len(row) > 0 {
			extra = separatorWidth + hintSize
		}
		if len(row) > 0 && used+extra > limit {
			rows = append(rows, row)
			row = make([]T, 0, len(hints))
			used = 0
		}
		if len(row) == 0 {
			used = hintSize
		} else {
			used = used + separatorWidth + hintSize
		}
		row = append(row, hint)
	}
	return append(rows, row)
}
