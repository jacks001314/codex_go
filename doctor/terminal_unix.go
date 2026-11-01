//go:build !windows

package doctor

import (
	"os"

	"golang.org/x/sys/unix"
)

// fileIsTerminal mirrors std::io::IsTerminal: /dev/null is a character device
// but not a terminal, so ask for the window size instead of the file mode.
func fileIsTerminal(file *os.File) bool {
	if file == nil {
		return false
	}
	_, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	return err == nil
}

func detectTerminalSizeForDoctor(file *os.File, isTerminal bool) terminalSizeProbe {
	if file == nil {
		return terminalSizeProbe{Err: "not detected"}
	}
	size, err := unix.IoctlGetWinsize(int(file.Fd()), unix.TIOCGWINSZ)
	if err != nil {
		return terminalSizeProbe{Err: "not detected"}
	}
	return terminalSizeProbe{Columns: int(size.Col), Rows: int(size.Row)}
}

func windowsConsoleDetailsForDoctor() []string {
	return nil
}
