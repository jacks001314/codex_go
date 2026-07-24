package command_runner

import (
	"bytes"
	"runtime"
	"strings"
	"testing"
)

func TestRunReportsExplicitUnsupported(t *testing.T) {
	var stderr bytes.Buffer
	code := Run(nil, nil, nil, &stderr)
	if code == 0 {
		t.Fatalf("Run() code = 0, want failure")
	}
	want := "bin.command_runner.win.run"
	if runtime.GOOS == "windows" {
		want = "runner: no pipe-in provided"
	}
	if !strings.Contains(stderr.String(), want) {
		t.Fatalf("stderr = %q, want %q", stderr.String(), want)
	}
}
