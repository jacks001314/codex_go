//go:build windows

package appserverdaemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/windows"
)

// Rust #50782 prepare_install_windows_tests::publishing_release_retries_
// transient_access_denied: an executable scanner briefly holding the staged
// directory without delete sharing must not fail publication.
func TestPublishDaemonReleaseRetriesTransientLockLikeRust(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	release := filepath.Join(root, "release")
	if err := os.Mkdir(stage, 0o755); err != nil {
		t.Fatalf("mkdir stage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "payload"), []byte("complete"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	stagePath, err := windows.UTF16PtrFromString(stage)
	if err != nil {
		t.Fatalf("stage path: %v", err)
	}
	handle, err := windows.CreateFile(
		stagePath,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		t.Fatalf("hold staged directory: %v", err)
	}
	released := make(chan struct{})
	go func() {
		time.Sleep(150 * time.Millisecond)
		_ = windows.CloseHandle(handle)
		close(released)
	}()

	if err := publishDaemonRelease(stage, release); err != nil {
		t.Fatalf("publishDaemonRelease under a transient lock: %v", err)
	}
	<-released
	payload, err := os.ReadFile(filepath.Join(release, "payload"))
	if err != nil {
		t.Fatalf("read published payload: %v", err)
	}
	if string(payload) != "complete" {
		t.Fatalf("published payload = %q, want complete", payload)
	}
}

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
