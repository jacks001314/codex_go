package tui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

type recordingWorkspaceCommandRunner struct {
	command WorkspaceCommand
	output  WorkspaceCommandOutput
	err     error
}

func (r *recordingWorkspaceCommandRunner) RunWorkspaceCommand(ctx context.Context, command WorkspaceCommand) (WorkspaceCommandOutput, error) {
	_ = ctx
	r.command = command
	return r.output, r.err
}

func TestWorkspaceHeadlineFromResponseMatchesRust(t *testing.T) {
	response := GetWorkspaceMessagesResponse{
		FeatureEnabled: true,
		Messages: []WorkspaceMessage{
			{MessageID: "announcement-id", MessageType: WorkspaceMessageAnnouncement, MessageBody: "Announcement body"},
			{MessageID: "empty-headline-id", MessageType: WorkspaceMessageHeadline, MessageBody: "   "},
			{MessageID: "headline-id", MessageType: WorkspaceMessageHeadline, MessageBody: " Workspace headline "},
		},
	}
	result := WorkspaceHeadlineFromResponse(response)
	if result.Kind != WorkspaceHeadlineFetchAvailable || result.Headline == nil || *result.Headline != "Workspace headline" {
		t.Fatalf("result = %#v", result)
	}

	disabled := WorkspaceHeadlineFromResponse(GetWorkspaceMessagesResponse{FeatureEnabled: false})
	if disabled.Kind != WorkspaceHeadlineFetchFeatureDisabled || disabled.Headline != nil {
		t.Fatalf("disabled = %#v", disabled)
	}

	empty := WorkspaceHeadlineFromResponse(GetWorkspaceMessagesResponse{FeatureEnabled: true})
	if empty.Kind != WorkspaceHeadlineFetchAvailable || empty.Headline != nil {
		t.Fatalf("empty = %#v", empty)
	}
}

func TestWorkspaceCommandBuilderMatchesRustDefaults(t *testing.T) {
	// Rust #50477: WorkspaceCommand no longer carries a TUI-owned output cap
	// (`get_git_diff.rs::assert_command_metadata` dropped the
	// `output_bytes_cap == 64 * 1024` assertion); only the uncapped opt-out
	// remains part of the command shape.
	command := NewWorkspaceCommand(" git ", "status").
		WithCWD(`D:\repo`).
		WithEnv(" GIT_OPTIONAL_LOCKS ", " 0 ").
		WithoutEnv("PAGER").
		WithTimeout(2500 * time.Millisecond).
		WithDisabledOutputCap()

	if err := command.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if command.Name != "git" || command.CWD != `D:\repo` {
		t.Fatalf("command identity = %#v", command)
	}
	if command.TimeoutMillis() != 2500 || !command.DisableOutputCap {
		t.Fatalf("command limits = %#v", command)
	}
	if len(command.Argv) != 2 || command.Argv[0] != "git" || command.Argv[1] != "status" {
		t.Fatalf("argv = %#v", command.Argv)
	}
	if command.Env["GIT_OPTIONAL_LOCKS"] == nil || *command.Env["GIT_OPTIONAL_LOCKS"] != "0" {
		t.Fatalf("env set = %#v", command.Env)
	}
	if _, ok := command.Env["PAGER"]; !ok || command.Env["PAGER"] != nil {
		t.Fatalf("env removal = %#v", command.Env)
	}
}

func TestWorkspaceCommandOutputAndRunnerBoundary(t *testing.T) {
	output := WorkspaceCommandOutput{ExitCode: 1, Stdout: "out", Stderr: "err"}
	if output.Success() {
		t.Fatal("non-zero exit should not be success")
	}
	runner := &recordingWorkspaceCommandRunner{
		output: WorkspaceCommandOutput{ExitCode: 0, Stdout: "ok"},
	}
	got, err := runner.RunWorkspaceCommand(context.Background(), NewWorkspaceCommand("git", "rev-parse"))
	if err != nil || !got.Success() || runner.command.Argv[0] != "git" {
		t.Fatalf("runner got=%#v err=%v recorded=%#v", got, err, runner.command)
	}
	runner.err = NewWorkspaceCommandError("transport failed")
	if _, err := runner.RunWorkspaceCommand(context.Background(), NewWorkspaceCommand("git")); err == nil || !strings.Contains(err.Error(), "transport failed") {
		t.Fatalf("runner error = %v", err)
	}
	if err := (WorkspaceCommand{}).Validate(); err == nil {
		t.Fatal("empty argv should fail validation")
	}
	if !errors.Is(NewWorkspaceCommandError("x"), NewWorkspaceCommandError("x")) {
		t.Log("WorkspaceCommandError is value comparable but not sentinel-based")
	}
}

func TestTerminalVisualizationInstructionsMatchRust(t *testing.T) {
	text := TerminalVisualizationInstructions()
	for _, want := range []string{
		"This surface is a terminal",
		"Use tables for exact mappings",
		"Use only ASCII characters in visuals.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("instructions missing %q:\n%s", want, text)
		}
	}
	if got := WithTerminalVisualizationInstructions(false, "dev", nil); got != nil {
		t.Fatalf("disabled nil control = %#v, want nil", got)
	}
	control := "control"
	got := WithTerminalVisualizationInstructions(false, "dev", &control)
	if got == nil || *got != "control" {
		t.Fatalf("disabled control = %#v", got)
	}
	got = WithTerminalVisualizationInstructions(true, "dev", nil)
	if got == nil || !strings.HasPrefix(*got, "dev\n\n- This surface is a terminal.") {
		t.Fatalf("enabled developer instructions = %#v", got)
	}
	got = WithTerminalVisualizationInstructions(true, "dev", &control)
	if got == nil || !strings.HasPrefix(*got, "control\n\n- This surface is a terminal.") {
		t.Fatalf("enabled control instructions = %#v", got)
	}
}

func TestWindowsSandboxLevelFromConfigMatchesRust(t *testing.T) {
	elevated := WindowsSandboxModeConfigElevated
	unelevated := WindowsSandboxModeConfigUnelevated
	if got := WindowsSandboxLevelFromConfig(&elevated, WindowsSandboxFeatureFlags{}); got != WindowsSandboxLevelElevated {
		t.Fatalf("explicit elevated = %s", got)
	}
	if got := WindowsSandboxLevelFromConfig(&unelevated, WindowsSandboxFeatureFlags{WindowsSandboxElevated: true}); got != WindowsSandboxLevelRestrictedToken {
		t.Fatalf("explicit unelevated = %s", got)
	}
	if got := WindowsSandboxLevelFromConfig(nil, WindowsSandboxFeatureFlags{WindowsSandboxElevated: true}); got != WindowsSandboxLevelElevated {
		t.Fatalf("feature elevated = %s", got)
	}
	if got := WindowsSandboxLevelFromConfig(nil, WindowsSandboxFeatureFlags{WindowsSandbox: true}); got != WindowsSandboxLevelRestrictedToken {
		t.Fatalf("feature unelevated = %s", got)
	}
	if got := WindowsSandboxLevelFromConfig(nil, WindowsSandboxFeatureFlags{}); got != WindowsSandboxLevelDisabled {
		t.Fatalf("disabled = %s", got)
	}
	parsed, ok := ParseWindowsSandboxModeConfig("default")
	if !ok || parsed == nil || *parsed != WindowsSandboxModeConfigUnelevated {
		t.Fatalf("parsed default = %#v ok=%v", parsed, ok)
	}
}

// Rust #50477 (upstream 6c15cc4aaf): "Use the app-server default output cap for
// TUI workspace commands".
//
// Rust parity counterpart: `get_git_diff.rs::assert_command_metadata`, which
// lost the `assert_eq!(command.output_bytes_cap, 64 * 1024)` expectation and is
// exercised by `get_git_diff_disables_helpers_for_tracked_and_untracked_diffs`.
// `WorkspaceCommand` no longer owns an output cap: bounded commands use the
// execution boundary's default and only `/diff`-style callers opt out.
func TestWorkspaceCommandUsesHostDefaultOutputCapLikeRust(t *testing.T) {
	probe := NewWorkspaceCommand("git", "config", "--get", "core.fsmonitor")
	if probe.DisableOutputCap {
		t.Fatalf("metadata probe must stay bounded: %#v", probe)
	}

	// The removed TUI cap was 64 KiB: a probe payload past that size must no
	// longer be truncated by the TUI.
	overOldCap := strings.Repeat("x", 70*1024)
	if got := commandOutputString([]byte(overOldCap), probe); got != overOldCap {
		t.Fatalf("bounded output truncated below the host default: len=%d", len(got))
	}

	// Bounded commands stop at the host `command/exec` default instead.
	if DefaultWorkspaceCommandOutputBytesCap != 1024*1024 {
		t.Fatalf("host default drifted from appserver defaultCommandExecOutputBytesCap: %d", DefaultWorkspaceCommandOutputBytesCap)
	}
	overHostDefault := strings.Repeat("y", DefaultWorkspaceCommandOutputBytesCap+128)
	if got := commandOutputString([]byte(overHostDefault), probe); len(got) != DefaultWorkspaceCommandOutputBytesCap {
		t.Fatalf("host default cap = %d, want %d", len(got), DefaultWorkspaceCommandOutputBytesCap)
	}

	// `/diff` keeps the uncapped opt-out (Rust `disable_output_cap` preserved).
	diff := NewWorkspaceCommand("git", "diff").WithDisabledOutputCap()
	uncapped := strings.Repeat("z", DefaultWorkspaceCommandOutputBytesCap+128)
	if got := commandOutputString([]byte(uncapped), diff); got != uncapped {
		t.Fatalf("uncapped command truncated: len=%d", len(got))
	}
	built := BuildGitDiffCommand("/workspace", GitFsmonitorBuiltIn, nil, GitTrackedDiffArgs()...)
	if !built.DisableOutputCap {
		t.Fatalf("git diff must keep disable_output_cap: %#v", built)
	}
}
