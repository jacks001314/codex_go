//go:build !windows

package elevated

import (
	"strings"
	"testing"
)

func TestRunnerPipeReportsExplicitUnsupportedOffWindows(t *testing.T) {
	if _, err := CurrentUsername(); err == nil || !strings.Contains(err.Error(), "elevated.runner_pipe.current_username") {
		t.Fatalf("CurrentUsername() error = %v, want explicit unsupported", err)
	}
	if _, err := CreateNamedPipe(`\\.\pipe\codex-test`, PipeAccessInbound, "user"); err == nil || !strings.Contains(err.Error(), "elevated.runner_pipe.create_named_pipe") {
		t.Fatalf("CreateNamedPipe() error = %v, want explicit unsupported", err)
	}
	if err := ConnectPipeHandle(1, 0); err == nil || !strings.Contains(err.Error(), "elevated.runner_pipe.connect_pipe") {
		t.Fatalf("ConnectPipeHandle() error = %v, want explicit unsupported", err)
	}
}
