package elevated

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPipePairUsesRunnerPrefix(t *testing.T) {
	in, out := PipePair()
	if !strings.HasPrefix(in, `\\.\pipe\codex-runner-`) || !strings.HasSuffix(in, "-in") {
		t.Fatalf("in pipe = %q", in)
	}
	if !strings.HasPrefix(out, `\\.\pipe\codex-runner-`) || !strings.HasSuffix(out, "-out") {
		t.Fatalf("out pipe = %q", out)
	}
	if in == out {
		t.Fatalf("PipePair returned identical names: %q", in)
	}
}

func TestFindRunnerExeFallsBackToCommandRunnerName(t *testing.T) {
	got := FindRunnerExe(t.TempDir(), filepath.Join(t.TempDir(), "codex.exe"))
	if filepath.Base(got) != "codex-command-runner.exe" {
		t.Fatalf("FindRunnerExe() = %q", got)
	}
}

func TestFindRunnerExeMaterializesSingleFileCLI(t *testing.T) {
	home := t.TempDir()
	currentExe := filepath.Join(t.TempDir(), "codex-go.exe")
	if err := os.WriteFile(currentExe, []byte("single-file-cli"), 0o700); err != nil {
		t.Fatalf("WriteFile(current exe) error = %v", err)
	}

	got := FindRunnerExe(home, currentExe)
	if filepath.Dir(got) != filepath.Join(home, sandboxBinDirname) || filepath.Ext(got) != ".exe" {
		t.Fatalf("FindRunnerExe() = %q", got)
	}
	data, err := os.ReadFile(got)
	if err != nil {
		t.Fatalf("ReadFile(materialized runner) error = %v", err)
	}
	if string(data) != "single-file-cli" {
		t.Fatalf("materialized runner = %q", data)
	}
}
