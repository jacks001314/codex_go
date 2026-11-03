package sandbox

import (
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	"codex_go/sandbox/sandboxpath"
)

// Rust #59009: the resolved writable roots are exactly the roots the backends
// used to derive for themselves, so sharing the resolver cannot widen or narrow
// a policy by accident.
func TestResolveFileSystemPermissionsKeepsWritableRootsLikeRust(t *testing.T) {
	root := t.TempDir()
	extra := filepath.Join(root, "extra")
	profile := WorkspaceWritePermissionProfile()
	policy := profile.LegacySandboxPolicy()
	policy.WritableRoots = []string{extra}
	profile.SandboxPolicy = policy

	resolved, err := ResolveFileSystemPermissionsForEnforcement(&profile, root, sandboxpath.ProjectionPreserveMutableRoots)
	if err != nil {
		t.Fatalf("ResolveFileSystemPermissionsForEnforcement() error = %v", err)
	}
	want := policy.GetWritableRootsWithCWD(root)
	if resolved.HasFullDiskWriteAccess {
		t.Fatalf("workspace-write profile reported full disk write access")
	}
	gotRoots := make([]string, 0, len(resolved.WritableRoots))
	for _, writable := range resolved.WritableRoots {
		gotRoots = append(gotRoots, writable.Root)
	}
	wantRoots := make([]string, 0, len(want))
	for _, writable := range want {
		wantRoots = append(wantRoots, writable.Root)
	}
	if !reflect.DeepEqual(gotRoots, wantRoots) {
		sort.Strings(gotRoots)
		sort.Strings(wantRoots)
		if !reflect.DeepEqual(gotRoots, wantRoots) {
			t.Fatalf("writable roots = %#v, want %#v", gotRoots, wantRoots)
		}
	}
	wantByRoot := map[string][]string{}
	for _, writable := range want {
		wantByRoot[writable.Root] = writable.ReadOnlySubpaths
	}
	for _, writable := range resolved.WritableRoots {
		if !reflect.DeepEqual(writable.ReadOnlySubpaths, wantByRoot[writable.Root]) {
			t.Fatalf("writable carveouts for %q = %#v, want %#v", writable.Root, writable.ReadOnlySubpaths, wantByRoot[writable.Root])
		}
	}
}

// A deny entry that a readable grant reopens must not become an unreadable root
// (Rust #59009 resolves precedence before the backend masks paths).
func TestResolveFileSystemPermissionsDropsReopenedDenialsLikeRust(t *testing.T) {
	profile := ReadOnlyPermissionProfile()
	profile.DeniedReadEntries = []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "path", Path: "/never/materialized"}, Access: FileSystemAccessDeny},
	}
	resolved, err := ResolveFileSystemPermissionsForEnforcement(&profile, "/workspace", sandboxpath.ProjectionNative)
	if err != nil {
		t.Fatalf("ResolveFileSystemPermissionsForEnforcement() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.UnreadableRoots, []string{"/never/materialized"}) {
		t.Fatalf("unreadable roots = %#v", resolved.UnreadableRoots)
	}
}

// Seatbelt still enforces explicit read denials layered over a fully writable
// policy; the shared resolver keeps them instead of short-circuiting them away.
func TestBuildSeatbeltPolicyKeepsDenialsForFullAccessLikeRust(t *testing.T) {
	root := t.TempDir()
	secret := filepath.Join(root, "id_rsa")
	profile := PermissionProfile{SandboxPolicy: NewDangerFullAccessPolicy(), NetworkEnabled: true}
	profile.DeniedReadEntries = []FileSystemSandboxEntry{
		{Path: FileSystemPath{Type: "path", Path: secret}, Access: FileSystemAccessDeny},
	}
	policy, parameters, err := buildSeatbeltPolicy(root, &profile, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicy: %v", err)
	}
	if !seatbeltHasParameterPrefix(parameters, "DENIED_READ_") {
		t.Fatalf("full-access policy dropped an explicit read denial:\n%s", policy)
	}
}
