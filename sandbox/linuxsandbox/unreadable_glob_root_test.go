package linuxsandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Mirrors Rust #57186: absent and non-directory search roots have no matches,
// directory and symlinked-directory roots are scanned, and any other metadata
// failure rejects the expansion instead of dropping a deny rule.
func TestInspectUnreadableGlobSearchRootLikeRust(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "file")
	if err := os.WriteFile(file, []byte("ordinary file"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	for _, tc := range []struct {
		name  string
		root  string
		usage bool
	}{
		{name: "directory", root: dir, usage: true},
		{name: "missing", root: filepath.Join(dir, "missing")},
		{name: "file", root: file},
		{name: "below file", root: filepath.Join(file, "child")},
	} {
		usable, err := inspectUnreadableGlobSearchRoot(tc.root)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", tc.name, err)
		}
		if usable != tc.usage {
			t.Fatalf("%s: usable = %t, want %t", tc.name, usable, tc.usage)
		}
	}

}

func TestInspectUnreadableGlobSearchRootFollowsDirectorySymlinks(t *testing.T) {
	dir := t.TempDir()
	link := filepath.Join(dir, "link")
	if err := os.Symlink(dir, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	usable, err := inspectUnreadableGlobSearchRoot(link)
	if err != nil || !usable {
		t.Fatalf("symlinked directory usable = %t, err = %v", usable, err)
	}
}

func TestInspectUnreadableGlobSearchRootFailsOnMetadataErrorLikeRust(t *testing.T) {
	dir := t.TempDir()
	loop := filepath.Join(dir, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	_, err := inspectUnreadableGlobSearchRoot(loop)
	if err == nil {
		t.Fatal("symlink loop root must fail closed")
	}
	if !strings.Contains(err.Error(), loop) {
		t.Fatalf("error = %v, want the search root", err)
	}
	if !strings.Contains(err.Error(), "too many levels of symbolic links") {
		t.Fatalf("error = %v, want the underlying metadata error", err)
	}
}
