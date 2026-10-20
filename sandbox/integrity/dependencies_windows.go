package integrity

import (
	"strings"

	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

// platformIntegrityDependencies mirrors Rust's Windows MXC backend (#51831):
// the base inventory already contains Codex and the sandbox launcher; a deny
// glob additionally inventories the scanner selected from the command's own
// PATH and working directory.
func platformIntegrityDependencies(request *RunnerRequest, prepared *PreparedPolicy) []integrityDependency {
	if request == nil || request.SandboxType != sandbox.SandboxTypeWindowsMxc ||
		prepared == nil || prepared.GlobDenials == nil || len(request.Command) == 0 {
		return nil
	}
	commandCWD := strings.TrimSpace(request.CWD)
	if commandCWD == "" {
		return nil
	}
	pathEnv := ""
	for key, value := range request.Env {
		if strings.EqualFold(key, "PATH") {
			pathEnv = value
			break
		}
	}
	if pathEnv == "" {
		return nil
	}
	scanner := windowssandbox.FindExecutableInCommandPath(
		windowssandbox.GlobScanProgram,
		pathEnv,
		commandCWD,
	)
	if scanner == "" {
		return nil
	}
	return []integrityDependency{{name: "glob_scanner", path: scanner}}
}
