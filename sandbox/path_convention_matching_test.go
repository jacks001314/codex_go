package sandbox

import (
	"runtime"
	"testing"

	"codex_go/utils"
)

// Mirrors Rust #41209: containment follows the executor's convention, not the
// Go host's. POSIX stays case-sensitive; Windows folds case and separators.
func TestPathWithinForConventionLikeRust(t *testing.T) {
	if pathWithinForConvention("/workspace/secret.env", "/workspace/SECRET.env", utils.ConventionPosix) {
		t.Fatal("POSIX containment folded case")
	}
	if !pathWithinForConvention("/workspace/app/secret.env", "/workspace", utils.ConventionPosix) {
		t.Fatal("POSIX subtree containment was not recognized")
	}
	if !pathWithinForConvention(`c:/WORKSPACE/App/.ENV`, `C:\workspace`, utils.ConventionWindows) {
		t.Fatal("Windows containment did not fold case and separators")
	}
	if pathWithinForConvention(`C:\workspace-other\a.env`, `C:\workspace`, utils.ConventionWindows) {
		t.Fatal("Windows containment matched a sibling prefix")
	}
}

// The access resolver uses the same convention-aware containment for exact
// path entries.
func TestResolveFileSystemEntryAccessUsesExecutorConventionLikeRust(t *testing.T) {
	entries := []FileSystemSandboxEntry{{
		Path:   FileSystemPath{Type: "path", Path: "/workspace/SECRET.env"},
		Access: FileSystemAccessDeny,
	}}
	if got := ResolveFileSystemEntryAccessForConvention(entries, "/workspace/secret.env", "/workspace", utils.ConventionPosix); got != "" {
		t.Fatalf("POSIX access = %q, want no match", got)
	}
	if got := ResolveFileSystemEntryAccessForConvention(entries, "/workspace/SECRET.env/child", "/workspace", utils.ConventionPosix); got != FileSystemAccessDeny {
		t.Fatalf("POSIX subtree access = %q, want deny", got)
	}
	if runtime.GOOS == "windows" {
		windows := []FileSystemSandboxEntry{{
			Path:   FileSystemPath{Type: "path", Path: `C:\workspace\SECRET.env`},
			Access: FileSystemAccessDeny,
		}}
		if got := ResolveFileSystemEntryAccessForConvention(windows, `c:/WORKSPACE/secret.env`, `C:\workspace`, utils.ConventionWindows); got != FileSystemAccessDeny {
			t.Fatalf("Windows access = %q, want case-insensitive deny", got)
		}
	}
}

// Mirrors Rust #41928: a Windows permission grant can be intersected from a
// non-Windows host when the executor convention is explicit.
func TestIntersectPermissionProfilesUsesExecutorConventionLikeRust(t *testing.T) {
	requested := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{
		Write: []string{`C:\workspace\extra`},
	}}
	granted := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{
		Write: []string{`c:/WORKSPACE/EXTRA`},
	}}
	got := IntersectPermissionProfilesWithConvention(requested, granted, `C:\workspace`, utils.ConventionWindows)
	if got.FileSystem == nil || len(got.FileSystem.Entries) != 1 {
		t.Fatalf("intersection = %#v", got)
	}
	if path := got.FileSystem.Entries[0].Path.Path; path != `c:\WORKSPACE\EXTRA` {
		t.Fatalf("intersected path = %q", path)
	}
}

func TestNormalizeRequestPermissionProfileURIsLikeRust(t *testing.T) {
	profile := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{
		Write: []string{"file:///C:/workspace/extra"},
	}}
	normalized, err := NormalizeRequestPermissionProfilePathsForConvention(profile, utils.ConventionWindows)
	if err != nil {
		t.Fatalf("NormalizeRequestPermissionProfilePathsForConvention() error = %v", err)
	}
	if len(normalized.FileSystem.Write) != 1 || normalized.FileSystem.Write[0] != `C:\workspace\extra` {
		t.Fatalf("normalized write roots = %#v", normalized.FileSystem.Write)
	}
	if _, err := NormalizeRequestPermissionProfilePathsForConvention(profile, utils.ConventionPosix); err == nil {
		t.Fatal("foreign URI convention must fail closed")
	}
}

func TestAdditionalPermissionsMaterializeByConventionLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	additional := &AdditionalPermissionProfile{FileSystem: []string{`c:/WORKSPACE/EXTRA`}}
	effective, err := PermissionProfileWithAdditionalPermissionsForConvention(&profile, additional, utils.ConventionWindows)
	if err != nil {
		t.Fatalf("PermissionProfileWithAdditionalPermissionsForConvention() error = %v", err)
	}
	found := false
	for _, root := range effective.LegacySandboxPolicy().WritableRoots {
		if root == `c:\WORKSPACE\EXTRA` {
			found = true
		}
	}
	if !found {
		t.Fatalf("writable roots = %#v, want Windows-normalized additional root", effective.LegacySandboxPolicy().WritableRoots)
	}
}

func TestSymbolicTmpdirGrantFailsClosedWithoutExecutorMetadataLikeRust(t *testing.T) {
	t.Setenv("TMPDIR", "")
	special := FileSystemSandboxEntry{
		Path:   FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "tmpdir"}},
		Access: FileSystemAccessWrite,
	}
	requested := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Entries: []FileSystemSandboxEntry{special}}}
	granted := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Entries: []FileSystemSandboxEntry{special}}}
	if got := IntersectPermissionProfilesWithConvention(requested, granted, "/workspace", utils.ConventionPosix); got.FileSystem != nil {
		t.Fatalf("unresolved tmpdir grant was accepted: %#v", got)
	}

	t.Setenv("TMPDIR", "/tmp")
	if got := IntersectPermissionProfilesWithConvention(requested, granted, "/workspace", utils.ConventionPosix); got.FileSystem == nil || len(got.FileSystem.Entries) != 1 {
		t.Fatalf("resolved tmpdir grant = %#v", got)
	}
}
