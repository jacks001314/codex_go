//go:build linux || android

package processhardening

import (
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// ApplyPreMain performs the Linux hardening before the CLI dispatch,
// mirroring Rust's pre-main hardening.
func ApplyPreMain() {
	applyLinux()
}

// applyLinux disables ptrace attach (non-dumpable), core dumps, and LD_*
// loader subversion, mirroring Rust pre_main_hardening_linux.
func applyLinux() {
	if _, err := unix.PrctlRetInt(unix.PR_SET_DUMPABLE, 0, 0, 0, 0); err != nil {
		fmt.Fprintf(os.Stderr, "ERROR: prctl(PR_SET_DUMPABLE, 0) failed: %v\n", err)
		os.Exit(prctlFailedExitCode)
	}
	setCoreFileSizeLimitToZero()
	removeEnvVarsWithPrefix("LD_")
}

func setRlimitCoreZero() error {
	return unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
