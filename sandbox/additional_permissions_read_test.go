package sandbox

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// Rust parity: the additional-permission profile keeps read and write roots
// apart, which the runtime's internal shell-snapshot grant needs (a read grant
// must not become a write grant).
func TestAdditionalPermissionProfileKeepsReadAndWriteRootsLikeRust(t *testing.T) {
	readRoot := filepath.Join(t.TempDir(), "snapshot")
	writeRoot := filepath.Join(t.TempDir(), "output")
	profile := AdditionalPermissionProfile{
		FileSystem:     []string{writeRoot},
		ReadFileSystem: []string{readRoot},
	}
	encoded, err := json.Marshal(&profile)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var wire struct {
		FileSystem struct {
			Read  []string `json:"read"`
			Write []string `json:"write"`
		} `json:"fileSystem"`
	}
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("Unmarshal(%s) error = %v", encoded, err)
	}
	if len(wire.FileSystem.Read) != 1 || wire.FileSystem.Read[0] != readRoot {
		t.Fatalf("fileSystem.read = %#v, want the read root", wire.FileSystem.Read)
	}
	if len(wire.FileSystem.Write) != 1 || wire.FileSystem.Write[0] != writeRoot {
		t.Fatalf("fileSystem.write = %#v, want the write root", wire.FileSystem.Write)
	}

	// A round trip preserves the split, and a read-only profile is not empty.
	var decoded AdditionalPermissionProfile
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("round trip error = %v", err)
	}
	if len(decoded.ReadFileSystem) != 1 || decoded.ReadFileSystem[0] != readRoot ||
		len(decoded.FileSystem) != 1 || decoded.FileSystem[0] != writeRoot {
		t.Fatalf("round-tripped profile = %#v", decoded)
	}
	readOnly := &AdditionalPermissionProfile{ReadFileSystem: []string{readRoot}}
	if readOnly.IsEmpty() {
		t.Fatal("a read-only profile reports empty")
	}
	if !(&AdditionalPermissionProfile{}).IsEmpty() {
		t.Fatal("an empty profile reports permissions")
	}

	// Normalization and merging keep the split and de-duplicate each side.
	normalized, err := NormalizeAdditionalPermissions(AdditionalPermissionProfile{
		FileSystem:     []string{"relative-output"},
		ReadFileSystem: []string{"relative-snapshot"},
	}, t.TempDir())
	if err != nil {
		t.Fatalf("NormalizeAdditionalPermissions() error = %v", err)
	}
	if len(normalized.FileSystem) != 1 || !filepath.IsAbs(normalized.FileSystem[0]) {
		t.Fatalf("normalized write roots = %#v", normalized.FileSystem)
	}
	if len(normalized.ReadFileSystem) != 1 || !filepath.IsAbs(normalized.ReadFileSystem[0]) {
		t.Fatalf("normalized read roots = %#v", normalized.ReadFileSystem)
	}
	merged := MergePermissionProfiles(&AdditionalPermissionProfile{ReadFileSystem: []string{readRoot}}, &AdditionalPermissionProfile{ReadFileSystem: []string{readRoot}, FileSystem: []string{writeRoot}})
	if merged == nil || len(merged.ReadFileSystem) != 1 || len(merged.FileSystem) != 1 {
		t.Fatalf("merged profile = %#v", merged)
	}
}
