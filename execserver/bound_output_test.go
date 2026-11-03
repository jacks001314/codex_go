package execserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBoundOutputWritesBoundFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "last.md")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	bound, err := OpenBoundOutput(path)
	if err != nil {
		t.Fatalf("OpenBoundOutput() error = %v", err)
	}
	if data, err := os.ReadFile(path); err != nil || string(data) != "old" {
		t.Fatalf("open truncated output: %q, %v", data, err)
	}
	if err := bound.Write([]byte("new message")); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := bound.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "new message" {
		t.Fatalf("content = %q", data)
	}
}

func TestBoundOutputRejectsReplacedPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "last.md")
	if err := os.WriteFile(path, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	bound, err := OpenBoundOutput(path)
	if err != nil {
		t.Fatalf("OpenBoundOutput() error = %v", err)
	}
	defer bound.Close()
	if err := os.Remove(path); err != nil {
		t.Fatalf("remove bound path: %v", err)
	}
	if err := os.WriteFile(path, []byte("replacement"), 0o600); err != nil {
		t.Fatalf("replace bound path: %v", err)
	}
	if err := bound.Write([]byte("secret")); err == nil {
		t.Fatalf("Write() succeeded after the path was replaced")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "replacement" {
		t.Fatalf("replacement content = %q", data)
	}
}
