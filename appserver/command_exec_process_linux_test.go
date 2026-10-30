//go:build linux

package appserver

import (
	osexec "os/exec"
	"syscall"
	"testing"
)

// TestPrepareCommandExecProcessAddsParentDeathSignalLikeRust covers the Linux
// half of ChildCommand::terminate_on_parent_death for command/exec children.
func TestPrepareCommandExecProcessAddsParentDeathSignalLikeRust(t *testing.T) {
	cmd := osexec.Command("sh", "-c", "true")
	prepareCommandExecProcess(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Pdeathsig != syscall.SIGTERM {
		t.Fatalf("SysProcAttr = %#v, want Pdeathsig SIGTERM", cmd.SysProcAttr)
	}
}
