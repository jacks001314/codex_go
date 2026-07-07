//go:build !windows

package elevated

import (
	"strings"
	"testing"
)

func TestRunnerClientReportsExplicitUnsupportedOffWindows(t *testing.T) {
	if _, err := ConnectRunner(`\\.\pipe\codex-test`); err == nil || !strings.Contains(err.Error(), "elevated.runner_client.connect") {
		t.Fatalf("ConnectRunner() error = %v, want explicit unsupported", err)
	}
	if _, err := SpawnRunnerTransport(t.TempDir(), t.TempDir(), &SandboxCredentials{}, "", SpawnRequest{}); err == nil || !strings.Contains(err.Error(), "elevated.runner_client.spawn_runner_transport") {
		t.Fatalf("SpawnRunnerTransport() error = %v, want explicit unsupported", err)
	}
}
