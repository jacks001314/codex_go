package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	coresandbox "codex_go/sandbox"
)

func TestGrantReadRootNonElevatedRejectsInvalidPathsLikeRust(t *testing.T) {
	request := &ReadRootGrantRequest{}
	if _, err := grantReadRootNonElevated(request, "relative", nil); err == nil || !strings.Contains(err.Error(), "path must be absolute") {
		t.Fatalf("relative path error = %v", err)
	}

	missing := filepath.Join(t.TempDir(), "missing")
	if _, err := grantReadRootNonElevated(request, missing, nil); err == nil || !strings.Contains(err.Error(), "path does not exist") {
		t.Fatalf("missing path error = %v", err)
	}

	file := filepath.Join(t.TempDir(), "file.txt")
	if err := os.WriteFile(file, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := grantReadRootNonElevated(request, file, nil); err == nil || !strings.Contains(err.Error(), "path must be a directory") {
		t.Fatalf("file path error = %v", err)
	}
}

func TestGrantReadRootNonElevatedRefreshesWithExistingAndExtraReadRoots(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	profile := coresandbox.ReadOnlyPermissionProfile()
	var captured *SandboxSetupRequest
	canonical, err := grantReadRootNonElevated(&ReadRootGrantRequest{
		PermissionProfile: &profile,
		WorkspaceRoots:    []string{root},
		CommandCWD:        root,
		Env:               map[string]string{},
		CodexHome:         home,
	}, root, func(request *SandboxSetupRequest) error {
		captured = request
		return nil
	})
	if err != nil {
		t.Fatalf("grant read root: %v", err)
	}
	wantCanonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if canonical != filepath.Clean(wantCanonical) {
		t.Fatalf("canonical = %q, want %q", canonical, filepath.Clean(wantCanonical))
	}
	if captured == nil || !captured.Overrides.ReadRootsSet || !captured.Overrides.WriteRootsSet || len(captured.Overrides.WriteRoots) != 0 {
		t.Fatalf("refresh request = %#v", captured)
	}
	found := false
	for _, path := range captured.Overrides.ReadRoots {
		if CanonicalPathKey(path) == CanonicalPathKey(canonical) {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("read roots = %#v, missing %q", captured.Overrides.ReadRoots, canonical)
	}
}
