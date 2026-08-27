package sandbox

import (
	"path/filepath"
	"runtime"
	"testing"
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
