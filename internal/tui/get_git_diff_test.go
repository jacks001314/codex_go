package tui

import (
	"reflect"
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
