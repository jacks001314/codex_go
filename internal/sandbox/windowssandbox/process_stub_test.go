//go:build !windows

package windowssandbox

import (
	"strings"
	"testing"
)

func TestProcessReportsExplicitUnsupportedOffWindows(t *testing.T) {
	if _, err := CreateProcessAsUserWithToken(ProcessSpawnRequest{}); !IsUnsupported(err) || !strings.Contains(err.Error(), "process.create_process_as_user") {
		t.Fatalf("CreateProcessAsUserWithToken() error = %v, want explicit unsupported", err)
	}
	if _, err := SpawnProcessWithPipesWithToken(PipeSpawnRequest{}); !IsUnsupported(err) || !strings.Contains(err.Error(), "process.spawn_process_with_pipes") {
		t.Fatalf("SpawnProcessWithPipesWithToken() error = %v, want explicit unsupported", err)
	}
	if _, err := ReadHandleLoop(0, nil); !IsUnsupported(err) || !strings.Contains(err.Error(), "process.read_handle_loop") {
		t.Fatalf("ReadHandleLoop() error = %v, want explicit unsupported", err)
	}
}
