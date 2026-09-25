package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	codexexec "codex_go/exec"
	"codex_go/execserver"
)

// Mirrors Rust EnvironmentManager::from_codex_home in the non-app-server hosts:
// `codex exec`, the TUI and the worktree paths build their executor
// environments from CODEX_HOME, so an environments.toml entry selects the same
// transport it does in the app-server.
func TestConfigureLocalRunnerEnvironmentsLoadsEnvironmentsTOML(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv(execserver.CodexExecServerURLEnvVarName, "")
	body := "default = \"ssh-dev\"\n\n[[environments]]\nid = \"ssh-dev\"\nprogram = \"codex-exec-server\"\nargs = [\"--stdio\"]\n"
	if err := os.WriteFile(filepath.Join(codexHome, execserver.EnvironmentsTOMLFile), []byte(body), 0o600); err != nil {
		t.Fatalf("write environments.toml: %v", err)
	}

	runner := codexexec.NewLocalRunner(codexHome)
	if err := configureLocalRunnerEnvironments(runner, codexHome, false); err != nil {
		t.Fatalf("configureLocalRunnerEnvironments() error = %v", err)
	}
	if len(runner.UnifiedExecEnvironments) != 1 {
		t.Fatalf("runner environments = %#v", runner.UnifiedExecEnvironments)
	}
	environment := runner.UnifiedExecEnvironments[0]
	if environment.ID != "ssh-dev" || environment.ExecServerStdioCommand == nil || environment.ExecServerStdioCommand.Program != "codex-exec-server" {
		t.Fatalf("configured environment = %#v", environment)
	}
	if got := environment.ExecServerStdioCommand.Args; len(got) != 1 || got[0] != "--stdio" {
		t.Fatalf("configured environment args = %#v", got)
	}
}

// A host with no environments.toml (and no CODEX_EXEC_SERVER_URL) keeps every
// tool on the implicit local environment, and a malformed file fails the host
// before any turn starts.
func TestConfigureLocalRunnerEnvironmentsWithoutFileAndRejectsMalformed(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv(execserver.CodexExecServerURLEnvVarName, "")
	runner := codexexec.NewLocalRunner(codexHome)
	if err := configureLocalRunnerEnvironments(runner, codexHome, false); err != nil {
		t.Fatalf("configureLocalRunnerEnvironments() error = %v", err)
	}
	if len(runner.UnifiedExecEnvironments) != 0 {
		t.Fatalf("environments without a file = %#v", runner.UnifiedExecEnvironments)
	}

	if err := os.WriteFile(filepath.Join(codexHome, execserver.EnvironmentsTOMLFile), []byte("default = \"ssh-dev\"\n[[environments]]\nid = \"ssh-dev\"\n"), 0o600); err != nil {
		t.Fatalf("write malformed environments.toml: %v", err)
	}
	err := configureLocalRunnerEnvironments(runner, codexHome, false)
	if err == nil || !strings.Contains(err.Error(), "must set exactly one of url or program") {
		t.Fatalf("malformed environments.toml error = %v", err)
	}
}

// Rust's CODEX_EXEC_SERVER_URL fallback: with no environments.toml the remote
// environment becomes the default for the standalone hosts too.
func TestConfigureLocalRunnerEnvironmentsHonorsExecServerURLFallback(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv(execserver.CodexExecServerURLEnvVarName, "wss://example.test/exec")
	runner := codexexec.NewLocalRunner(codexHome)
	if err := configureLocalRunnerEnvironments(runner, codexHome, false); err != nil {
		t.Fatalf("configureLocalRunnerEnvironments() error = %v", err)
	}
	if len(runner.UnifiedExecEnvironments) != 1 {
		t.Fatalf("fallback environments = %#v", runner.UnifiedExecEnvironments)
	}
	if got := runner.UnifiedExecEnvironments[0]; got.ID != execserver.RemoteEnvironmentID || got.ExecServerURL != "wss://example.test/exec" {
		t.Fatalf("fallback environment = %#v", got)
	}
}

// Rust ignore_user_config: the user's environments.toml is skipped and only the
// CODEX_EXEC_SERVER_URL fallback applies.
func TestConfigureLocalRunnerEnvironmentsSkipsUserConfigWhenIgnored(t *testing.T) {
	codexHome := t.TempDir()
	body := "default = \"ssh-dev\"\n\n[[environments]]\nid = \"ssh-dev\"\nprogram = \"codex-exec-server\"\n"
	if err := os.WriteFile(filepath.Join(codexHome, execserver.EnvironmentsTOMLFile), []byte(body), 0o600); err != nil {
		t.Fatalf("write environments.toml: %v", err)
	}
	t.Setenv(execserver.CodexExecServerURLEnvVarName, "wss://example.test/exec")

	runner := codexexec.NewLocalRunner(codexHome)
	if err := configureLocalRunnerEnvironments(runner, codexHome, true); err != nil {
		t.Fatalf("configureLocalRunnerEnvironments(ignore) error = %v", err)
	}
	if len(runner.UnifiedExecEnvironments) != 1 || runner.UnifiedExecEnvironments[0].ID != execserver.RemoteEnvironmentID {
		t.Fatalf("ignored user config environments = %#v", runner.UnifiedExecEnvironments)
	}

	// Without the fallback the ignored user config leaves tools local.
	t.Setenv(execserver.CodexExecServerURLEnvVarName, "")
	runner = codexexec.NewLocalRunner(codexHome)
	if err := configureLocalRunnerEnvironments(runner, codexHome, true); err != nil {
		t.Fatalf("configureLocalRunnerEnvironments(ignore) error = %v", err)
	}
	if len(runner.UnifiedExecEnvironments) != 0 {
		t.Fatalf("ignored user config without a fallback = %#v", runner.UnifiedExecEnvironments)
	}
}
