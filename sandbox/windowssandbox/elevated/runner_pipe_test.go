package elevated

import (
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
