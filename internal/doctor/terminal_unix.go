//go:build !windows

package doctor

import (
	"os"

	"golang.org/x/sys/unix"
)

func detectTerminalSizeForDoctor(file *os.File, isTerminal bool) terminalSizeProbe {
	if !isTerminal || file == nil {
		return terminalSizeProbe{Err: "not detected"}
	}
	size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return terminalSizeProbe{Err: err.Error()}
	}
	return terminalSizeProbe{Columns: int(size.Col), Rows: int(size.Row)}
}

func windowsConsoleDetailsForDoctor() []string {
	return nil
}
