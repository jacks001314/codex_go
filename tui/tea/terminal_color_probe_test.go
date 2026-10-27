package tea

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestProbeTerminalColorsForTUIOnlyRunsForTerminals covers the startup gate: the
// bounded color probe runs only when the TUI owns real terminal stdio, so tests
// and piped sessions are never touched.
func TestProbeTerminalColorsForTUIOnlyRunsForTerminals(t *testing.T) {
	// Non-file readers/writers are skipped (no panic, no probe).
	if got := probeTerminalColorsForTUI(nil, nil); got != nil {
		t.Fatalf("probe of nil stdio returned %T, want nil", got)
	}
	reader := strings.NewReader("x")
	if got := probeTerminalColorsForTUI(reader, os.Stdout); got != io.Reader(reader) {
		t.Fatalf("probe of non-file input wrapped it as %T", got)
	}

	// Regular files are not terminals, so the probe is skipped even on hosts
	// that implement it.
	dir := t.TempDir()
	path := filepath.Join(dir, "in.txt")
	if err := os.WriteFile(path, []byte("input"), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open input: %v", err)
	}
	defer file.Close()
	outPath := filepath.Join(dir, "out.txt")
	out, err := os.Create(outPath)
	if err != nil {
		t.Fatalf("create output: %v", err)
	}
	defer out.Close()
	if got := probeTerminalColorsForTUI(file, out); got != io.Reader(file) {
		t.Fatalf("probe of regular files wrapped the input as %T", got)
	}
	if data, err := os.ReadFile(outPath); err != nil || len(data) != 0 {
		t.Fatalf("piped output = %q (err=%v), want no probe bytes", data, err)
	}
}
