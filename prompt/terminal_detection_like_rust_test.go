package prompt

import (
	"os"
	"path/filepath"
	"testing"
)

func TestIsTerminalReaderRejectsRegularFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "input.txt")
	if err := os.WriteFile(path, []byte("hi"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	defer file.Close()

	if IsTerminalReader(file) {
		t.Fatal("regular file reported as a terminal")
	}
}

func TestIsTerminalReaderPrefersIsTerminalMethod(t *testing.T) {
	if !IsTerminalReader(terminalReader{}) {
		t.Fatal("wrapper with IsTerminal() must be trusted")
	}
	if IsTerminalReader(nil) {
		t.Fatal("nil reader reported as a terminal")
	}
}
