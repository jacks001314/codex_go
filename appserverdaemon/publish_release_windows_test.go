//go:build windows

package appserverdaemon

import (
	"os"
	"testing"

	"golang.org/x/sys/windows"
)

// TestIsTransientPublishErrorLikeRust mirrors Rust's retry predicate: only
// permission-denied and sharing violations are retried (#50782).
func TestIsTransientPublishErrorLikeRust(t *testing.T) {
	linkErr := func(errno windows.Errno) error {
		return &os.LinkError{Op: "rename", Old: "a", New: "b", Err: errno}
	}
	if !isTransientPublishError(linkErr(windows.ERROR_SHARING_VIOLATION)) {
		t.Fatal("sharing violation should be transient")
	}
	if !isTransientPublishError(linkErr(windows.ERROR_ACCESS_DENIED)) {
		t.Fatal("access denied should be transient")
	}
	if isTransientPublishError(linkErr(windows.ERROR_FILE_NOT_FOUND)) {
		t.Fatal("missing file should not be transient")
	}
}
