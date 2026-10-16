package tui

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"
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

// untrackedDiffRunnerOptions configures the fake runner used by the untracked
// diff collection tests below.
type untrackedDiffRunnerOptions struct {
	files []string
	// gated makes every per-file diff wait for release() so the peak number of
	// in-flight diffs is observable without guessing at timings.
	gated  bool
	target int
	// skew makes later files finish first, so an implementation that appended
	// patches in completion order would emit a reversed order.
	skew time.Duration
	// failExit marks files whose git diff fails with a status outside 0/1.
	failExit map[string]bool
	// failSpawn marks files whose git diff cannot be spawned at all.
	failSpawn map[string]bool
	// trackedExit / untrackedListExit override the tracked diff and ls-files
	// statuses, which keep their original failure semantics.
	trackedExit       int
	untrackedListExit int
}

type untrackedDiffRunner struct {
	mu          sync.Mutex
	inFlight    int
	peak        int
	options     untrackedDiffRunnerOptions
	filled      chan struct{}
	fillOnce    sync.Once
	release     chan struct{}
	releaseOnce sync.Once
	// overflow records that more than MaxConcurrentUntrackedDiffs per-file
	// diffs were ever in flight at the same time; overCap is closed on the
	// first such observation so a test can fail deterministically instead of
	// sampling a timing window.
	overflow    bool
	overCap     chan struct{}
	overCapOnce sync.Once
}

func newUntrackedDiffRunner(options untrackedDiffRunnerOptions) *untrackedDiffRunner {
	if options.trackedExit == 0 {
		// 1 is "success with a diff" for git diff.
		options.trackedExit = 1
	}
	return &untrackedDiffRunner{
		options: options,
		filled:  make(chan struct{}),
		release: make(chan struct{}),
		overCap: make(chan struct{}),
	}
}

func (r *untrackedDiffRunner) releaseAll() {
	r.releaseOnce.Do(func() { close(r.release) })
}

func (r *untrackedDiffRunner) peakInFlight() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.peak
}

// capViolated reports whether any observation saw more than
// MaxConcurrentUntrackedDiffs per-file diffs in flight at once.
func (r *untrackedDiffRunner) capViolated() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.overflow
}

func (r *untrackedDiffRunner) RunWorkspaceCommand(_ context.Context, command WorkspaceCommand) (WorkspaceCommandOutput, error) {
	subcommand, args := gitDiffTestSubcommand(command.Argv)
	joined := strings.Join(command.Argv, " ")
	switch {
	case subcommand == "rev-parse":
		return WorkspaceCommandOutput{ExitCode: 0, Stdout: "true\n"}, nil
	case subcommand == "config":
		// No fsmonitor and no diff-filter overrides.
		return WorkspaceCommandOutput{ExitCode: 1}, nil
	case subcommand == "ls-files":
		return WorkspaceCommandOutput{
			ExitCode: r.options.untrackedListExit,
			Stdout:   strings.Join(r.options.files, "\n") + "\n",
		}, nil
	case strings.Contains(joined, "--no-index"):
		file := args[len(args)-1]
		r.mu.Lock()
		r.inFlight++
		if r.inFlight > r.peak {
			r.peak = r.inFlight
		}
		if r.options.target > 0 && r.inFlight >= r.options.target {
			r.fillOnce.Do(func() { close(r.filled) })
		}
		if r.inFlight > MaxConcurrentUntrackedDiffs {
			r.overflow = true
			r.overCapOnce.Do(func() { close(r.overCap) })
		}
		r.mu.Unlock()

		if r.options.gated {
			<-r.release
		}
		if r.options.skew > 0 {
			index := len(r.options.files)
			for i, name := range r.options.files {
				if name == file {
					index = i
				}
			}
			time.Sleep(time.Duration(len(r.options.files)-index) * r.options.skew)
		}

		r.mu.Lock()
		r.inFlight--
		r.mu.Unlock()

		if r.options.failSpawn[file] {
			return WorkspaceCommandOutput{}, fmt.Errorf("git %s failed to spawn", file)
		}
		if r.options.failExit[file] {
			return WorkspaceCommandOutput{ExitCode: 2, Stdout: "failed-output\n"}, nil
		}
		return WorkspaceCommandOutput{ExitCode: 1, Stdout: file + "\n"}, nil
	default:
		return WorkspaceCommandOutput{ExitCode: r.options.trackedExit, Stdout: "tracked\n"}, nil
	}
}

func gitDiffTestSubcommand(argv []string) (string, []string) {
	rest := argv
	for len(rest) > 0 {
		if rest[0] == "git" {
			rest = rest[1:]
			continue
		}
		if rest[0] == "-c" && len(rest) > 2 {
			rest = rest[2:]
			continue
		}
		return rest[0], rest
	}
	return "", nil
}

func untrackedDiffTestFiles(count int) []string {
	files := make([]string, 0, count)
	for index := 0; index < count; index++ {
		files = append(files, fmt.Sprintf("file-%02d.txt", index))
	}
	return files
}

func expectedUntrackedDiff(files []string, skipped map[string]bool) string {
	var builder strings.Builder
	builder.WriteString("tracked\n")
	for _, file := range files {
		if skipped[file] {
			continue
		}
		builder.WriteString(file + "\n")
	}
	return builder.String()
}

// Rust bounds one collection to MAX_CONCURRENT_UNTRACKED_DIFFS = 8
// (git-utils/src/info.rs:43, buffered at info.rs:761): twelve untracked files
// must never put more than eight diffs in flight at once.
func TestReadGitDiffBoundsUntrackedDiffConcurrencyLikeRust(t *testing.T) {
	files := untrackedDiffTestFiles(12)
	runner := newUntrackedDiffRunner(untrackedDiffRunnerOptions{
		files:  files,
		gated:  true,
		target: MaxConcurrentUntrackedDiffs,
	})

	type result struct {
		diff   string
		isRepo bool
		err    error
	}
	done := make(chan result, 1)
	go func() {
		diff, isRepo, err := ReadGitDiffWithRunner(context.Background(), runner, "")
		done <- result{diff: diff, isRepo: isRepo, err: err}
	}()

	select {
	case <-runner.filled:
	case <-time.After(10 * time.Second):
		runner.releaseAll()
		got := <-done
		t.Fatalf("untracked diffs never reached %d in flight (peak %d, err %v)", MaxConcurrentUntrackedDiffs, runner.peakInFlight(), got.err)
	}
	// Every in-flight diff is parked inside the runner, so a bounded collector
	// can never start a ninth one. Waiting for that violation is an event wait
	// rather than a timing assertion: with the semaphore in place it cannot
	// fire no matter how slowly the machine runs.
	select {
	case <-runner.overCap:
		runner.releaseAll()
		<-done
		t.Fatalf("peak in-flight untracked diffs = %d, want at most %d", runner.peakInFlight(), MaxConcurrentUntrackedDiffs)
	case <-time.After(3 * time.Second):
	}
	if peak := runner.peakInFlight(); peak > MaxConcurrentUntrackedDiffs {
		runner.releaseAll()
		<-done
		t.Fatalf("peak in-flight untracked diffs = %d, want at most %d", peak, MaxConcurrentUntrackedDiffs)
	}
	runner.releaseAll()
	got := <-done
	if got.err != nil || !got.isRepo {
		t.Fatalf("ReadGitDiffWithRunner = (%v, %q, %v)", got.isRepo, got.diff, got.err)
	}
	// Twelve files with eight slots must fill every slot exactly once.
	if peak := runner.peakInFlight(); peak != MaxConcurrentUntrackedDiffs {
		t.Fatalf("peak in-flight untracked diffs = %d, want exactly %d", peak, MaxConcurrentUntrackedDiffs)
	}
	if want := expectedUntrackedDiff(files, nil); got.diff != want {
		t.Fatalf("diff = %q, want %q", got.diff, want)
	}
}

// The patches follow the ls-files order even though the diffs finish out of
// order (later files finish first here), matching Rust's buffered append.
func TestReadGitDiffPreservesUntrackedDiffOrderLikeRust(t *testing.T) {
	files := untrackedDiffTestFiles(12)
	runner := newUntrackedDiffRunner(untrackedDiffRunnerOptions{
		files: files,
		skew:  3 * time.Millisecond,
	})
	diff, isRepo, err := ReadGitDiffWithRunner(context.Background(), runner, "")
	if err != nil || !isRepo {
		t.Fatalf("ReadGitDiffWithRunner = (%v, %q, %v)", isRepo, diff, err)
	}
	if want := expectedUntrackedDiff(files, nil); diff != want {
		t.Fatalf("diff = %q, want %q", diff, want)
	}
	if peak := runner.peakInFlight(); peak > MaxConcurrentUntrackedDiffs {
		t.Fatalf("peak in-flight untracked diffs = %d, want at most %d", peak, MaxConcurrentUntrackedDiffs)
	}
	if runner.capViolated() {
		t.Fatal("untracked diffs ran over the concurrency bound")
	}
}

// A file whose diff fails only skips that file: the collection still returns
// the tracked patch and every other untracked patch (git-utils/src/info.rs:764-770,
// including the failing file-04 vector of diff_tests.rs:232-257).
func TestReadGitDiffSkipsFailedUntrackedDiffsLikeRust(t *testing.T) {
	files := untrackedDiffTestFiles(12)
	runner := newUntrackedDiffRunner(untrackedDiffRunnerOptions{
		files:     files,
		failExit:  map[string]bool{"file-04.txt": true, "file-08.txt": true},
		failSpawn: map[string]bool{"file-10.txt": true},
	})
	diff, isRepo, err := ReadGitDiffWithRunner(context.Background(), runner, "")
	if err != nil {
		t.Fatalf("a failed untracked diff must not fail the collection: %v", err)
	}
	if !isRepo {
		t.Fatal("ReadGitDiffWithRunner reported a non-git repo")
	}
	skipped := map[string]bool{"file-04.txt": true, "file-08.txt": true, "file-10.txt": true}
	if want := expectedUntrackedDiff(files, skipped); diff != want {
		t.Fatalf("diff = %q, want %q", diff, want)
	}
}

// The tracked diff and the ls-files listing keep Rust's failure semantics: a
// status outside 0/1 still fails the whole read.
func TestReadGitDiffKeepsTrackedAndUntrackedListFailureSemantics(t *testing.T) {
	trackedFailure := newUntrackedDiffRunner(untrackedDiffRunnerOptions{
		files:       []string{"new.txt"},
		trackedExit: 2,
	})
	if _, isRepo, err := ReadGitDiffWithRunner(context.Background(), trackedFailure, ""); err == nil || !isRepo {
		t.Fatalf("tracked diff failure = (%v, %v), want an error", isRepo, err)
	}

	listFailure := newUntrackedDiffRunner(untrackedDiffRunnerOptions{
		files:             []string{"new.txt"},
		untrackedListExit: 1,
	})
	if _, isRepo, err := ReadGitDiffWithRunner(context.Background(), listFailure, ""); err == nil || !isRepo {
		t.Fatalf("ls-files failure = (%v, %v), want an error", isRepo, err)
	}
}
