package app

// Rust parity: codex-rs/tui/src/app_server_session.rs task-tool capabilities.
// A remote app-server session persists the threads whose server accepted the
// codex_tui task-tool namespace as empty marker files, so a later process can
// still offer task references for them
// (AppServerSession::with_local_codex_home / remember_task_tool_thread).

import (
	"os"
	"path/filepath"
	"strings"
)

// taskToolCapabilitiesDirName mirrors Rust's
// `tui-thread-reference-capabilities` directory.
const taskToolCapabilitiesDirName = "tui-thread-reference-capabilities"

func taskToolCapabilitiesDir(codexHome string) string {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return ""
	}
	return filepath.Join(codexHome, taskToolCapabilitiesDirName)
}

// rememberRemoteTaskToolThread writes the capability marker (best effort, like
// Rust's warn-on-error).
func rememberRemoteTaskToolThread(codexHome string, threadID string) {
	directory := taskToolCapabilitiesDir(codexHome)
	threadID = strings.TrimSpace(threadID)
	marker := capabilityMarkerName(threadID)
	if directory == "" || marker == "" {
		return
	}
	if err := os.MkdirAll(directory, 0o755); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(directory, marker), nil, 0o600)
}

// remoteTaskToolThreadAvailable mirrors Rust's task_tools_available marker half.
func remoteTaskToolThreadAvailable(codexHome string, threadID string) bool {
	directory := taskToolCapabilitiesDir(codexHome)
	threadID = strings.TrimSpace(threadID)
	marker := capabilityMarkerName(threadID)
	if directory == "" || marker == "" {
		return false
	}
	info, err := os.Stat(filepath.Join(directory, marker))
	return err == nil && !info.IsDir()
}

// capabilityMarkerName keeps the marker a plain file name even if a malformed
// thread id reaches this layer.
func capabilityMarkerName(threadID string) string {
	name := strings.TrimSpace(threadID)
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}

// inheritRemoteTaskToolCapability mirrors Rust's fork inheritance
// (AppServerSession::fork_thread_with_permission_mode): a fork of a thread that
// hosts the task-tool namespace keeps the capability, so a later process
// resuming the fork still offers task references.
func inheritRemoteTaskToolCapability(codexHome string, parentThreadID string, childThreadID string) {
	if !remoteTaskToolThreadAvailable(codexHome, parentThreadID) {
		return
	}
	rememberRemoteTaskToolThread(codexHome, childThreadID)
}
