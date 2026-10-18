package sandbox

import "testing"

// Mirrors Rust #51843: the integrity runner observes the same file-system
// entries that the launcher enforces, including read, write, deny, and glob
// entries.
func TestRuntimeFileSystemEntriesExposeCanonicalPolicyLikeRust(t *testing.T) {
	raw := `{
		"type":"managed",
		"file_system":{
			"type":"restricted",
			"entries":[
				{"path":{"type":"path","path":"/work"},"access":"read"},
				{"path":{"type":"path","path":"/work/out"},"access":"write"},
				{"path":{"type":"glob_pattern","pattern":"/work/[z-a]"},"access":"deny"},
				{"path":{"type":"special","value":{"kind":"root"}},"access":"write"}
			]
		},
		"network":"restricted"
	}`
	profile, err := ParseRuntimePermissionProfileJSON(raw)
	if err != nil {
		t.Fatalf("ParseRuntimePermissionProfileJSON() error = %v", err)
	}
	entries := profile.RuntimeFileSystemEntries()
	if len(entries) != 4 {
		t.Fatalf("RuntimeFileSystemEntries() = %#v, want 4 entries", entries)
	}
	if entries[2].Path.Type != "glob_pattern" || entries[2].Path.Pattern != "/work/[z-a]" || entries[2].Access != FileSystemAccessDeny {
		t.Fatalf("deny glob entry = %#v", entries[2])
	}
	if entries[3].Path.Value == nil || entries[3].Path.Value.Kind != "root" || entries[3].Access != FileSystemAccessWrite {
		t.Fatalf("special write entry = %#v", entries[3])
	}
}

// A legacy profile without canonical runtime JSON still exposes the entries
// produced by its serialized workspace-write policy.
func TestRuntimeFileSystemEntriesSerializeLegacyProfileLikeRust(t *testing.T) {
	profile := PermissionProfile{SandboxPolicy: NewWorkspaceWritePolicy()}
	entries := profile.RuntimeFileSystemEntries()
	if len(entries) == 0 {
		t.Fatal("RuntimeFileSystemEntries() = nil for a workspace-write profile")
	}
}
