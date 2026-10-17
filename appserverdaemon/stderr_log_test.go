package appserverdaemon

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPreserveStderrLogKeepsBoundedTailLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.stderr.log")
	contents := bytes.Repeat([]byte("x"), maxPreviousStderrLogBytes+4096)
	copy(contents[len(contents)-4:], "tail")
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preserveStderrLog(path); err != nil {
		t.Fatalf("preserveStderrLog: %v", err)
	}
	previous, err := os.ReadFile(path + ".previous")
	if err != nil {
		t.Fatal(err)
	}
	if len(previous) != maxPreviousStderrLogBytes {
		t.Fatalf("previous bytes = %d, want %d", len(previous), maxPreviousStderrLogBytes)
	}
	if !bytes.HasSuffix(previous, []byte("tail")) {
		t.Fatalf("previous tail = %q", previous[len(previous)-8:])
	}
	original, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(original, contents) {
		t.Fatalf("preserve modified the current log")
	}
}

func TestPreserveStderrLogSkipsMissingEmptyAndNonregularLikeRust(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.stderr.log")
	if err := preserveStderrLog(missing); err != nil {
		t.Fatalf("missing: %v", err)
	}
	empty := filepath.Join(dir, "empty.stderr.log")
	if err := os.WriteFile(empty, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := preserveStderrLog(empty); err != nil {
		t.Fatalf("empty: %v", err)
	}
	nonregular := filepath.Join(dir, "directory.stderr.log")
	if err := os.Mkdir(nonregular, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := preserveStderrLog(nonregular); err != nil {
		t.Fatalf("nonregular: %v", err)
	}
	for _, path := range []string{missing, empty, nonregular} {
		if _, err := os.Stat(path + ".previous"); !os.IsNotExist(err) {
			t.Fatalf("%s produced a previous snapshot", path)
		}
	}
}
