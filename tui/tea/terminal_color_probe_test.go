package tea

import (
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
	probeTerminalColorsForTUI(nil, nil)
	probeTerminalColorsForTUI(strings.NewReader("x"), os.Stdout)

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
	probeTerminalColorsForTUI(file, out)
	if data, err := os.ReadFile(outPath); err != nil || len(data) != 0 {
		t.Fatalf("piped output = %q (err=%v), want no probe bytes", data, err)
	}
}
