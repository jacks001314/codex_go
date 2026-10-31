//go:build !windows

package config

import "syscall"

// configAccessDeniedErrno is EACCES, the error Rust reports for a directory.
func configAccessDeniedErrno() error {
	return syscall.EACCES
}

// configOSErrorText keeps Go's message on platforms where Rust prints the same
// strerror text.
func configOSErrorText(err error) string {
	return err.Error()
}
