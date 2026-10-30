//go:build !windows

package execserver

import (
	"context"
	"testing"

	"golang.org/x/sys/unix"
)

// TestExecServerPipeProcessLeadsOwnSessionLikeRust mirrors Rust #47605 for the
// exec-server pipe carrier: process/start children run through the shared child
// launcher's NewSession mode, so their session and process group are their own.
func TestExecServerPipeProcessLeadsOwnSessionLikeRust(t *testing.T) {
	server := NewServer()
	if _, err := server.startProcess(context.Background(), &ExecParams{
		ProcessID: "pipe-session",
		Argv:      []string{"sh", "-c", "sleep 30"},
		EnvPolicy: &ExecEnvPolicy{Inherit: "all", IgnoreDefaultExcludes: true},
		Env:       map[string]string{},
	}); err != nil {
		t.Fatalf("startProcess() error = %v", err)
	}
	state := server.lookup("pipe-session")
	if state == nil {
		t.Fatal("process state missing")
	}
	state.mu.Lock()
	cmd := state.cmd
	state.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		t.Fatalf("process command = %#v", cmd)
	}
	pid := cmd.Process.Pid
	sid, err := unix.Getsid(pid)
	if err != nil {
		t.Fatalf("Getsid(%d) error = %v", pid, err)
	}
	if sid != pid {
		t.Fatalf("session id = %d, want child pid %d", sid, pid)
	}
	pgid, err := unix.Getpgid(pid)
	if err != nil {
		t.Fatalf("Getpgid(%d) error = %v", pid, err)
	}
	if pgid != pid {
		t.Fatalf("process group = %d, want child pid %d", pgid, pid)
	}
	_ = state.terminate()
}
