package execserver

import (
	"fmt"
	"io"
	"os"
	"strings"
	"testing"
)

// shellSnapshotReplayState returns the replayed snapshot text. Descriptor replay
// keeps the state in the per-launch unnamed file (Rust #48078); environment
// replay chunks it into __CODEX_SHELL_SNAPSHOT_STATE_* variables.
func shellSnapshotReplayState(t *testing.T, rewritten *ExecParams, reader *os.File) string {
	t.Helper()
	if reader != nil {
		defer reader.Close()
		content, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("read snapshot transport: %v", err)
		}
		return string(content)
	}
	var builder strings.Builder
	for index := 0; ; index++ {
		chunk, ok := rewritten.Env[fmt.Sprintf("__CODEX_SHELL_SNAPSHOT_STATE_%d", index)]
		if !ok {
			return builder.String()
		}
		builder.WriteString(chunk)
	}
}

// shellSnapshotReplaysThroughState reports whether the launch script restores the
// snapshot from the environment chunks or from the inherited descriptor.
func shellSnapshotReplaysThroughState(script string) bool {
	return strings.Contains(script, "unset __CODEX_SHELL_SNAPSHOT_STATE_0")
}
