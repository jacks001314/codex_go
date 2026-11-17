package runtimeutil

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestUpdateDisplayRootsKeepsFirstRootLikeRust pins Rust #61820
// `TurnDiffTracker::update_environment_display_roots`: a steer cannot change
// the root an already published relative path refers to, while a newly added
// environment re-renders the cached diff with its id prefix.
func TestUpdateDisplayRootsKeepsFirstRootLikeRust(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	path := filepath.Join(firstRoot, "file.txt")
	change := FileChange{Kind: ChangeUpdate, Path: path, OldContent: "one\n", NewContent: "two\n"}

	tracker := NewDiffTracker()
	tracker.UpdateDisplayRoots(map[string]string{"local": firstRoot})
	tracker.Track("local", []FileChange{change}, true)
	if diff := tracker.UnifiedDiff(); diff == nil || !strings.Contains(*diff, "a/file.txt") {
		t.Fatalf("first root diff = %v", diff)
	}

	// A later steer that reports a different root for the same environment must
	// not move the path that was already published.
	tracker.UpdateDisplayRoots(map[string]string{"local": secondRoot})
	tracker.Track("local", []FileChange{change}, true)
	if diff := tracker.UnifiedDiff(); diff == nil || !strings.Contains(*diff, "a/file.txt") {
		t.Fatalf("second root rewrote the published path: %v", diff)
	}

	// Adding an environment adds an id prefix to every header, so the cached
	// diff has to be rebuilt even before the next change arrives.
	tracker.UpdateDisplayRoots(map[string]string{"remote": secondRoot})
	diff := tracker.UnifiedDiff()
	if diff == nil || !strings.Contains(*diff, "a/local/file.txt") || !strings.Contains(*diff, "b/local/file.txt") {
		t.Fatalf("new environment did not refresh headers: %v", diff)
	}
}
