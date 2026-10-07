//go:build windows

package execserver

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors the invariants of Rust #51511
// (`relative_volume_opens_files_and_rejects_junctions`, plus its drive-root
// case) for the Go implementation.
//
// Go has no volume-root fallback because it never performs the strict NT open
// that motivated it: `openRegularFileForRead`/`openRegularFileForWrite` call
// Win32 `CreateFile` on the plain DOS path (never the `\??\C:\` alias) with
// `FILE_FLAG_OPEN_REPARSE_POINT`, and every path component is walked by
// `noFollowSymlinkComponent`. The invariants the Rust fix protects are frozen
// here: ordinary drive-letter paths keep working, the drive root is not treated
// as a reparse component, and a junction is never traversed or created through.
func TestDriveLetterPathsAreNotReparseRejectedAndJunctionsStayBlocked(t *testing.T) {
	follow := false
	dir := t.TempDir()
	real := filepath.Join(dir, "real")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatalf("MkdirAll(%q) error = %v", real, err)
	}
	existing := filepath.Join(real, "existing.txt")
	if err := os.WriteFile(existing, []byte("original"), 0o600); err != nil {
		t.Fatalf("WriteFile(%q) error = %v", existing, err)
	}

	// Drive-letter opens must not be rejected as reparse points.
	response, err := readFile(&FSReadFileParams{Path: existing, FollowSymlinks: &follow})
	if err != nil {
		t.Fatalf("readFile(drive-letter path %q) error = %v", existing, err)
	}
	data, err := base64.StdEncoding.DecodeString(response.DataBase64)
	if err != nil || string(data) != "original" {
		t.Fatalf("readFile contents = %q, %v; want %q", data, err, "original")
	}

	// The DOS drive alias itself must not be reported as a reparse component.
	root := filepath.VolumeName(existing) + string(os.PathSeparator)
	if link, err := noFollowSymlinkComponent(root); err != nil || link != "" {
		t.Fatalf("noFollowSymlinkComponent(%q) = %q, %v; want no link", root, link, err)
	}
	if link, err := noFollowSymlinkComponent(existing); err != nil || link != "" {
		t.Fatalf("noFollowSymlinkComponent(%q) = %q, %v; want no link", existing, link, err)
	}

	junctionParent := t.TempDir()
	junction := filepath.Join(junctionParent, "junction")
	output, err := exec.Command("cmd", "/C", "mklink", "/J", junction, real).CombinedOutput()
	if err != nil {
		t.Skipf("mklink /J unavailable: %v (%s)", err, strings.TrimSpace(string(output)))
	}

	// A junction must never be traversed: neither for reads, nor for opens, nor
	// for creation of new files below it.
	if _, err := readFile(&FSReadFileParams{Path: filepath.Join(junction, "existing.txt"), FollowSymlinks: &follow}); err == nil {
		t.Fatal("readFile through a junction succeeded, want rejection")
	}
	server := NewServer()
	if _, err := server.openFile(&FSOpenParams{HandleID: "junction", Path: filepath.Join(junction, "existing.txt"), FollowSymlinks: &follow}); err == nil {
		t.Fatal("openFile through a junction succeeded, want rejection")
	}
	if _, err := writeFile(&FSWriteFileParams{
		Path:           filepath.Join(junction, "blocked.txt"),
		DataBase64:     base64.StdEncoding.EncodeToString([]byte("blocked")),
		FollowSymlinks: &follow,
	}); err == nil {
		t.Fatal("writeFile through a junction succeeded, want rejection")
	}
	if _, err := os.Stat(filepath.Join(real, "blocked.txt")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("junction traversal created blocked.txt: %v", err)
	}

	// The junction is itself a final-component reparse point.
	if _, err := readFile(&FSReadFileParams{Path: junction, FollowSymlinks: &follow}); err == nil {
		t.Fatal("readFile(junction) succeeded, want rejection")
	}
}
