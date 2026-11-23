package tea

import "strings"

// Rust parity: codex-rs/tui/src/chatwidget/slash_dispatch.rs
// diff_permission_profile (#63400).
//
// /diff runs Git through standalone app-server commands, so it would otherwise
// pick up the server's default permissions even when the session uses another
// profile. A profile id can only be reloaded when the local daemon can resolve
// it for a workspace that the command's working directory fully represents:
// extra roots or permissions from session-only settings are not replayable.
func (m *Model) diffPermissionProfileID() string {
	if m == nil || m.State == nil || !m.localDaemonSession || !m.diffPermissionProfileAllowed {
		return ""
	}
	// A frozen or read-only view borrowed another thread's settings.
	if m.readOnlyThread || m.archivedThread {
		return ""
	}
	// A pending server profile means the confirmed settings are not in place yet.
	if strings.TrimSpace(m.pendingServerProfile) != "" {
		return ""
	}
	if !m.diffWorkspaceIsSingleRoot() {
		return ""
	}
	profileID := strings.TrimSpace(m.State.Sandbox)
	if profileID == "" || strings.HasPrefix(profileID, ":") {
		return ""
	}
	return profileID
}

// diffWorkspaceIsSingleRoot reports whether the session's server-resolved
// workspace is exactly the diff working directory. Before a thread exists the
// TUI only knows the startup workspace, which the command cwd fully describes.
func (m *Model) diffWorkspaceIsSingleRoot() bool {
	cwd := strings.TrimSpace(m.sessionCWD)
	if cwd == "" {
		return false
	}
	roots := m.sessionWorkspaceRoots
	if len(roots) == 0 {
		return strings.TrimSpace(m.State.ThreadID) == ""
	}
	if len(roots) != 1 {
		return false
	}
	return sameWorkspacePath(roots[0], cwd)
}

// sameWorkspacePath compares workspace paths the way the settings comparison
// does: trimmed, case-insensitive on Windows-style separators.
func sameWorkspacePath(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == right {
		return left != ""
	}
	normalize := func(value string) string {
		return strings.ToLower(strings.ReplaceAll(value, "\\", "/"))
	}
	return normalize(left) == normalize(right)
}
