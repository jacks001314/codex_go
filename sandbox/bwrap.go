package sandbox

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"codex_go/sandbox/sandboxpath"
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
	// systemBwrapProbeStderrLimitBytes caps the stderr kept for the usability
	// verdict (Rust SYSTEM_BWRAP_PROBE_STDERR_LIMIT_BYTES).
	systemBwrapProbeStderrLimitBytes = 64 * 1024
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

// findSystemBwrapInPath mirrors Rust find_system_bwrap_in_path: the first
// `bwrap` on PATH whose canonical location the filesystem policy cannot replace
// (Rust #51211). Candidates under the working directory are skipped, and so is
// anything the policy may write and the current user can modify, so a project
// cannot shadow the sandbox launcher with its own binary.
func findSystemBwrapInPath(pathEnv string, cwd string, policy sandboxpath.FilesystemPolicy) string {
	return sandboxpath.FindExecutableInPath("bwrap", pathEnv, cwd, policy)
}

// probeSystemBwrapUserNamespaces mirrors Rust
// system_bwrap_has_user_namespace_access: bubblewrap is usable unless it reports
// one of the user-namespace markers. A launcher that cannot be started or that
// outlives the timeout is treated as usable so the warning never blocks work.
//
// The child owns an explicit stderr pipe instead of a buffer, so process exit is
// observed as soon as the launcher terminates: a descendant that keeps the pipe
// open cannot delay the verdict (Rust #62160). Only the bytes that already
// arrived are inspected, matching the bounded non-blocking read upstream.
func probeSystemBwrapUserNamespaces(bwrapPath string, timeout time.Duration) bool {
	if timeout <= 0 {
		timeout = systemBwrapProbeTimeout
	}
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		return true
	}
	command := exec.Command(bwrapPath,
		"--unshare-user",
		"--unshare-net",
		"--ro-bind", "/", "/",
		"/bin/true")
	command.Stderr = writeEnd
	if err := command.Start(); err != nil {
		_ = writeEnd.Close()
		_ = readEnd.Close()
		return true
	}
	// The parent's copy must go away so the launcher owns the pipe.
	_ = writeEnd.Close()
	stderr := &bwrapStderrSnapshot{limit: systemBwrapProbeStderrLimitBytes}
	readerDone := make(chan struct{})
	go func() {
		defer close(readerDone)
		stderr.collect(readEnd)
	}()
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	timedOut := false
	var waitErr error
	select {
	case waitErr = <-done:
	case <-time.After(timeout):
		timedOut = true
		if command.Process != nil {
			_ = command.Process.Kill()
		}
		<-done
	}
	captured := stderr.String()
	// Closing the read end releases a reader still blocked on a descendant.
	_ = readEnd.Close()
	<-readerDone
	if timedOut {
		return true
	}
	if waitErr == nil {
		return true
	}
	return !isUserNamespaceFailure(captured)
}

// bwrapStderrSnapshot keeps the bounded prefix of a launcher's stderr while the
// probe decides whether the failure names a user-namespace problem.
type bwrapStderrSnapshot struct {
	limit int
	mu    sync.Mutex
	buf   bytes.Buffer
}

func (s *bwrapStderrSnapshot) collect(reader io.Reader) {
	var buffer [4096]byte
	for {
		count, err := reader.Read(buffer[:])
		if count > 0 {
			s.append(buffer[:count])
		}
		if err != nil {
			return
		}
	}
}

func (s *bwrapStderrSnapshot) append(chunk []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining := s.limit - s.buf.Len()
	if remaining <= 0 {
		return
	}
	if len(chunk) > remaining {
		chunk = chunk[:remaining]
	}
	_, _ = s.buf.Write(chunk)
}

func (s *bwrapStderrSnapshot) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.buf.String()
}
