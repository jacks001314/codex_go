//go:build windows

package config

import (
	"errors"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

// configAccessDeniedErrno is ERROR_ACCESS_DENIED, which Rust reports when the
// configured path names a directory.
func configAccessDeniedErrno() error {
	return syscall.ERROR_ACCESS_DENIED
}

// configOSErrorText renders a filesystem error the way Rust's io::Error Display
// does on Windows: the system-localized message, then the numeric os error.
func configOSErrorText(err error) string {
	var errno syscall.Errno
	if !errors.As(err, &errno) || errno == 0 {
		return err.Error()
	}
	message := formatWindowsSystemMessage(uint32(errno))
	if message == "" {
		return err.Error()
	}
	return message + " (os error " + strconv.FormatUint(uint64(errno), 10) + ")"
}

func formatWindowsSystemMessage(code uint32) string {
	buffer := make([]uint16, 1024)
	length, err := windows.FormatMessage(
		windows.FORMAT_MESSAGE_FROM_SYSTEM|windows.FORMAT_MESSAGE_IGNORE_INSERTS,
		0,
		code,
		0,
		buffer,
		nil,
	)
	if err != nil || length == 0 {
		return ""
	}
	text := string(utf16.Decode(buffer[:length]))
	return strings.TrimRight(text, " \t\r\n\x00")
}
