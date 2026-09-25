package exec

import (
	"testing"

	"codex_go/execserver"
	"codex_go/tool"
	"codex_go/turn"
)

// Mirrors Rust EnvironmentManager::from_codex_home in the in-process hosts: the
// host-resolved environments reach a turn's shell options, and the shell
// executor resolves a launch with no explicit environment request to the
// configured default.
func TestRunnerInstallsConfiguredEnvironmentsOnTurnShellOptions(t *testing.T) {
	runner := NewLocalRunner("")
	runner.UnifiedExecEnvironments = []tool.UnifiedExecEnvironment{{
		ID:                     "ssh-dev",
		ExecServerStdioCommand: &execserver.StdioExecServerCommand{Program: "codex-exec-server"},
	}}

	options := turn.DefaultToolRegistryOptions(t.TempDir())
	if options.Shell == nil {
		t.Fatal("the default tool registry options have no shell options")
	}
	runner.applyUnifiedExecEnvironments(options)
	if len(options.Shell.UnifiedExecEnvironments) != 1 || options.Shell.UnifiedExecEnvironments[0].ID != "ssh-dev" {
		t.Fatalf("turn shell environments = %#v", options.Shell.UnifiedExecEnvironments)
	}
	if options.Shell.UnifiedExecEnvironments[0].ExecServerStdioCommand == nil || options.Shell.UnifiedExecEnvironments[0].ExecServerStdioCommand.Program != "codex-exec-server" {
		t.Fatalf("turn shell environment transport = %#v", options.Shell.UnifiedExecEnvironments[0])
	}
}

func TestRunnerWithoutEnvironmentsKeepsToolsLocal(t *testing.T) {
	runner := NewLocalRunner("")
	options := turn.DefaultToolRegistryOptions(t.TempDir())
	runner.applyUnifiedExecEnvironments(options)
	if len(options.Shell.UnifiedExecEnvironments) != 0 {
		t.Fatalf("unconfigured runner environments = %#v", options.Shell.UnifiedExecEnvironments)
	}

	// A configured list is copied, not aliased, so a subagent or later mutation
	// cannot change the runner's own environment set.
	runner.UnifiedExecEnvironments = []tool.UnifiedExecEnvironment{{ID: "ssh-dev"}}
	runner.applyUnifiedExecEnvironments(options)
	options.Shell.UnifiedExecEnvironments[0].ID = "mutated"
	if runner.UnifiedExecEnvironments[0].ID != "ssh-dev" {
		t.Fatalf("runner environments were aliased: %#v", runner.UnifiedExecEnvironments)
	}
}
