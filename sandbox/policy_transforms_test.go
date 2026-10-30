package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"codex_go/utils"
)

func TestIntersectPermissionProfilesRejectsSymbolicSlashTmpGrantsOnWindowsLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows-specific symbolic slash_tmp semantics")
	}
	cwd := t.TempDir()
	slashTmp := FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "slash_tmp"}}
	granted := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Entries: []FileSystemSandboxEntry{{Path: slashTmp, Access: FileSystemAccessWrite}}}}

	requestedLiteral := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Write: []string{"/tmp"}}}
	if got := IntersectPermissionProfiles(requestedLiteral, granted, cwd); got.FileSystem != nil || got.Network != nil {
		t.Fatalf("literal /tmp request intersect symbolic grant = %#v", got)
	}

	requestedSymbolic := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Entries: []FileSystemSandboxEntry{{Path: slashTmp, Access: FileSystemAccessWrite}}}}
	if got := IntersectPermissionProfiles(requestedSymbolic, granted, cwd); got.FileSystem != nil || got.Network != nil {
		t.Fatalf("symbolic slash_tmp request intersect symbolic grant = %#v", got)
	}
}

// Mirrors Rust #41209: Windows exact-path and glob matching fold case and
// normalize separators, so a deny rule cannot be bypassed by casing.
func TestPermissionPathAndGlobMatchingFoldsCaseOnWindowsLikeRust(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows case-insensitive path identity")
	}
	root := `C:\Repo`
	entries := []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "path", Path: root}, Access: FileSystemAccessDeny},
		{Path: FileSystemPath{Type: "glob_pattern", Pattern: `C:\Repo\**\*.KEY`}, Access: FileSystemAccessDeny},
	}
	for _, target := range []string{`c:\repo\secret.txt`, `C:\REPO\keys\server.key`} {
		if got := ResolveFileSystemEntryAccess(entries, target, root); got != FileSystemAccessDeny {
			t.Fatalf("ResolveFileSystemEntryAccess(%q) = %q, want deny", target, got)
		}
	}
}

func TestIntersectPermissionProfilesMaterializesCWDAndRestrictsNetworkLikeRust(t *testing.T) {
	cwd := t.TempDir()
	requestedNetwork := true
	grantedNetwork := true
	projectRoots := FileSystemPath{Type: "special", Value: &FileSystemSpecialPath{Kind: "project_roots"}}
	requested := RequestPermissionProfile{
		Network: &AdditionalNetworkPermissions{Enabled: &requestedNetwork},
		FileSystem: &AdditionalFileSystemPermissions{Entries: []FileSystemSandboxEntry{
			{Path: projectRoots, Access: FileSystemAccessWrite},
		}},
	}
	child := filepath.Join(cwd, "child")
	granted := RequestPermissionProfile{
		Network: &AdditionalNetworkPermissions{Enabled: &grantedNetwork},
		FileSystem: &AdditionalFileSystemPermissions{Entries: []FileSystemSandboxEntry{
			{Path: FileSystemPath{Type: "path", Path: child}, Access: FileSystemAccessWrite},
		}},
	}
	got := IntersectPermissionProfiles(requested, granted, cwd)
	if !permissionNetworkEnabled(got.Network) || got.FileSystem == nil || len(got.FileSystem.Entries) != 1 {
		t.Fatalf("intersection = %#v", got)
	}
	entry := got.FileSystem.Entries[0]
	if entry.Access != FileSystemAccessWrite || entry.Path.Type != "path" || entry.Path.Path != cleanRunPath(child) {
		t.Fatalf("accepted entry = %#v", entry)
	}

	networkDenied := false
	granted.Network.Enabled = &networkDenied
	if got := IntersectPermissionProfiles(requested, granted, cwd); got.Network != nil {
		t.Fatalf("network intersection = %#v", got.Network)
	}
}

func TestIntersectPermissionProfilesRetainsConstrainingDenyLikeRust(t *testing.T) {
	cwd := t.TempDir()
	root := filepath.Join(cwd, "workspace")
	secret := filepath.Join(root, "secret")
	requested := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Entries: []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "path", Path: root}, Access: FileSystemAccessWrite},
		{Path: FileSystemPath{Type: "path", Path: secret}, Access: FileSystemAccessDeny},
	}}}
	granted := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Entries: []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "path", Path: root}, Access: FileSystemAccessWrite},
	}}}
	got := IntersectPermissionProfiles(requested, granted, cwd)
	if got.FileSystem == nil || len(got.FileSystem.Entries) != 2 {
		t.Fatalf("intersection = %#v", got)
	}
	if got.FileSystem.Entries[1].Access != FileSystemAccessDeny || got.FileSystem.Entries[1].Path.Path != cleanRunPath(secret) {
		t.Fatalf("retained deny = %#v", got.FileSystem.Entries[1])
	}
}

func TestPathsMayOverlapMatchesAncestorDescendantAndDisjointLikeRust(t *testing.T) {
	base := t.TempDir()
	if !pathsMayOverlap(filepath.Join(base, "a"), filepath.Join(base, "a", "b")) {
		t.Fatal("pathsMayOverlap(ancestor, descendant) = false, want true")
	}
	if !pathsMayOverlap(filepath.Join(base, "a"), filepath.Join(base, "a")) {
		t.Fatal("pathsMayOverlap(equal) = false, want true")
	}
	if pathsMayOverlap(filepath.Join(base, "a"), filepath.Join(base, "b")) {
		t.Fatal("pathsMayOverlap(disjoint) = true, want false")
	}
}

// Mirrors Rust #41909: home-relative deny globs resolve against the executor's
// home directory, so intersecting a home grant keeps the deny constraint.
func TestIntersectPermissionProfilesExpandsHomeRelativeDenyGlobLikeRust(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Skip("no home directory available")
	}
	cwd := t.TempDir()
	requested := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Entries: []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "path", Path: home}, Access: FileSystemAccessWrite},
		{Path: FileSystemPath{Type: "glob_pattern", Pattern: "~/secrets/**"}, Access: FileSystemAccessDeny},
	}}}
	granted := RequestPermissionProfile{FileSystem: &AdditionalFileSystemPermissions{Write: []string{home}}}

	got := IntersectPermissionProfiles(requested, granted, cwd)
	if got.FileSystem == nil {
		t.Fatal("intersection dropped the accepted grant")
	}
	want := cleanRunPath(filepath.Join(home, "secrets", "**"))
	for _, entry := range got.FileSystem.Entries {
		if entry.Access != FileSystemAccessDeny {
			continue
		}
		if entry.Path.Type != "glob_pattern" || cleanRunPath(entry.Path.Pattern) != want {
			t.Fatalf("retained deny = %#v, want pattern %q", entry, want)
		}
		return
	}
	t.Fatalf("home-relative deny glob was dropped: %#v", got.FileSystem.Entries)
}

func TestMaterializePermissionGlobExpandsHomeLikeRust(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Skip("no home directory available")
	}
	cwd := t.TempDir()
	if got, want := materializePermissionGlob("~/keys/*.pem", cwd), cleanRunPath(filepath.Join(home, "keys", "*.pem")); got != want {
		t.Fatalf("materializePermissionGlob(~/keys/*.pem) = %q, want %q", got, want)
	}
	if got, want := materializePermissionGlob("~", cwd), cleanRunPath(home); got != want {
		t.Fatalf("materializePermissionGlob(~) = %q, want %q", got, want)
	}
	if got, want := materializePermissionGlob("~user/keys", cwd), cleanRunPath(filepath.Join(cwd, "~user", "keys")); got != want {
		t.Fatalf("materializePermissionGlob(~user/keys) = %q, want %q", got, want)
	}
}

func TestMaterializePermissionGlobForConventionExpandsHomeLikeRust(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || strings.TrimSpace(home) == "" {
		t.Skip("no home directory available")
	}
	if runtime.GOOS == "windows" {
		want := cleanRunPath(filepath.Join(home, "keys", "*.pem"))
		if got := materializePermissionGlobForConvention(`~\keys\*.pem`, `C:\work`, utils.ConventionWindows); got != want {
			t.Fatalf("materializePermissionGlobForConvention(Windows home) = %q, want %q", got, want)
		}
	}
	// Rust only treats ~\ as home-relative under the Windows convention.
	if got := materializePermissionGlobForConvention(`~\keys`, "/work", utils.ConventionPosix); got != "/work/~\\keys" {
		t.Fatalf("materializePermissionGlobForConvention(POSIX, ~\\keys) = %q", got)
	}
}
