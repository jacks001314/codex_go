//go:build !linux && !darwin && !freebsd && !netbsd && !openbsd

package transcript_image

// TerminalCellSize reports an unknown cell size. Sixel previews therefore stay
// text-only on platforms whose console API does not expose pixel dimensions.
func TerminalCellSize() CellSize {
	return CellSize{}
}
