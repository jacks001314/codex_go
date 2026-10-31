package prompt

import (
	"os"

	"golang.org/x/sys/windows"
)

// IsTerminalFile mirrors Rust's std::io::IsTerminal on Windows, which asks the
// console for its mode. os.ModeCharDevice is not equivalent here: the NUL device
// and other character devices report it, while Rust treats them as non-terminal.
func IsTerminalFile(file *os.File) bool {
	if file == nil {
		return false
	}
	var mode uint32
	return windows.GetConsoleMode(windows.Handle(file.Fd()), &mode) == nil
}
