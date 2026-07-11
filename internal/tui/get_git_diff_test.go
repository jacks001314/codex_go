package tui

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
)

func TestBuildGitDiffCommandMatchesRust(t *testing.T) {
	overrides := []GitConfigOverride{
		{Key: "filter.evil.clean"},
		{Key: "filter.evil.process"},
		{Key: "filter.evil.required", Value: "false"},
	}
	command := BuildGitDiffCommand("/workspace", GitFsmonitorBuiltIn, overrides, GitTrackedDiffArgs()...)
	wantArgv := append([]string{"git", "-c", "core.fsmonitor=true", "-c", DisableHooksConfig()}, GitTrackedDiffArgs()...)
	if !reflect.DeepEqual(command.Argv, wantArgv) {
		t.Fatalf("argv = %#v, want %#v", command.Argv, wantArgv)
	}
	if command.CWD != "/workspace" || command.Timeout != GitDiffCommandTimeout || !command.DisableOutputCap {
		t.Fatalf("command metadata = %#v", command)
	}
	if got := command.Env["GIT_CONFIG_COUNT"]; got == nil || *got != "3" {
		t.Fatalf("GIT_CONFIG_COUNT = %v", got)
	}
	if got := command.Env["GIT_CONFIG_KEY_2"]; got == nil || *got != "filter.evil.required" {
		t.Fatalf("GIT_CONFIG_KEY_2 = %v", got)
	}
	if got := command.Env["GIT_CONFIG_VALUE_2"]; got == nil || *got != "false" {
		t.Fatalf("GIT_CONFIG_VALUE_2 = %v", got)
	}
}

func TestGitDiffArgsMatchRust(t *testing.T) {
	wantTracked := []string{"diff", "--no-textconv", "--no-ext-diff", "--submodule=short", "--ignore-submodules=dirty", "--color"}
	if got := GitTrackedDiffArgs(); !reflect.DeepEqual(got, wantTracked) {
		t.Fatalf("tracked args = %#v", got)
	}

	wantUntracked := append([]string{}, wantTracked...)
	wantUntracked = append(wantUntracked, "--no-index", "--", GitNullDevice(), "new.txt")
	if got := GitUntrackedDiffArgs(" new.txt "); !reflect.DeepEqual(got, wantUntracked) {
		t.Fatalf("untracked args = %#v, want %#v", got, wantUntracked)
	}
}

func TestDiffFilterConfigOverridesMatchRust(t *testing.T) {
	got := DiffFilterConfigOverrides("filter.z.process\x00filter.evil.clean\x00filter.evil.process\x00")
	want := []GitConfigOverride{
		{Key: "filter.evil.clean"},
		{Key: "filter.evil.process"},
		{Key: "filter.evil.required", Value: "false"},
		{Key: "filter.z.clean"},
		{Key: "filter.z.process"},
		{Key: "filter.z.required", Value: "false"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("overrides = %#v, want %#v", got, want)
	}
}

func TestGitCaptureStatusHandlingMatchesRust(t *testing.T) {
	if got, err := GitCaptureDiff(GitTrackedDiffArgs(), GitCommandResult{Stdout: "diff\n", ExitCode: 1}); err != nil || got != "diff\n" {
		t.Fatalf("diff exit 1 = %q err=%v", got, err)
	}
	_, err := GitCaptureDiff(GitTrackedDiffArgs(), GitCommandResult{ExitCode: 2})
	if err == nil || err.Error() != `git ["diff", "--no-textconv", "--no-ext-diff", "--submodule=short", "--ignore-submodules=dirty", "--color"] failed with status 2` {
		t.Fatalf("diff exit 2 err = %v", err)
	}
	_, err = GitCaptureStdout(GitUntrackedListArgs(), GitCommandResult{ExitCode: 1})
	if err == nil || err.Error() != `git ["ls-files", "--others", "--exclude-standard"] failed with status 1` {
		t.Fatalf("stdout exit 1 err = %v", err)
	}
}

func TestParseUntrackedAndComposeGitDiffMatchesRust(t *testing.T) {
	files := ParseUntrackedGitFiles(" new.txt\n\nnested/file.go \n")
	if !reflect.DeepEqual(files, []string{"new.txt", "nested/file.go"}) {
		t.Fatalf("files = %#v", files)
	}
	if got := ComposeGitDiff("tracked\n", []string{"untracked-a\n", "untracked-b\n"}); got != "tracked\nuntracked-a\nuntracked-b\n" {
		t.Fatalf("composed diff = %q", got)
	}
}

func TestReadGitDiffWithRunnerMatchesRustCommandFlow(t *testing.T) {
	cwd := "/workspace"
	overrides := []GitConfigOverride{
		{Key: "filter.evil.clean"},
		{Key: "filter.evil.process"},
		{Key: "filter.evil.required", Value: "false"},
	}
	runner := newScriptedGitDiffRunner(t, []scriptedGitDiffResponse{
		{command: BuildGitProbeCommand(cwd, "rev-parse", "--is-inside-work-tree"), output: WorkspaceCommandOutput{ExitCode: 0, Stdout: "true\n"}},
		{command: BuildGitProbeCommand(cwd, "config", "--null", "--get", "core.fsmonitor"), output: WorkspaceCommandOutput{ExitCode: 0, Stdout: "/tmp/fsmonitor-helper\x00"}},
		{command: BuildGitProbeCommand(cwd, "config", "--null", "--type=bool", "--fixed-value", "--get", "core.fsmonitor", "/tmp/fsmonitor-helper"), output: WorkspaceCommandOutput{ExitCode: 128}},
		{command: BuildGitDiffCommand(cwd, GitFsmonitorDisabled, nil, "config", "--null", "--name-only", "--get-regexp", ExecutableFilterConfigPattern), output: WorkspaceCommandOutput{ExitCode: 0, Stdout: "filter.evil.clean\x00filter.evil.process\x00"}},
		{command: BuildGitDiffCommand(cwd, GitFsmonitorDisabled, overrides, GitTrackedDiffArgs()...), output: WorkspaceCommandOutput{ExitCode: 1, Stdout: "tracked\n"}},
		{command: BuildGitDiffCommand(cwd, GitFsmonitorDisabled, nil, GitUntrackedListArgs()...), output: WorkspaceCommandOutput{ExitCode: 0, Stdout: "new.txt\n"}},
		{command: BuildGitDiffCommand(cwd, GitFsmonitorDisabled, overrides, GitUntrackedDiffArgs("new.txt")...), output: WorkspaceCommandOutput{ExitCode: 1, Stdout: "untracked\n"}},
	})

	diff, isRepo, err := ReadGitDiffWithRunner(context.Background(), runner, cwd)
	if err != nil {
		t.Fatalf("ReadGitDiffWithRunner returned error: %v", err)
	}
	if !isRepo || diff != "tracked\nuntracked\n" {
		t.Fatalf("result = (%v, %q), want git repo tracked+untracked", isRepo, diff)
	}
	runner.assertComplete()
}

func TestReadGitDiffWithRunnerReturnsNoRepoLikeRust(t *testing.T) {
	cwd := "/workspace"
	runner := newScriptedGitDiffRunner(t, []scriptedGitDiffResponse{
		{command: BuildGitProbeCommand(cwd, "rev-parse", "--is-inside-work-tree"), output: WorkspaceCommandOutput{ExitCode: 128}},
	})

	diff, isRepo, err := ReadGitDiffWithRunner(context.Background(), runner, cwd)
	if err != nil {
		t.Fatalf("ReadGitDiffWithRunner returned error: %v", err)
	}
	if isRepo || diff != "" {
		t.Fatalf("result = (%v, %q), want no repo", isRepo, diff)
	}
	runner.assertComplete()
}

func TestReadGitDiffIncludesTrackedAndUntrackedWithRustNoIndexPath(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := initTUIDiffRepo(t)
	writeTUIDiffFile(t, dir, "tracked.txt", "old\nnew\n")
	writeTUIDiffFile(t, dir, "untracked.txt", "fresh\n")

	diff, isRepo, err := ReadGitDiff(dir)
	if err != nil {
		t.Fatalf("ReadGitDiff returned error: %v", err)
	}
	if !isRepo {
		t.Fatal("ReadGitDiff reported non-git repo")
	}
	cleanDiff := stripANSIGitDiffTest(diff)
	for _, want := range []string{"diff --git", "tracked.txt", "+new", "untracked.txt", "+fresh"} {
		if !strings.Contains(cleanDiff, want) {
			t.Fatalf("diff missing %q:\n%s", want, cleanDiff)
		}
	}
}

type scriptedGitDiffResponse struct {
	command WorkspaceCommand
	output  WorkspaceCommandOutput
	err     error
}

type scriptedGitDiffRunner struct {
	t         *testing.T
	responses []scriptedGitDiffResponse
	index     int
}

func newScriptedGitDiffRunner(t *testing.T, responses []scriptedGitDiffResponse) *scriptedGitDiffRunner {
	t.Helper()
	return &scriptedGitDiffRunner{t: t, responses: responses}
}

func (r *scriptedGitDiffRunner) RunWorkspaceCommand(ctx context.Context, command WorkspaceCommand) (WorkspaceCommandOutput, error) {
	r.t.Helper()
	_ = ctx
	if r.index >= len(r.responses) {
		r.t.Fatalf("unexpected command: %#v", command)
	}
	response := r.responses[r.index]
	r.index++
	if !reflect.DeepEqual(command, response.command) {
		r.t.Fatalf("command %d = %#v, want %#v", r.index, command, response.command)
	}
	return response.output, response.err
}

func (r *scriptedGitDiffRunner) assertComplete() {
	r.t.Helper()
	if r.index != len(r.responses) {
		r.t.Fatalf("unused responses = %d", len(r.responses)-r.index)
	}
}

func initTUIDiffRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	runTUIDiffGit(t, dir, "init")
	runTUIDiffGit(t, dir, "config", "user.email", "codex@example.test")
	runTUIDiffGit(t, dir, "config", "user.name", "Codex Test")
	writeTUIDiffFile(t, dir, "tracked.txt", "old\n")
	runTUIDiffGit(t, dir, "add", "tracked.txt")
	runTUIDiffGit(t, dir, "commit", "-m", "initial")
	return dir
}

func writeTUIDiffFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func runTUIDiffGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
}

func stripANSIGitDiffTest(value string) string {
	return regexp.MustCompile(`\x1b\[[0-9;]*m`).ReplaceAllString(value, "")
}
