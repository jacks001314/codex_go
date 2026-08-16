//go:build darwin

package processhardening

import (
	"fmt"
	"os"
	"syscall"

	"golang.org/x/sys/unix"
)

// ApplyPreMain performs the macOS hardening before the CLI dispatch,
// mirroring Rust's pre-main hardening.
func ApplyPreMain() {
	applyDarwin()
}

// applyDarwin prevents debugger attach, disables core dumps, and clears DYLD_*
// loader subversion, mirroring Rust pre_main_hardening_macos.
func applyDarwin() {
	ret, _, errno := syscall.Syscall6(uintptr(syscall.SYS_PTRACE), uintptr(unix.PT_DENY_ATTACH), 0, 0, 0, 0, 0)
	if errno != 0 || ret == ^uintptr(0) {
		fmt.Fprintf(os.Stderr, "ERROR: ptrace(PT_DENY_ATTACH) failed: %v\n", errno)
		os.Exit(ptraceDenyAttachExitCode)
	}
	setCoreFileSizeLimitToZero()
	removeEnvVarsWithPrefix("DYLD_")
}

func setRlimitCoreZero() error {
	return unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
