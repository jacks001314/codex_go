//go:build linux

package linuxsandbox

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/unix"
)

// probeBwrapHelp reads a launcher's `--help` output and reports which
// capabilities it advertises (Rust system_bwrap_capabilities).
func probeBwrapHelp(path string) (bwrapCapabilities, bool) {
	output, err := exec.Command(path, "--help").CombinedOutput()
	if err != nil && len(output) == 0 {
		return bwrapCapabilities{}, false
	}
	return parseBwrapHelp(string(output))
}

// resolveBwrapLauncher picks the launcher the sandbox runs (Rust
// preferred_bwrap_launcher).
func resolveBwrapLauncher() bwrapLauncher {
	return selectBwrapLauncher(exec.LookPath, probeBwrapHelp, bundledBwrapResource)
}

// makeFilesInheritable clears close-on-exec so bubblewrap can use the file
// descriptors the argv refers to (Rust exec_util::make_files_inheritable).
func makeFilesInheritable(fds []int) {
	for _, fd := range fds {
		if fd < 0 {
			continue
		}
		_, _ = unix.FcntlInt(uintptr(fd), unix.F_SETFD, 0)
	}
}

// execBundledBwrap runs the packaged launcher after checking the digest the
// release pinned, through /proc/self/fd so a replaced file cannot change what
// executes (Rust bundled_bwrap::BundledBwrapLauncher::exec).
func execBundledBwrap(path string, argv []string, env []string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open bundled bubblewrap %s: %w", path, err)
	}
	defer file.Close()
	if err := verifyBundledBwrapDigest(path, bundledBwrapSHA256); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(BundledBwrapDigestFailureExitCode)
	}
	fdPath := fmt.Sprintf("/proc/self/fd/%d", file.Fd())
	if err := unix.Exec(fdPath, argv, env); err != nil {
		return fmt.Errorf("failed to exec bundled bubblewrap %s via %s: %w", path, fdPath, err)
	}
	return nil
}

// bwrapUnavailableError mirrors Rust's panic text when neither launcher works.
func bwrapUnavailableError() error {
	return fmt.Errorf("%s", strings.TrimSpace(BwrapUnavailableMessage))
}
