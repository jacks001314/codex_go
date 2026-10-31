//go:build !windows

package prompt

import "os"

// IsTerminalFile mirrors Rust's std::io::IsTerminal: a character device is a
// terminal on Unix. Windows overrides this in terminal_windows.go.
func IsTerminalFile(file *os.File) bool {
	if file == nil {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}
