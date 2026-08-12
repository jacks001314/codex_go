package win

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"codex_go/sandbox/windowssandbox"
)

const RuntimeReadExecuteMask = 0x001200a9 | 0x00120089
const RuntimeInheritance = 0x1 | 0x2

func SetupRuntimeBin(sandboxGroupSID string) error {
	var refreshErrors []string
	return EnsureCodexAppRuntimePathsReadable(sandboxGroupSID, &refreshErrors, io.Discard)
}

func EnsureCodexAppRuntimePathsReadable(sandboxGroupSID string, refreshErrors *[]string, log io.Writer) error {
	localAppData := LocalAppDataRoot()
	// Mirrors Rust setup_runtime_bin.rs runtime_paths (#38064): the local
	// Codex application root is granted as a whole so the read/execute ACL
	// inherits across its contents, while the managed primary runtime (outside
	// LocalAppData) is handled separately.
	for _, runtimePath := range runtimePaths(localAppData, os.Getenv("USERPROFILE")) {
		if !runtimeDirEligible(runtimePath) {
			continue
		}
		req := windowssandbox.ACLRequest{Path: runtimePath, SID: sandboxGroupSID, Mask: RuntimeReadExecuteMask}
		hasAccess, err := windowssandbox.PathMaskAllows(req, true)
		if err != nil {
			if windowssandbox.IsUnsupported(err) {
				appendRefreshError(refreshErrors, fmt.Sprintf("runtime ACL setup unsupported off Windows: %v", err))
				logLine(log, fmt.Sprintf("runtime ACL setup unsupported off Windows: %v; continuing", err))
				return nil
			}
			appendRefreshError(refreshErrors, fmt.Sprintf("runtime read/execute mask check failed on %s for sandbox_group: %v", runtimePath, err))
			logLine(log, fmt.Sprintf("runtime read/execute mask check failed on %s for sandbox_group: %v; continuing", runtimePath, err))
			hasAccess = false
		}
		if hasAccess {
			continue
		}
		logLine(log, fmt.Sprintf("granting read/execute ACE to %s for sandbox users", runtimePath))
		if err := windowssandbox.EnsureAllowMaskACEsWithInheritance(req, RuntimeInheritance); err != nil {
			appendRefreshError(refreshErrors, fmt.Sprintf("grant read/execute ACE failed on %s for sandbox_group: %v", runtimePath, err))
			logLine(log, fmt.Sprintf("grant read/execute ACE failed on %s for sandbox_group: %v", runtimePath, err))
		}
	}
	return nil
}

// runtimePaths mirrors Rust runtime_paths: the Codex app root under
// LocalAppData, then the managed primary runtime under the user profile cache.
func runtimePaths(localAppData string, userProfile string) []string {
	var runtimePaths []string
	if strings.TrimSpace(localAppData) != "" {
		runtimePaths = append(runtimePaths, filepath.Join(localAppData, "OpenAI", "Codex"))
	}
	if strings.TrimSpace(userProfile) != "" {
		runtimePaths = append(runtimePaths, filepath.Join(userProfile, ".cache", "codex-runtimes"))
	}
	return runtimePaths
}

// runtimeDirEligible mirrors Rust's symlink_metadata guard: only real
// directories (no reparse points) are inspected or updated. Missing paths,
// files, and directory junctions/symlinks are skipped.
func runtimeDirEligible(path string) bool {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || isReparsePoint(info) {
		return false
	}
	return true
}

func LocalAppDataRoot() string {
	if value := os.Getenv("LOCALAPPDATA"); value != "" {
		return value
	}
	if profile := os.Getenv("USERPROFILE"); profile != "" {
		return filepath.Join(profile, "AppData", "Local")
	}
	return ""
}

func appendRefreshError(refreshErrors *[]string, message string) {
	if refreshErrors != nil {
		*refreshErrors = append(*refreshErrors, message)
	}
}

func logLine(log io.Writer, line string) {
	if log == nil {
		return
	}
	_, _ = fmt.Fprintln(log, line)
}
