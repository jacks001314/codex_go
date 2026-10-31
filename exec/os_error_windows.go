package exec

import (
	"errors"
	"fmt"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// rustOSErrorMessage renders an OS error the way Rust's io::Error Display does
// on Windows: the localized system message from FormatMessageW without its
// trailing CRLF, followed by " (os error N)".
func rustOSErrorMessage(err error) (string, bool) {
	var errno syscall.Errno
	if !errors.As(err, &errno) {
		return "", false
	}
	buf := make([]uint16, 2048)
	n, formatErr := windows.FormatMessage(
		windows.FORMAT_MESSAGE_FROM_SYSTEM|windows.FORMAT_MESSAGE_IGNORE_INSERTS,
		0,
		uint32(errno),
		0,
		buf,
		nil,
	)
	if formatErr != nil || n == 0 {
		return "", false
	}
	message := strings.TrimRight(windows.UTF16ToString(buf[:n]), "\r\n")
	return fmt.Sprintf("%s (os error %d)", message, uint32(errno)), true
}
