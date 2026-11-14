package sandbox

import (
	"codex_go/sandbox/sandboxpath"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestEnforcementKeepsCanonicalReadOnlySubtree(t *testing.T) {
	root := t.TempDir()
	docs := filepath.Join(root, "docs")
	wire := rustPermissionProfileWire{Type: "managed", Network: "restricted", FileSys: &rustPermissionFilesystem{Type: "restricted", Entries: []rustPermissionFilesystemEntry{
		{Path: rustPermissionFilesystemPath{Type: "path", Path: root}, Access: "write"},
		{Path: rustPermissionFilesystemPath{Type: "path", Path: docs}, Access: "read"},
	}}}
	raw, err := json.Marshal(wire)
	if err != nil {
		t.Fatal(err)
	}
	profile, err := ParseRuntimePermissionProfileJSON(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolveFileSystemPermissionsForEnforcement(profile, root, sandboxpath.ProjectionNative)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.HasFullDiskReadAccess || resolved.HasFullDiskWriteAccess {
		t.Fatal("restricted profile became unrestricted")
	}
	if len(resolved.WritableRoots) != 1 {
		t.Fatalf("roots=%+v", resolved.WritableRoots)
	}
	if resolved.WritableRoots[0].IsPathWritable(filepath.Join(docs, "guide.txt")) {
		t.Fatal("read-only subtree became writable")
	}
	if !resolved.WritableRoots[0].IsPathWritable(filepath.Join(root, "ordinary.txt")) {
		t.Fatal("ordinary file not writable")
	}
	_, params, err := buildSeatbeltPolicy(root, profile, nil, "")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, param := range params {
		if strings.HasPrefix(param.Name, "PROTECTED_WRITE_") && param.Value == cleanSeatbeltPath(docs) {
			found = true
		}
	}
	if !found {
		t.Fatal("Seatbelt lost explicit read-only subtree")
	}
}
