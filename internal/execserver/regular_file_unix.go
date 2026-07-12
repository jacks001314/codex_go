//go:build !windows

package execserver

import (
	"os"

	"golang.org/x/sys/unix"
)

func openRegularFileForRead(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
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
