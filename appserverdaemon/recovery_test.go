package appserverdaemon

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/daemonrecovery"
)

// TestLifecycleDiscardsPendingRecoveryLikeRust pins the lifecycle points that
// clear the pending recovery snapshot before a planned daemon replacement
// (Rust app-server-daemon lib.rs thread_recovery::discard_pending call sites).
func TestLifecycleDiscardsPendingRecoveryLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	for _, testCase := range []struct {
		name        string
		appRunning  bool
		socketReady bool
		command     LifecycleCommand
	}{
		{"stop", true, true, LifecycleStop},
		{"fresh-start", false, false, LifecycleStart},
		{"restart", true, true, LifecycleRestart},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			stub.appRunning = testCase.appRunning
			stub.socketReady = testCase.socketReady
			stub.updaterRunning = true
			home := t.TempDir()
			daemon := NewDaemonForCodexHome(home, "")
			recovery := daemonrecovery.FilePath(home)
			if err := os.MkdirAll(filepath.Dir(recovery), 0o700); err != nil {
				t.Fatalf("MkdirAll error = %v", err)
			}
			if err := os.WriteFile(recovery, []byte(`{}`), 0o600); err != nil {
				t.Fatalf("WriteFile error = %v", err)
			}
			runner := NewLifecycleRunner(daemon)
			runner.Now = func() time.Time { return fixedDaemonTime() }
			if _, err := runner.Run(testCase.command); err != nil {
				t.Fatalf("Run(%s) error = %v", testCase.command, err)
			}
			if pathExistsNoFollow(recovery) {
				t.Fatalf("Run(%s) left the pending recovery snapshot", testCase.command)
			}
		})
	}
}
