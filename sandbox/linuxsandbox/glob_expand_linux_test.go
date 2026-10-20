//go:build linux

package linuxsandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox/sandboxpath"
)

func TestExpandUnreadableGlobsUsesExplicitEnvironmentLikeRust(t *testing.T) {
	root := t.TempDir()
	commandCWD := filepath.Join(root, "command")
	bin := filepath.Join(root, "bin")
	scanRoot := filepath.Join(commandCWD, "workspace")
	for _, path := range []string{commandCWD, bin, scanRoot} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(root, "marker")
	envMarker := filepath.Join(root, "env-marker")
	script := "#!/bin/sh\npwd > \"" + marker + "\"\nenv > \"" + envMarker + "\"\nprintf '%s\\0' 'secret.key'\n"
	if err := os.WriteFile(filepath.Join(bin, GlobScanProgram), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	paths, err := ExpandUnreadableGlobsInEnvironment(
		[]string{filepath.Join(scanRoot, "*.key")},
		scanRoot,
		sandboxpath.FilesystemPolicy{},
		nil,
		map[string]string{"PATH": bin, "ONLY_THIS": "1"},
		commandCWD,
	)
	if err != nil {
		t.Fatalf("ExpandUnreadableGlobsInEnvironment() error = %v", err)
	}
	if len(paths) != 1 || paths[0] != filepath.Join(scanRoot, "secret.key") {
		t.Fatalf("expanded paths = %#v", paths)
	}
	if cwd, err := os.ReadFile(marker); err != nil || strings.TrimSpace(string(cwd)) != commandCWD {
		t.Fatalf("scanner cwd = %q, %v", cwd, err)
	}
	if env, err := os.ReadFile(envMarker); err != nil || !strings.Contains(string(env), "ONLY_THIS=1") {
		t.Fatalf("scanner environment = %q, %v", env, err)
	}
}
