//go:build !windows

package execserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBoundOutputRejectsSymlinkReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last.md")
	victim := filepath.Join(dir, "victim")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(victim, []byte("victim"), 0o600); err != nil {
		t.Fatal(err)
	}
	bound, err := OpenBoundOutput(path)
	if err != nil {
		t.Fatalf("OpenBoundOutput() error = %v", err)
	}
	defer bound.Close()
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(victim, path); err != nil {
		t.Fatal(err)
	}
	if err := bound.Write([]byte("secret")); err == nil {
		t.Fatalf("Write() succeeded after a symlink replaced the path")
	}
	data, err := os.ReadFile(victim)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "victim" {
		t.Fatalf("victim content = %q", data)
	}
}
