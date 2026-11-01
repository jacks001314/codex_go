package exec

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/execserver"
)

// Rust parity: exec/tests/suite/worktree.rs
// worktree_rejects_remote_defaults_before_allocation. A remote default from
// either CODEX_EXEC_SERVER_URL or environments.toml fails the run before a
// managed checkout is created.
func TestRunWorktreeRejectsRemoteDefaultBeforeAllocationLikeRust(t *testing.T) {
	for _, route := range []string{"environment", "file", "noise"} {
		t.Run(route, func(t *testing.T) {
			home := t.TempDir()
			switch route {
			case "environment":
				t.Setenv(execserver.CodexExecServerURLEnvVarName, "ws://127.0.0.1:9")
			case "file":
				contents := "default = 'remote'\n[[environments]]\nid = 'remote'\nurl = 'ws://127.0.0.1:9'\n"
				if err := os.WriteFile(filepath.Join(home, execserver.EnvironmentsTOMLFile), []byte(contents), 0o600); err != nil {
					t.Fatalf("write environments.toml: %v", err)
				}
			case "noise":
				t.Setenv(execserver.CodexExecServerNoiseRegistryURLEnvVar, "https://registry.example/")
				t.Setenv(execserver.CodexExecServerNoiseEnvironmentIDEnvVar, "environment-1")
				t.Setenv(execserver.CodexExecServerNoiseAuthTokenEnvVar, "registry-token")
			}
			var stdout, stderr bytes.Buffer
			_, err := NewLocalRunner(home).Run(Request{
				Exec: cli.ExecOptions{
					Prompt: "hello",
					JSON:   true,
					Shared: cli.SharedOptions{Worktree: true},
				},
			}, strings.NewReader(""), &stdout, &stderr)
			if err == nil || !strings.Contains(err.Error(), "--worktree requires local execution") {
				t.Fatalf("Run() error = %v, want --worktree requires local execution", err)
			}
			if _, statErr := os.Stat(filepath.Join(home, "worktrees")); !os.IsNotExist(statErr) {
				t.Fatalf("worktrees directory was created (stat error %v)", statErr)
			}
		})
	}
}
