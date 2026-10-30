package sandbox

import "testing"

// TestNormalizeAdditionalPermissionsUsesExecutorCWDLikeRust mirrors Rust #42314:
// a permission request keeps the target environment's native cwd, so relative
// grants resolve under that environment's path convention instead of the Codex
// host's.
func TestNormalizeAdditionalPermissionsUsesExecutorCWDLikeRust(t *testing.T) {
	enabled := true
	profile := AdditionalPermissionProfile{
		Network:        &enabled,
		FileSystem:     []string{"src", `C:\workspace\nested`},
		ReadFileSystem: []string{"."},
	}
	normalized, err := NormalizeAdditionalPermissionsForExecutor(profile, "file:///C:/workspace")
	if err != nil {
		t.Fatalf("NormalizeAdditionalPermissionsForExecutor() error = %v", err)
	}
	assertPermissionPaths(t, "write", normalized.FileSystem, []string{`C:\workspace\src`, `C:\workspace\nested`})
	assertPermissionPaths(t, "read", normalized.ReadFileSystem, []string{`C:\workspace`})
	if normalized.Network == nil || !*normalized.Network {
		t.Fatalf("network = %v, want enabled", normalized.Network)
	}

	posix, err := NormalizeAdditionalPermissionsForExecutor(
		AdditionalPermissionProfile{FileSystem: []string{"src", "/srv/other"}},
		"file:///srv/work",
	)
	if err != nil {
		t.Fatalf("NormalizeAdditionalPermissionsForExecutor(posix) error = %v", err)
	}
	assertPermissionPaths(t, "posix write", posix.FileSystem, []string{"/srv/work/src", "/srv/other"})
}

// TestNormalizeAdditionalPermissionsRejectsCrossConventionGrantLikeRust keeps a
// grant spelled with another convention from being silently reinterpreted
// against the executor cwd.
func TestNormalizeAdditionalPermissionsRejectsCrossConventionGrantLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name  string
		cwd   string
		grant string
	}{
		{name: "posix grant for windows executor", cwd: "file:///C:/workspace", grant: "/posix/path"},
		{name: "windows grant for posix executor", cwd: "file:///srv/work", grant: `C:\windows\path`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NormalizeAdditionalPermissionsForExecutor(
				AdditionalPermissionProfile{FileSystem: []string{testCase.grant}},
				testCase.cwd,
			)
			if err == nil {
				t.Fatalf("grant %q under cwd %q was accepted, want a convention mismatch", testCase.grant, testCase.cwd)
			}
		})
	}
}

// TestNormalizeAdditionalPermissionsKeepsHostBehavior ensures the executor
// entry point still delegates to the host resolver for a native cwd.
func TestNormalizeAdditionalPermissionsKeepsHostBehavior(t *testing.T) {
	cwd := t.TempDir()
	normalized, err := NormalizeAdditionalPermissionsForExecutor(
		AdditionalPermissionProfile{FileSystem: []string{"src"}},
		cwd,
	)
	if err != nil {
		t.Fatalf("NormalizeAdditionalPermissionsForExecutor() error = %v", err)
	}
	want, err := NormalizeAdditionalPermissions(AdditionalPermissionProfile{FileSystem: []string{"src"}}, cwd)
	if err != nil {
		t.Fatalf("NormalizeAdditionalPermissions() error = %v", err)
	}
	assertPermissionPaths(t, "host write", normalized.FileSystem, want.FileSystem)
}

func assertPermissionPaths(t *testing.T, label string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s paths = %v, want %v", label, got, want)
	}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("%s paths = %v, want %v", label, got, want)
		}
	}
}
