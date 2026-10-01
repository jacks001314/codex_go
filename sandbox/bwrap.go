package sandbox

import (
	"bytes"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// System-bubblewrap discovery and warning (Rust sandboxing/src/bwrap.rs).
//
// The sandbox prefers a system `bwrap`; the packaged launcher is the fallback.
// These helpers only look at PATH, because the warning tells the user to install
// bubblewrap while (as the text says) the bundled binary keeps working.

const (
	// missingBwrapWarning mirrors Rust MISSING_BWRAP_WARNING.
	missingBwrapWarning = "Codex could not find bubblewrap on PATH. " +
		"Install bubblewrap with your OS package manager. " +
		"See the sandbox prerequisites: " +
		"https://developers.openai.com/codex/concepts/sandboxing#prerequisites. " +
		"Codex will use the bundled bubblewrap in the meantime."
	// userNamespaceWarning mirrors Rust USER_NAMESPACE_WARNING.
	userNamespaceWarning = "Codex's Linux sandbox uses bubblewrap and needs access to create user namespaces."
	// wsl1BwrapWarning mirrors Rust WSL1_BWRAP_WARNING.
	wsl1BwrapWarning = "Codex's Linux sandbox uses bubblewrap, which is not supported on WSL1 " +
		"because WSL1 cannot create the required user namespaces. " +
		"Use WSL2 for sandboxed shell commands."
	// systemBwrapProbeTimeout bounds the user-namespace probe (Rust
	// SYSTEM_BWRAP_PROBE_TIMEOUT).
	systemBwrapProbeTimeout = 500 * time.Millisecond
)

// userNamespaceFailureMarkers mirror Rust USER_NAMESPACE_FAILURES. Only these
// messages prove that bubblewrap could not create a user namespace; any other
// non-zero exit is treated as usable.
var userNamespaceFailureMarkers = []string{
	"loopback: Failed RTM_NEWADDR",
	"loopback: Failed RTM_NEWLINK",
	"setting up uid map: Permission denied",
	"No permissions to create a new namespace",
}

// systemBwrapWarning reports why the system bubblewrap is missing or unusable, or
// "" when it is fine (Rust system_bwrap_warning_for_path).
func systemBwrapWarning(systemBwrapPath string, wsl1 bool, probe func(string) bool) string {
	if wsl1 {
		return wsl1BwrapWarning
	}
	if strings.TrimSpace(systemBwrapPath) == "" {
		return missingBwrapWarning
	}
	if probe != nil && !probe(systemBwrapPath) {
		return userNamespaceWarning
	}
	return ""
}

// isUserNamespaceFailure reports whether bubblewrap's stderr names a user
// namespace failure (Rust is_user_namespace_failure).
func isUserNamespaceFailure(stderr string) bool {
	for _, marker := range userNamespaceFailureMarkers {
		if strings.Contains(stderr, marker) {
			return true
		}
	}
	return false
}

// findSystemBwrapInPath mirrors Rust find_system_bwrap_in_path: the first `bwrap`
// on PATH whose canonical location is outside the working directory, so a
// project cannot shadow the sandbox launcher with its own binary.
func findSystemBwrapInPath(pathEnv string, cwd string) string {
	canonicalCwd := canonicalDirectory(cwd)
	for _, dir := range filepath.SplitList(pathEnv) {
		if strings.TrimSpace(dir) == "" {
			continue
		}
		candidate := filepath.Join(dir, "bwrap")
		resolved := canonicalFile(candidate)
		if resolved == "" {
			continue
		}
		if canonicalCwd != "" && pathIsInside(resolved, canonicalCwd) {
			continue
		}
		return resolved
	}
	return ""
}

func canonicalFile(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return ""
	}
	info, err := exec.LookPath(resolved)
	if err != nil || strings.TrimSpace(info) == "" {
		return ""
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return ""
	}
	return absolute
}

func canonicalDirectory(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}
	absolute, err := filepath.Abs(resolved)
	if err != nil {
		return ""
	}
	return absolute
}

func pathIsInside(path string, root string) bool {
	relative, err := filepath.Rel(root, path)
	if err != nil {
		return false
	}
	if relative == "." {
		return true
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// probeSystemBwrapUserNamespaces mirrors Rust
// system_bwrap_has_user_namespace_access: bubblewrap is usable unless it reports
// one of the user-namespace markers. A launcher that cannot be started or that
// outlives the timeout is treated as usable so the warning never blocks work.
func probeSystemBwrapUserNamespaces(bwrapPath string, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = systemBwrapProbeTimeout
	}
	command := exec.Command(bwrapPath,
		"--unshare-user",
		"--unshare-net",
		"--ro-bind", "/", "/",
		"/bin/true")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return true
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return true
		}
		return !isUserNamespaceFailure(stderr.String())
	case <-time.After(timeout):
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		<-done
		return true
	}
}
