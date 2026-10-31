//go:build windows

package prompt

import (
	"os"
	"testing"
)

// Rust's std::io::IsTerminal asks the console for its mode, so the NUL device is
// not a terminal even though os.ModeCharDevice reports one.
func TestIsTerminalFileRejectsNULDeviceLikeRust(t *testing.T) {
	file, err := os.OpenFile("NUL", os.O_RDWR, 0)
	if err != nil {
		t.Skipf("cannot open NUL: %v", err)
	}
	defer file.Close()

	if IsTerminalFile(file) {
		t.Fatal(`NUL reported as a terminal`)
	}
}
