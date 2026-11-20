package appserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #62653: fs/getMetadata reports a regular file's size and answers an
// explicit null for anything that is not a regular file, so a client can tell an
// empty file (0) from an unavailable size (null).
func TestGetMetadataReportsRegularFileSizeLikeRust(t *testing.T) {
	service := NewFSService()
	root := t.TempDir()
	file := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(file, []byte("alpha"), 0o600); err != nil {
		t.Fatalf("WriteFile fixture error = %v", err)
	}

	metadata, err := service.GetMetadata(&GetMetadataParams{Path: file})
	if err != nil {
		t.Fatalf("GetMetadata(file) error = %v", err)
	}
	if metadata.SizeBytes == nil || *metadata.SizeBytes != 5 {
		t.Fatalf("file sizeBytes = %v, want 5", metadata.SizeBytes)
	}
	encoded, err := json.Marshal(metadata)
	if err != nil {
		t.Fatalf("Marshal(file metadata) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"sizeBytes":5`) {
		t.Fatalf("file metadata JSON = %s, want the size", encoded)
	}

	directory, err := service.GetMetadata(&GetMetadataParams{Path: root})
	if err != nil {
		t.Fatalf("GetMetadata(directory) error = %v", err)
	}
	if directory.SizeBytes != nil {
		t.Fatalf("directory sizeBytes = %v, want null", *directory.SizeBytes)
	}
	encoded, err = json.Marshal(directory)
	if err != nil {
		t.Fatalf("Marshal(directory metadata) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"sizeBytes":null`) {
		t.Fatalf("directory metadata JSON = %s, want an explicit null", encoded)
	}

}

// The size follows the resolved target, so a symlink to a regular file reports the
// file's size rather than the link's own length.
func TestGetMetadataReportsSymlinkTargetSizeLikeRust(t *testing.T) {
	service := NewFSService()
	root := t.TempDir()
	file := filepath.Join(root, "notes.txt")
	if err := os.WriteFile(file, []byte("alpha"), 0o600); err != nil {
		t.Fatalf("WriteFile fixture error = %v", err)
	}
	link := filepath.Join(root, "notes-link.txt")
	if err := os.Symlink(file, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	linked, err := service.GetMetadata(&GetMetadataParams{Path: link})
	if err != nil {
		t.Fatalf("GetMetadata(symlink) error = %v", err)
	}
	if linked.SizeBytes == nil || *linked.SizeBytes != 5 {
		t.Fatalf("symlink sizeBytes = %v, want the target's size", linked.SizeBytes)
	}
	if !linked.IsSymlink || !linked.IsFile {
		t.Fatalf("symlink metadata = %+v, want a file symlink", linked)
	}
}

// An older server omits sizeBytes, which must decode as unavailable rather than
// as a zero-length file.
func TestGetMetadataDecodesMissingSizeLikeRust(t *testing.T) {
	var response GetMetadataResponse
	if err := json.Unmarshal([]byte(`{"isDirectory":false,"isFile":true,"isSymlink":false}`), &response); err != nil {
		t.Fatalf("Unmarshal(older metadata) error = %v", err)
	}
	if response.SizeBytes != nil {
		t.Fatalf("sizeBytes = %v, want nil for a server that omits it", *response.SizeBytes)
	}
	encoded, err := json.Marshal(&GetMetadataResponse{})
	if err != nil {
		t.Fatalf("Marshal(GetMetadataResponse) error = %v", err)
	}
	if !strings.Contains(string(encoded), `"sizeBytes":null`) {
		t.Fatalf("empty metadata JSON = %s, want an explicit null", encoded)
	}
}
