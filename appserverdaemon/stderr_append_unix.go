//go:build !windows

package appserverdaemon

import (
	"os"

	"golang.org/x/sys/unix"
)

// enableStderrAppendMode makes this process's stderr append to EOF, so writes
// after a truncation never reopen a hole in the managed log (Rust #55856
// `enable_append`).
func enableStderrAppendMode() error {
	fd := os.Stderr.Fd()
	flags, err := unix.FcntlInt(fd, unix.F_GETFL, 0)
	if err != nil {
		return err
	}
	if _, err := unix.FcntlInt(fd, unix.F_SETFL, flags|unix.O_APPEND); err != nil {
		return err
	}
	return nil
}
