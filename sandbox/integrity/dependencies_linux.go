//go:build linux

package integrity

import (
	"os"
	"path/filepath"
	"strings"

	"codex_go/sandbox/linuxsandbox"
	"codex_go/sandbox/sandboxpath"
)

func platformIntegrityDependencies(request *RunnerRequest, prepared *PreparedPolicy) []integrityDependency {
	if request == nil || prepared == nil || len(request.Command) == 0 {
		return nil
	}
	commandCWD := strings.TrimSpace(request.CWD)
	if commandCWD == "" {
		return nil
	}
	dependencies := []integrityDependency{}
	if bundled, ok := linuxsandbox.FindBundledBwrapForExecutable(request.Command[0], commandCWD); ok {
		dependencies = append(dependencies, integrityDependency{
			name: "bundled_bwrap_candidate",
			path: bundled,
		})
	}
	pathEnv, hasPath := request.Env["PATH"]
	if !hasPath {
		return dependencies
	}
	pathEnv = integrityCommandPath(pathEnv, commandCWD)
	policy := sandboxpath.FilesystemPolicy{WritableRoots: prepared.Roots}
	if system := sandboxpath.FindExecutableInPath("bwrap", pathEnv, commandCWD, policy); system != "" {
		dependencies = append(dependencies, integrityDependency{
			name: "system_bwrap_candidate",
			path: system,
		})
	}
	if prepared.GlobDenials != nil {
		if scanner := sandboxpath.FindExecutableInPath(linuxsandbox.GlobScanProgram, pathEnv, commandCWD, policy); scanner != "" {
			dependencies = append(dependencies, integrityDependency{
				name: "glob_scanner",
				path: scanner,
			})
		}
	}
	return dependencies
}

func integrityCommandPath(pathEnv, commandCWD string) string {
	entries := filepath.SplitList(pathEnv)
	for index, entry := range entries {
		if strings.TrimSpace(entry) == "" {
			entries[index] = commandCWD
			continue
		}
		if !filepath.IsAbs(entry) {
			entries[index] = filepath.Join(commandCWD, entry)
		}
	}
	return strings.Join(entries, string(os.PathListSeparator))
}
