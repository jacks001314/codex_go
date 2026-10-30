//go:build unix

package appserver

import (
	osexec "os/exec"
	"syscall"
	"testing"
)

// TestPrepareCommandExecProcessUsesRustNewSessionLikeRust pins Rust #47605 for
// the app-server pipe carrier: command/exec children start a new session
// instead of remaining in the server's process group.
func TestPrepareCommandExecProcessUsesRustNewSessionLikeRust(t *testing.T) {
	cmd := osexec.Command("sh", "-c", "true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: false}
	prepareCommandExecProcess(cmd)
	if cmd.SysProcAttr == nil || !cmd.SysProcAttr.Setsid {
		t.Fatalf("SysProcAttr = %#v, want Setsid", cmd.SysProcAttr)
	}
}

func TestPrepareCommandExecProcessIgnoresNilLikeRust(t *testing.T) {
	prepareCommandExecProcess(nil)
}
