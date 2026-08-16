//go:build freebsd || openbsd

package processhardening

import "golang.org/x/sys/unix"

// ApplyPreMain performs the BSD hardening before the CLI dispatch, mirroring
// Rust's pre-main hardening.
func ApplyPreMain() {
	applyBSD()
}

// applyBSD disables core dumps and clears LD_* loader subversion, mirroring
// Rust pre_main_hardening_bsd.
func applyBSD() {
	setCoreFileSizeLimitToZero()
	removeEnvVarsWithPrefix("LD_")
}

func setRlimitCoreZero() error {
	return unix.Setrlimit(unix.RLIMIT_CORE, &unix.Rlimit{Cur: 0, Max: 0})
}
