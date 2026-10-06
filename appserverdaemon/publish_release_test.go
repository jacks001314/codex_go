package appserverdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPublishDaemonReleaseMovesPackage covers the shared publish path: a staged
// package becomes the release directory, and a missing source reports the
// staging/release paths (Rust #50782).
func TestPublishDaemonReleaseMovesPackage(t *testing.T) {
	root := t.TempDir()
	stage := filepath.Join(root, "stage")
	if err := os.Mkdir(stage, 0o755); err != nil {
		t.Fatalf("mkdir stage: %v", err)
	}
	if err := os.WriteFile(filepath.Join(stage, "payload"), []byte("ok"), 0o644); err != nil {
		t.Fatalf("write payload: %v", err)
	}
	release := filepath.Join(root, "release")
	if err := publishDaemonRelease(stage, release); err != nil {
		t.Fatalf("publishDaemonRelease: %v", err)
	}
	if _, err := os.Stat(filepath.Join(release, "payload")); err != nil {
		t.Fatalf("published payload missing: %v", err)
	}

	err := publishDaemonRelease(filepath.Join(root, "missing"), filepath.Join(root, "release2"))
	if err == nil || !strings.Contains(err.Error(), "failed to publish managed daemon release from") {
		t.Fatalf("error = %v, want publish failure", err)
	}
}
