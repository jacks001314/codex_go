//go:build linux

package processgroup

import (
	osexec "os/exec"
	"syscall"
	"testing"
)

// TestPrepareChildSessionAddsParentDeathSignalLikeRust covers
// ChildCommand::terminate_on_parent_death: the Linux pipe child receives SIGTERM
// when the spawning process exits.
func TestPrepareChildSessionAddsParentDeathSignalLikeRust(t *testing.T) {
	cmd := osexec.Command("sh", "-c", "true")
	PrepareChildSession(cmd)
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Pdeathsig != syscall.SIGTERM {
		t.Fatalf("SysProcAttr = %#v, want Pdeathsig SIGTERM", cmd.SysProcAttr)
	}
}
