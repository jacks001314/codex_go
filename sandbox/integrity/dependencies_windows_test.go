package integrity

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

func TestMXCDependenciesUseCommandPathLikeRust(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "command")
	tools := filepath.Join(root, "tools")
	for _, directory := range []string{cwd, tools} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	scanner := filepath.Join(tools, "rg.exe")
	if err := os.WriteFile(scanner, []byte("fixture"), 0o755); err != nil {
		t.Fatal(err)
	}
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
		Command:     []string{filepath.Join(tools, "codex.exe")},
		Env:         map[string]string{"PATH": tools},
	}, prepared)
	if len(dependencies) != 1 || dependencies[0].name != "glob_scanner" ||
		!sameWindowsPath(dependencies[0].path, scanner) {
		t.Fatalf("dependencies = %#v, want %q", dependencies, scanner)
	}
}

func sameWindowsPath(left, right string) bool {
	return strings.EqualFold(filepath.Clean(left), filepath.Clean(right))
}
