//go:build linux || darwin || freebsd || netbsd || openbsd

package transcript_image

import (
	"os"

	"golang.org/x/sys/unix"
)

// TerminalCellSize reports the measured terminal cell size from the standard
// output's window size. Sixel previews require a nonzero result; Kitty scales
// by cells and does not.
func TerminalCellSize() CellSize {
	size, err := unix.IoctlGetWinsize(int(os.Stdout.Fd()), unix.TIOCGWINSZ)
	if err != nil || size.Col == 0 || size.Row == 0 || size.Xpixel == 0 || size.Ypixel == 0 {
		return CellSize{}
	}
	return CellSize{
		Width:  max(uint32(size.Xpixel)/uint32(size.Col), 1),
		Height: max(uint32(size.Ypixel)/uint32(size.Row), 1),
	}
}
