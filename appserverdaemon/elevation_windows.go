//go:build windows

package appserverdaemon

import (
	"golang.org/x/sys/windows"
)

// currentProcessElevated reports whether the launcher's own token is elevated
// (Rust backend::windows::ensure_not_elevated's TokenElevation query).
var currentProcessElevated = func() bool {
	return windows.GetCurrentProcessToken().IsElevated()
}
