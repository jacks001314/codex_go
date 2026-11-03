//go:build !windows

package execserver

import (
	"errors"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// openRegularFileForBoundWrite creates a missing file or opens an existing one
// without truncating it. O_NOFOLLOW keeps a symlink at the final component from
// redirecting the bound handle (Rust #58974).
func openRegularFileForBoundWrite(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o666)
	if err != nil {
		return nil, err
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, os.ErrInvalid
	}
	return closeFileOnError(file, validateRegularFile(path, file, true))
}

// rejectMultipleLinks refuses a file another path can still mutate.
func rejectMultipleLinks(file *os.File) error {
	info, err := file.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	if stat.Nlink > 1 {
		return errors.New("output file has multiple links")
	}
	return nil
}
