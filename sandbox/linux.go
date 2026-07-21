package sandbox

import (
	"os/exec"
	"strings"

	"codex_go/utils"
)

// SystemBwrapWarning checks if bubblewrap can be used on the current system
// and returns a warning message if there are known issues, matching Rust's
// system_bwrap_warning behavior.
func SystemBwrapWarning() string {
	if utils.IsWSL1() {
		return "Claude Code's sandboxing is not available on WSL1, which lacks support for user namespaces. " +
			"Please upgrade to WSL2 or use a native Linux environment to enable sandboxed tool execution."
	}

	bwrapPath, err := exec.LookPath("bwrap")
	if err != nil {
		return "bubblewrap is unavailable: no system bwrap was found on PATH"
	}

	if !systemBwrapHasUserNamespaceAccess(bwrapPath) {
		return "bubblewrap cannot create user namespaces. This is often due to a system security " +
			"policy. You may need to enable unprivileged user namespaces or run in a different environment."
	}

	return ""
}

// systemBwrapHasUserNamespaceAccess probes whether bwrap can create user namespaces
// by attempting a minimal sandboxed command.
func systemBwrapHasUserNamespaceAccess(bwrapPath string) bool {
	cmd := exec.Command(bwrapPath,
		"--unshare-user",
		"--unshare-net",
		"--ro-bind", "/", "/",
		"/bin/true")

	output, err := cmd.CombinedOutput()
	if err == nil {
		return true
	}

	// Check if the error is a user namespace failure
	return !isUserNamespaceFailure(string(output))
}

// isUserNamespaceFailure checks if the error output indicates a user namespace issue.
func isUserNamespaceFailure(stderr string) bool {
	lowerStderr := strings.ToLower(stderr)
	userNamespaceFailures := []string{
		"creating new namespace",
		"user namespaces are not enabled",
		"permission denied",
		"operation not permitted",
	}

	for _, pattern := range userNamespaceFailures {
		if strings.Contains(lowerStderr, pattern) {
			return true
		}
	}
	return false
}
