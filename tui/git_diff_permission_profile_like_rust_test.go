package tui

import (
	"context"
	"strings"
	"testing"
)

// profileRecordingRunner records every command and answers from a keyed table.
// It implements WorkspacePermissionProfileProber so the run can resolve a named
// session profile (Rust #63400).
type profileRecordingRunner struct {
	commands []WorkspaceCommand
	outputs  map[string]WorkspaceCommandOutput
	probe    func(cwd string, profileID string) (bool, error)
	probed   []string
}

func (r *profileRecordingRunner) RunWorkspaceCommand(ctx context.Context, command WorkspaceCommand) (WorkspaceCommandOutput, error) {
	r.commands = append(r.commands, command)
	if output, ok := r.outputs[strings.Join(command.Argv, " ")]; ok {
		return output, nil
	}
	return WorkspaceCommandOutput{ExitCode: 128}, nil
}

func (r *profileRecordingRunner) HasPermissionProfile(ctx context.Context, cwd string, profileID string) (bool, error) {
	r.probed = append(r.probed, profileID)
	if r.probe == nil {
		return true, nil
	}
	return r.probe(cwd, profileID)
}

func profileDiffOutputs(cwd string) map[string]WorkspaceCommandOutput {
	return map[string]WorkspaceCommandOutput{
		strings.Join(BuildGitProbeCommand(cwd, "rev-parse", "--is-inside-work-tree").Argv, " "):                                                                       {ExitCode: 0, Stdout: "true\n"},
		strings.Join(BuildGitProbeCommand(cwd, "config", "--null", "--get", "core.fsmonitor").Argv, " "):                                                              {ExitCode: 1},
		strings.Join(BuildGitDiffCommand(cwd, GitFsmonitorDisabled, nil, "config", "--null", "--name-only", "--get-regexp", ExecutableFilterConfigPattern).Argv, " "): {ExitCode: 1},
		strings.Join(BuildGitDiffCommand(cwd, GitFsmonitorDisabled, nil, GitTrackedDiffArgs()...).Argv, " "):                                                          {ExitCode: 1, Stdout: "tracked\n"},
		strings.Join(BuildGitDiffCommand(cwd, GitFsmonitorDisabled, nil, GitUntrackedListArgs()...).Argv, " "):                                                        {ExitCode: 0},
	}
}

func envValue(command WorkspaceCommand, key string) (string, bool) {
	value, ok := command.Env[key]
	if !ok || value == nil {
		return "", false
	}
	return *value, true
}

// TestReadGitDiffWithProfileAppliesSessionProfileLikeRust covers Rust #63400:
// every Git command carries the session profile and stays local (no lazy fetch,
// no transport protocols).
func TestReadGitDiffWithProfileAppliesSessionProfileLikeRust(t *testing.T) {
	cwd := "/workspace"
	runner := &profileRecordingRunner{outputs: profileDiffOutputs(cwd)}
	diff, isRepo, err := ReadGitDiffWithProfile(context.Background(), runner, cwd, "trusted")
	if err != nil {
		t.Fatalf("ReadGitDiffWithProfile: %v", err)
	}
	if !isRepo || diff != "tracked\n" {
		t.Fatalf("result = (%v, %q)", isRepo, diff)
	}
	if len(runner.probed) != 1 || runner.probed[0] != "trusted" {
		t.Fatalf("profile probes = %#v, want one trusted probe", runner.probed)
	}
	if len(runner.commands) < 3 {
		t.Fatalf("recorded %d commands", len(runner.commands))
	}
	for _, command := range runner.commands {
		if command.PermissionProfile != "trusted" {
			t.Fatalf("command %s carried profile %q", command.Argv, command.PermissionProfile)
		}
		if got, ok := envValue(command, "GIT_NO_LAZY_FETCH"); !ok || got != "1" {
			t.Fatalf("command %s GIT_NO_LAZY_FETCH = %q, %v", command.Argv, got, ok)
		}
		if got, ok := envValue(command, "GIT_ALLOW_PROTOCOL"); !ok || got != "" {
			t.Fatalf("command %s GIT_ALLOW_PROTOCOL = %q, %v", command.Argv, got, ok)
		}
	}
}

// TestReadGitDiffWithProfileFallsBackWhenProfileMissingLikeRust: a reattached
// client whose profile no longer resolves runs under the server defaults.
func TestReadGitDiffWithProfileFallsBackWhenProfileMissingLikeRust(t *testing.T) {
	cwd := "/workspace"
	runner := &profileRecordingRunner{
		outputs: profileDiffOutputs(cwd),
		probe:   func(string, string) (bool, error) { return false, nil },
	}
	if _, _, err := ReadGitDiffWithProfile(context.Background(), runner, cwd, "gone"); err != nil {
		t.Fatalf("ReadGitDiffWithProfile: %v", err)
	}
	for _, command := range runner.commands {
		if command.PermissionProfile != "" {
			t.Fatalf("command %s kept the missing profile %q", command.Argv, command.PermissionProfile)
		}
		if _, ok := command.Env["GIT_NO_LAZY_FETCH"]; ok {
			t.Fatalf("command %s carried the profiled env", command.Argv)
		}
	}
}

// TestReadGitDiffWithProfileKeepsBuiltInProfileLikeRust: a `:` id is not a
// custom profile, so it is used without a catalog probe.
func TestReadGitDiffWithProfileKeepsBuiltInProfileLikeRust(t *testing.T) {
	cwd := "/workspace"
	runner := &profileRecordingRunner{outputs: profileDiffOutputs(cwd)}
	if _, _, err := ReadGitDiffWithProfile(context.Background(), runner, cwd, ":workspace"); err != nil {
		t.Fatalf("ReadGitDiffWithProfile: %v", err)
	}
	if len(runner.probed) != 0 {
		t.Fatalf("built-in profile was probed: %#v", runner.probed)
	}
	if got := runner.commands[0].PermissionProfile; got != ":workspace" {
		t.Fatalf("built-in profile = %q", got)
	}
}

// TestReadGitDiffWithProfileSurfacesDiscoveryErrorLikeRust: a rev-parse failure
// other than the ordinary discovery miss is reported under the profile.
func TestReadGitDiffWithProfileSurfacesDiscoveryErrorLikeRust(t *testing.T) {
	cwd := "/workspace"
	runner := &profileRecordingRunner{outputs: map[string]WorkspaceCommandOutput{
		strings.Join(BuildGitProbeCommand(cwd, "rev-parse", "--is-inside-work-tree").Argv, " "): {
			ExitCode: 128,
			Stderr:   "fatal: detected dubious ownership in repository at '/workspace'\n",
		},
	}}
	_, isRepo, err := ReadGitDiffWithProfile(context.Background(), runner, cwd, "trusted")
	if err == nil || isRepo {
		t.Fatalf("result = (%v, %v)", isRepo, err)
	}
	want := "git rev-parse failed under the session permission profile (exit 128).\nfatal: detected dubious ownership in repository at '/workspace'"
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}
}

// TestReadGitDiffWithProfileTreatsOrdinaryMissAsNoRepoLikeRust: the standard
// "not a git repository" diagnostic still means "not inside a repo".
func TestReadGitDiffWithProfileTreatsOrdinaryMissAsNoRepoLikeRust(t *testing.T) {
	cwd := "/workspace"
	runner := &profileRecordingRunner{outputs: map[string]WorkspaceCommandOutput{
		strings.Join(BuildGitProbeCommand(cwd, "rev-parse", "--is-inside-work-tree").Argv, " "): {
			ExitCode: 128,
			Stderr:   "fatal: not a git repository (or any of the parent directories): .git\r\n",
		},
	}}
	diff, isRepo, err := ReadGitDiffWithProfile(context.Background(), runner, cwd, "trusted")
	if err != nil || isRepo || diff != "" {
		t.Fatalf("result = (%q, %v, %v)", diff, isRepo, err)
	}
}

// TestReadGitDiffWithProfilePropagatesProbeErrorLikeRust: a catalog failure
// fails the diff instead of silently running under different permissions.
func TestReadGitDiffWithProfilePropagatesProbeErrorLikeRust(t *testing.T) {
	runner := &profileRecordingRunner{
		outputs: profileDiffOutputs("/workspace"),
		probe:   func(string, string) (bool, error) { return false, errDiffProbeUnavailable },
	}
	if _, _, err := ReadGitDiffWithProfile(context.Background(), runner, "/workspace", "trusted"); err == nil || err.Error() != "profile catalog unavailable" {
		t.Fatalf("error = %v", err)
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands ran after a failed probe: %#v", runner.commands)
	}
}

type diffProbeError string

func (e diffProbeError) Error() string { return string(e) }

const errDiffProbeUnavailable = diffProbeError("profile catalog unavailable")
