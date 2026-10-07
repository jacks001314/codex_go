//go:build linux

package linuxsandbox

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"codex_go/sandbox/sandboxpath"
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
// preferred_bwrap_launcher). The system candidate must survive the command's own
// filesystem policy, so a bwrap the sandbox would let the command replace is
// never probed or executed before confinement (Rust #51211).
func resolveBwrapLauncher(profile *linuxPermissionProfile, sandboxPolicyCWD string) bwrapLauncher {
	return selectBwrapLauncherForPolicy(
		preSandboxFilesystemPolicy(profile, sandboxPolicyCWD),
		os.Getenv("PATH"),
		processWorkingDirectory(),
		probeBwrapHelp,
		bundledBwrapResource,
	)
}

// preSandboxFilesystemPolicy mirrors the filesystem-policy view Rust
// `FileSystemSandboxPolicy` hands the pre-sandbox PATH filter.
func preSandboxFilesystemPolicy(profile *linuxPermissionProfile, sandboxPolicyCWD string) sandboxpath.FilesystemPolicy {
	if profile == nil || profile.Filesystem == nil {
		return sandboxpath.FilesystemPolicy{}
	}
	filesystem := profile.Filesystem
	if filesystem.hasFullDiskWriteAccess() {
		// Full-disk policies intentionally enumerate no writable roots.
		return sandboxpath.FilesystemPolicy{FullDiskWriteAccess: true}
	}
	roots := sandboxpath.WritableRootsWithProtectedSubpaths(filesystem.writableRoots(sandboxPolicyCWD))
	// An explicit read entry inside a writable root is a read-only carveout too
	// (Rust `WritableRoot::read_only_subpaths`), and it still cannot hide a
	// replaceable parent from the ancestor walk.
	carveouts := filesystem.protectedReadOnlySubpaths(sandboxPolicyCWD)
	for index := range roots {
		for _, carveout := range carveouts {
			if pathWithinRoot(carveout, roots[index].Root) && !containsPath(roots[index].ReadOnlySubpaths, carveout) {
				roots[index].ReadOnlySubpaths = append(roots[index].ReadOnlySubpaths, carveout)
			}
		}
	}
	return sandboxpath.FilesystemPolicy{WritableRoots: roots}
}

func pathWithinRoot(path string, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func containsPath(paths []string, path string) bool {
	for _, candidate := range paths {
		if candidate == path {
			return true
		}
	}
	return false
}

func processWorkingDirectory() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return cwd
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
