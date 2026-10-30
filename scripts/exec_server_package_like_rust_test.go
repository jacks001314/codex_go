package scripts

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Rust #57837 ships the standalone executor in every release package. Go's
// packaging pipelines must build cmd/codex-exec-server beside the CLI and name
// it in codex-package.json so a managed daemon can resolve it.
func TestPackagingShipsStandaloneExecutorLikeRust(t *testing.T) {
	_, filename, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root := filepath.Dir(filename)
	tests := []struct {
		path     string
		required []string
	}{
		{
			path: filepath.Join(root, "build.sh"),
			required: []string{
				"./cmd/codex-exec-server",
				`EXEC_SERVER_OUTPUT=$(dirname -- "$OUTPUT")/codex-exec-server$EXT`,
				`"execServer": "codex-exec-server$EXT"`,
			},
		},
		{
			path: filepath.Join(root, "build.ps1"),
			required: []string{
				"./cmd/codex-exec-server",
				`"codex-exec-server$Extension"`,
				`execServer    = "codex-exec-server$Extension"`,
			},
		},
		{
			path: filepath.Join(root, "..", "npm", "scripts", "build-packages.mjs"),
			required: []string{
				"./cmd/codex-exec-server",
				"codex-exec-server",
				"execServer: `bin/${execServerName}`",
			},
		},
	}
	for _, test := range tests {
		t.Run(filepath.Base(test.path), func(t *testing.T) {
			content, err := os.ReadFile(test.path)
			if err != nil {
				t.Fatal(err)
			}
			for _, required := range test.required {
				if !strings.Contains(string(content), required) {
					t.Fatalf("%s does not ship the standalone executor: missing %q", test.path, required)
				}
			}
		})
	}
}
