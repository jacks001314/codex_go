package scripts

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestWindowsBuildPipelinesPackageSandboxHelpers(t *testing.T) {
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
			path: filepath.Join(root, "build.ps1"),
			required: []string{
				"codex-resources",
				"./cmd/codex-command-runner",
				"./cmd/codex-windows-sandbox-setup",
			},
		},
		{
			path: filepath.Join(root, "build.sh"),
			required: []string{
				"codex-resources",
				"codex-command-runner",
				"codex-windows-sandbox-setup",
			},
		},
		{
			path: filepath.Join(root, "..", "npm", "scripts", "build-packages.mjs"),
			required: []string{
				"codex-resources",
				"codex-package.json",
				"codex-command-runner",
				"codex-windows-sandbox-setup",
				"x86_64-pc-windows-msvc",
			},
		},
		{
			path: filepath.Join(root, "..", "npm", "codex", "bin", "codex.js"),
			required: []string{
				"targetTriple",
				"x86_64-pc-windows-msvc",
				`"bin"`,
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
					t.Fatalf("%s does not package %q", test.path, required)
				}
			}
		})
	}
}
