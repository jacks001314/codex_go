package integrity

import (
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

// Rust #59250 removed the external deny-glob scanner from MXC dependencies.
func TestMXCDependenciesExcludeGlobScannerLikeRust(t *testing.T) {
	cwd := t.TempDir()
	entries := []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: filepath.Join(cwd, "*.key")},
		Access: sandbox.FileSystemAccessDeny,
	}}
	prepared, err := PreparePolicy(entries, cwd)
	if err != nil {
		t.Fatal(err)
	}
	dependencies := platformIntegrityDependencies(&RunnerRequest{
		SandboxType: sandbox.SandboxTypeWindowsMxc,
		CWD:         cwd,
		Command:     []string{filepath.Join(cwd, "codex.exe")},
		Env:         map[string]string{"PATH": cwd},
	}, prepared)
	if len(dependencies) != 0 {
		t.Fatalf("dependencies = %#v, want no scanner dependency", dependencies)
	}
}
