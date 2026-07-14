package win

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"codex_go/internal/sandbox/windowssandbox"
)

const RuntimeReadExecuteMask = 0x001200a9 | 0x00120089
const RuntimeInheritance = 0x1 | 0x2

func SetupRuntimeBin(sandboxGroupSID string) error {
	var refreshErrors []string
	return EnsureCodexAppRuntimePathsReadable(sandboxGroupSID, &refreshErrors, io.Discard)
}

func EnsureCodexAppRuntimePathsReadable(sandboxGroupSID string, refreshErrors *[]string, log io.Writer) error {
	localAppData := LocalAppDataRoot()
	if localAppData == "" {
		return nil
	}
	codexRoot := filepath.Join(localAppData, "OpenAI", "Codex")
	for _, runtimePath := range []string{filepath.Join(codexRoot, "bin"), filepath.Join(codexRoot, "runtimes")} {
		info, err := os.Stat(runtimePath)
		if err != nil || !info.IsDir() {
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
