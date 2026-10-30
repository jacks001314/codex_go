//go:build unix

package processgroup

import (
	osexec "os/exec"
	"syscall"
	"testing"
)

// TestPrepareChildSessionDetachesLikeRustNewSession pins the shared pipe
// launcher settings Rust #47605 applies through ChildCommand: a new session so
// the child no longer inherits the controlling TTY, with existing attributes
// preserved.
func TestPrepareChildSessionDetachesLikeRustNewSession(t *testing.T) {
	cmd := osexec.Command("sh", "-c", "true")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: false}
	PrepareChildSession(cmd)
	if cmd.SysProcAttr == nil {
		t.Fatal("PrepareChildSession cleared SysProcAttr")
	}
	if !cmd.SysProcAttr.Setsid {
		t.Fatalf("SysProcAttr = %#v, want Setsid", cmd.SysProcAttr)
	}
	if cmd.SysProcAttr.Setpgid {
		t.Fatalf("SysProcAttr = %#v, want existing Setpgid value preserved", cmd.SysProcAttr)
	}
}

func TestPrepareChildSessionIgnoresNilCommandLikeRust(t *testing.T) {
	PrepareChildSession(nil)
}
