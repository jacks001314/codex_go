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
	command := NewWorkspaceCommand(" git ", "status").
		WithCWD(`D:\repo`).
		WithEnv(" GIT_OPTIONAL_LOCKS ", " 0 ").
		WithoutEnv("PAGER").
		WithTimeout(2500 * time.Millisecond).
		WithOutputBytesCap(4096).
		WithDisabledOutputCap()

	if err := command.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if command.Name != "git" || command.CWD != `D:\repo` {
		t.Fatalf("command identity = %#v", command)
	}
	if command.TimeoutMillis() != 2500 || command.OutputBytesCap != 4096 || !command.DisableOutputCap {
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
