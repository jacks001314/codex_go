package appserver

import (
	"testing"

	"codex_go/sandbox"
)

// TestRequestPermissionsMaterializesTargetNativeCWDLikeRust mirrors Rust #42314
// (`Preserve target-native cwd in permission approval requests`): the request
// cwd keeps the target environment's spelling, and granted access is
// materialized against that convention even when it differs from the host's.
func TestRequestPermissionsMaterializesTargetNativeCWDLikeRust(t *testing.T) {
	requested := &sandbox.AdditionalPermissionProfile{
		FileSystem:     []string{"src"},
		ReadFileSystem: []string{"logs"},
	}
	granted := &sandbox.AdditionalPermissionProfile{
		FileSystem:     []string{`C:\workspace\src\nested`, `C:\elsewhere`},
		ReadFileSystem: []string{`C:\workspace\logs\app.log`},
	}
	subset := intersectRequestedPermissions(requested, granted, "file:///C:/workspace")
	assertPermissionPaths(t, "write", subset.FileSystem, []string{`C:\workspace\src\nested`})
	assertPermissionPaths(t, "read", subset.ReadFileSystem, []string{`C:\workspace\logs\app.log`})
}

// TestRequestPermissionsDropsCrossConventionGrantLikeRust fails closed when the
// client answers with a path that belongs to another path convention.
func TestRequestPermissionsDropsCrossConventionGrantLikeRust(t *testing.T) {
	requested := &sandbox.AdditionalPermissionProfile{FileSystem: []string{"src"}}
	granted := &sandbox.AdditionalPermissionProfile{FileSystem: []string{"/posix/src"}}
	subset := intersectRequestedPermissions(requested, granted, "file:///C:/workspace")
	if len(subset.FileSystem) != 0 {
		t.Fatalf("write grants = %v, want none for a cross-convention answer", subset.FileSystem)
	}
}

// TestPermissionPathWithinUsesExecutorConventionLikeRust covers the containment
// helper directly: a Windows grant under a Windows request must match even when
// the Codex host cannot represent either path natively.
func TestPermissionPathWithinUsesExecutorConventionLikeRust(t *testing.T) {
	if !permissionPathWithin(`C:\workspace\src\nested`, `C:\workspace\src`) {
		t.Fatal("nested Windows grant was not recognized as inside its request")
	}
	if permissionPathWithin(`C:\workspace2`, `C:\workspace`) {
		t.Fatal("sibling Windows prefix was accepted as inside the request")
	}
	if !permissionPathWithin("/srv/work/src", "/srv/work") {
		t.Fatal("POSIX grant was not recognized as inside its request")
	}
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
