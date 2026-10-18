//go:build linux

package integrity

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

func TestBubblewrapDependenciesUseLauncherLocationAndCommandPathLikeRust(t *testing.T) {
	root := t.TempDir()
	cwd := filepath.Join(root, "command")
	tools := filepath.Join(root, "tools")
	bundled := filepath.Join(tools, "codex-resources", "bwrap")
	for _, directory := range []string{cwd, tools, filepath.Dir(bundled)} {
		if err := os.MkdirAll(directory, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, path := range []string{
		filepath.Join(tools, "bwrap"),
		filepath.Join(tools, "rg"),
		filepath.Join(tools, "codex"),
		bundled,
	} {
		if err := os.WriteFile(path, []byte("fixture"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	launcher := filepath.Join(cwd, "sandbox-helper")
	if err := os.Symlink(filepath.Join(tools, "codex"), launcher); err != nil {
		t.Fatal(err)
	}
	entries := []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: "*.key"},
		Access: sandbox.FileSystemAccessDeny,
	}}
	prepared, err := PreparePolicy(entries, cwd)
	if err != nil {
		t.Fatal(err)
	}
	for _, pathEnv := range []string{"../tools", ":../tools"} {
		request := &RunnerRequest{CWD: cwd, Command: []string{launcher}, Env: map[string]string{"PATH": pathEnv}}
		got := map[string]string{}
		for _, dependency := range platformIntegrityDependencies(request, prepared) {
			resolved, err := filepath.EvalSymlinks(dependency.path)
			if err != nil {
				t.Fatal(err)
			}
			got[dependency.name] = resolved
		}
		want := map[string]string{
			"bundled_bwrap_candidate": bundled,
			"system_bwrap_candidate":  filepath.Join(tools, "bwrap"),
			"glob_scanner":            filepath.Join(tools, "rg"),
		}
		for name, path := range want {
			if got[name] != path {
				t.Fatalf("PATH %q dependency %s = %q, want %q (all=%#v)", pathEnv, name, got[name], path, got)
			}
		}
	}
}
