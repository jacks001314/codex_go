//go:build windows

package applypatch

import (
	"errors"
	"fmt"
	"strings"
	"syscall"

	"golang.org/x/sys/windows"
)

// rustIOMessage renders an error the way Rust's std::io::Error Display does on
// Windows: the localized system message without its trailing CRLF followed by
// " (os error N)".
func rustIOMessage(err error) string {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		buf := make([]uint16, 2048)
		n, formatErr := windows.FormatMessage(
			windows.FORMAT_MESSAGE_FROM_SYSTEM|windows.FORMAT_MESSAGE_IGNORE_INSERTS,
			0,
			uint32(errno),
			0,
			buf,
			nil,
		)
		if formatErr == nil && n > 0 {
			message := strings.TrimRight(windows.UTF16ToString(buf[:n]), "\r\n")
			return fmt.Sprintf("%s (os error %d)", message, uint32(errno))
		}
	}
	return plainIOMessage(err)
}
