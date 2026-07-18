package appserver

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestWatchRejectsDuplicateForSameConnection(t *testing.T) {
	manager := NewConnectionFileWatchManager()
	path := filepath.Join(t.TempDir(), "HEAD")
	if _, err := manager.Watch(&ConnectionFileWatchParams{ConnectionID: "1", WatchID: "head", Path: path}); err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if _, err := manager.Watch(&ConnectionFileWatchParams{ConnectionID: "1", WatchID: "head", Path: path}); err == nil {
		t.Fatalf("expected duplicate watch error")
	}
	if _, err := manager.Watch(&ConnectionFileWatchParams{ConnectionID: "2", WatchID: "head", Path: path}); err != nil {
		t.Fatalf("same watch id on different connection should be allowed: %v", err)
	}
}

func TestUnwatchIsConnectionScoped(t *testing.T) {
	manager := NewConnectionFileWatchManager()
	path := filepath.Join(t.TempDir(), "HEAD")
	if _, err := manager.Watch(&ConnectionFileWatchParams{ConnectionID: "1", WatchID: "head", Path: path}); err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	if _, err := manager.Unwatch(&ConnectionFileUnwatchParams{ConnectionID: "2", WatchID: "head"}); err != nil {
		t.Fatalf("foreign Unwatch() error = %v", err)
	}
	if manager.WatchCount() != 1 {
		t.Fatalf("foreign unwatch should be no-op")
	}
	if _, err := manager.Unwatch(&ConnectionFileUnwatchParams{ConnectionID: "1", WatchID: "head"}); err != nil {
		t.Fatalf("owner Unwatch() error = %v", err)
	}
	if manager.WatchCount() != 0 {
		t.Fatalf("owner unwatch should remove watch")
	}
}

func TestChangedMatchesRecursiveWatches(t *testing.T) {
	manager := NewConnectionFileWatchManager()
	root := t.TempDir()
	if _, err := manager.Watch(&ConnectionFileWatchParams{ConnectionID: "1", WatchID: "root", Path: root, Recursive: true}); err != nil {
		t.Fatalf("Watch() error = %v", err)
	}
	notifications := manager.Changed(filepath.Join(root, "dir", "file.txt"))
	if len(notifications) != 1 || notifications[0].WatchID != "root" {
		t.Fatalf("unexpected notifications: %#v", notifications)
	}
}

func TestWatchRejectsNilParams(t *testing.T) {
	manager := NewConnectionFileWatchManager()
	if _, err := manager.Watch(nil); err == nil {
		t.Fatalf("expected nil params error")
	}
	if _, err := manager.Unwatch(nil); err != nil {
		t.Fatalf("Unwatch(nil) should be a no-op: %v", err)
	}
}

func TestConnectionFileChangedNotificationUsesRequiredArray(t *testing.T) {
	data, err := json.Marshal(&ConnectionFileChangedNotification{ConnectionID: "1", WatchID: "watch"})
	if err != nil {
		t.Fatalf("MarshalJSON() error = %v", err)
	}
	if !strings.Contains(string(data), `"changedPaths":[]`) {
		t.Fatalf("notification JSON = %s", data)
	}
}
