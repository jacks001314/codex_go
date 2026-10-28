//go:build linux

package execserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/utils"
)

// TestFSWatchMissingNotifiesOnCreatedDirectoryLikeRust exercises the Linux
// watch end to end: an empty observation is reusable until the missing path is
// created, then fs/missingChanged invalidates it.
func TestFSWatchMissingNotifiesOnCreatedDirectoryLikeRust(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, ".agents", "skills")
	uri, err := utils.FromHostNativePath(missing)
	if err != nil {
		t.Fatalf("FromHostNativePath: %v", err)
	}
	notifications := make(chan FSMissingChangedNotification, 1)
	server := &Server{}
	response := server.watchMissing(&FSWatchMissingParams{WatchID: 4, Missing: []string{uri.String()}}, func(method string, params any) {
		if method != MethodFSMissingChanged {
			return
		}
		if changed, ok := params.(FSMissingChangedNotification); ok {
			notifications <- changed
		}
	})
	if !response.Watching {
		t.Skip("filesystem does not support missing-path watches")
	}
	if err := os.MkdirAll(missing, 0o755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	select {
	case changed := <-notifications:
		if changed.WatchID != 4 {
			t.Fatalf("watchId = %d, want 4", changed.WatchID)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("fs/missingChanged was not delivered")
	}
	server.invalidateMissingWatch()
}

// TestFSWatchMissingRejectsPresentPathsLikeRust keeps an observation from
// starting when the project marker it depends on is missing.
func TestFSWatchMissingRejectsPresentPathsLikeRust(t *testing.T) {
	dir := t.TempDir()
	missingURI, err := utils.FromHostNativePath(filepath.Join(dir, ".agents", "skills"))
	if err != nil {
		t.Fatalf("FromHostNativePath: %v", err)
	}
	presentURI, err := utils.FromHostNativePath(filepath.Join(dir, "Cargo.toml"))
	if err != nil {
		t.Fatalf("FromHostNativePath: %v", err)
	}
	server := &Server{}
	response := server.watchMissing(&FSWatchMissingParams{WatchID: 1, Missing: []string{missingURI.String()}, Present: stringPtr(presentURI.String())}, nil)
	if response.Watching {
		t.Fatal("missing project marker unexpectedly watched")
	}
}
