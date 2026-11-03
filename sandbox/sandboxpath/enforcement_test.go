package sandboxpath

import (
	"reflect"
	"testing"
)

// Rust #59009: one resolver collects grants, denials, glob masks and platform
// defaults for every sandbox backend.
func TestResolveForEnforcementCollectsGrantsDenialsAndGlobs(t *testing.T) {
	req := EnforcementRequest{
		CWD:                     "/repo",
		HasFullDiskReadAccess:   false,
		HasFullDiskWriteAccess:  false,
		IncludePlatformDefaults: true,
		PlatformDefaultReadRoots: []string{
			"/usr", "/etc",
		},
		Entries: []EnforcementEntry{
			{Access: "read", Path: "/usr"},
			{Access: "write", Path: "/repo"},
			{Access: "write", Path: "/repo"},
			{Access: "deny", Path: "/repo/.env"},
			{Access: "deny", Path: "/"},
			{Access: "deny", Pattern: "**/*.pem"},
		},
		Projection: ProjectionPreserveMutableRoots,
		CanRead: func(path string) bool {
			return path == "/repo/.env"
		},
	}
	resolved, err := ResolveForEnforcement(req)
	if err != nil {
		t.Fatalf("ResolveForEnforcement() error = %v", err)
	}
	// A writable root is readable too, so it joins the readable grant set in
	// policy order (Rust #59009 keeps entry order).
	if resolved.ReadableRoots == nil || !reflect.DeepEqual(resolved.ReadableRoots, []string{"/usr", "/repo", "/etc"}) {
		t.Fatalf("readable roots = %#v", resolved.ReadableRoots)
	}
	wantWritable := []WritableRoot{{Root: "/repo"}}
	if !reflect.DeepEqual(resolved.WritableRoots, wantWritable) {
		t.Fatalf("writable roots = %#v", resolved.WritableRoots)
	}
	if resolved.UnreadableRoots != nil {
		t.Fatalf("unreadable roots = %#v, want the carveout-filtered set", resolved.UnreadableRoots)
	}
	if !reflect.DeepEqual(resolved.UnreadableGlobs, []string{"**/*.pem"}) {
		t.Fatalf("unreadable globs = %#v", resolved.UnreadableGlobs)
	}
	if !resolved.IncludePlatformDefaults {
		t.Fatal("platform defaults were dropped")
	}
}

func TestResolveForEnforcementKeepsDenialsForFullyWritablePolicy(t *testing.T) {
	depth := 4
	req := EnforcementRequest{
		HasFullDiskReadAccess:  true,
		HasFullDiskWriteAccess: true,
		Entries: []EnforcementEntry{
			{Access: "write", Path: "/"},
			{Access: "deny", Path: "/home/secrets"},
			{Access: "deny", Pattern: "/repo/*.key"},
		},
		GlobScanMaxDepth: &depth,
	}
	resolved, err := ResolveForEnforcement(req)
	if err != nil {
		t.Fatalf("ResolveForEnforcement() error = %v", err)
	}
	if len(resolved.ReadableRoots) != 0 || len(resolved.WritableRoots) != 0 || len(resolved.UnreadableRoots) != 0 {
		t.Fatalf("fully writable policy resolved roots = %#v", resolved)
	}
	if !reflect.DeepEqual(resolved.UnreadableGlobs, []string{"/repo/*.key"}) {
		t.Fatalf("unreadable globs = %#v", resolved.UnreadableGlobs)
	}
	if resolved.GlobScanMaxDepth == nil || *resolved.GlobScanMaxDepth != 4 {
		t.Fatalf("glob scan depth = %#v", resolved.GlobScanMaxDepth)
	}
}

// Backends that cannot evaluate glob syntax (Linux mounts) expand deny globs
// into concrete masks while Seatbelt keeps the patterns.
func TestResolveForEnforcementExpandsDenyGlobsForMountBackends(t *testing.T) {
	req := EnforcementRequest{
		CWD:                    "/repo",
		HasFullDiskReadAccess:  true,
		HasFullDiskWriteAccess: false,
		Projection:             ProjectionLinuxMounts,
		Entries: []EnforcementEntry{
			{Access: "write", Path: "/repo"},
			{Access: "deny", Pattern: "/repo/**/.env"},
		},
		ExpandGlob: func(pattern string, maxDepth *int) ([]string, error) {
			if pattern != "/repo/**/.env" {
				t.Fatalf("expand pattern = %q", pattern)
			}
			return []string{"/repo/app/.env", "/repo/.env"}, nil
		},
		DecorateWritableRoots: func(roots []string) []WritableRoot {
			out := make([]WritableRoot, 0, len(roots))
			for _, root := range roots {
				out = append(out, WritableRoot{
					Root:                   root,
					ReadOnlySubpaths:       []string{root + "/.git"},
					ProtectedMetadataNames: []string{".git"},
				})
			}
			return out
		},
	}
	resolved, err := ResolveForEnforcement(req)
	if err != nil {
		t.Fatalf("ResolveForEnforcement() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.UnreadableRoots, []string{"/repo/app/.env", "/repo/.env"}) {
		t.Fatalf("unreadable roots = %#v", resolved.UnreadableRoots)
	}
	if !reflect.DeepEqual(resolved.UnreadableGlobs, []string{"/repo/**/.env"}) {
		t.Fatalf("unreadable globs = %#v", resolved.UnreadableGlobs)
	}
	if len(resolved.WritableRoots) != 1 || resolved.WritableRoots[0].Root != "/repo" {
		t.Fatalf("writable roots = %#v", resolved.WritableRoots)
	}
	if !reflect.DeepEqual(resolved.WritableRoots[0].ReadOnlySubpaths, []string{"/repo/.git"}) {
		t.Fatalf("writable carveouts = %#v", resolved.WritableRoots[0].ReadOnlySubpaths)
	}
}

func TestResolveForEnforcementAppliesProjectionToGrantPaths(t *testing.T) {
	req := EnforcementRequest{
		HasFullDiskReadAccess:  false,
		HasFullDiskWriteAccess: false,
		Projection:             ProjectionNative,
		Entries: []EnforcementEntry{
			{Access: "read", Path: "/var/tmp"},
			{Access: "write", Path: "/var/tmp/work"},
		},
		Project: func(path string) string {
			if path == "/var/tmp" {
				return "/private/var/tmp"
			}
			return "/private" + path
		},
	}
	resolved, err := ResolveForEnforcement(req)
	if err != nil {
		t.Fatalf("ResolveForEnforcement() error = %v", err)
	}
	if !reflect.DeepEqual(resolved.ReadableRoots, []string{"/private/var/tmp", "/private/var/tmp/work"}) {
		t.Fatalf("readable roots = %#v", resolved.ReadableRoots)
	}
	if !reflect.DeepEqual(resolved.WritableRoots, []WritableRoot{{Root: "/private/var/tmp/work"}}) {
		t.Fatalf("writable roots = %#v", resolved.WritableRoots)
	}
}
