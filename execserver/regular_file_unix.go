//go:build !windows

package execserver

import (
	"os"

	"golang.org/x/sys/unix"
)

func openRegularFileForRead(path string) (*os.File, error) {
	// Rust #39200: O_NOFOLLOW prevents a symlink at the final path component
	// from redirecting sensitive reads.
	fd, err := unix.Open(path, unix.O_RDONLY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
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

// openRegularFileForWrite implements Rust #50177's `fs/open` replacement mode:
// create a missing file or truncate an existing one for positional writes.
// O_NOFOLLOW keeps a symlink at the final path component from redirecting the
// write, and O_NONBLOCK mirrors the read variant's FIFO protection.
func openRegularFileForWrite(path string) (*os.File, error) {
	fd, err := unix.Open(path, unix.O_WRONLY|unix.O_CREAT|unix.O_TRUNC|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0o666)
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
