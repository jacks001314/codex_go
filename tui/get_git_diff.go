package tui

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"sort"
	"strings"
	"time"
)

// Rust parity: codex-rs/tui/src/get_git_diff.rs.

const (
	GitDiffCommandTimeout         = 30 * time.Second
	ExecutableFilterConfigPattern = `^filter\..*\.(clean|process)$`
)

type GitDiffRequest struct {
	CWD     string
	Staged  bool
	Context int
}

type GitFsmonitorOverride int

const (
	GitFsmonitorDisabled GitFsmonitorOverride = iota
	GitFsmonitorBuiltIn
)

func (o GitFsmonitorOverride) GitConfigArg() string {
	switch o {
	case GitFsmonitorBuiltIn:
		return "core.fsmonitor=true"
	default:
		return "core.fsmonitor=false"
	}
}

type GitConfigOverride struct {
	Key   string
	Value string
}

type GitCommandResult struct {
	Stdout   string
	ExitCode int
}

type LocalWorkspaceCommandRunner struct{}

func (LocalWorkspaceCommandRunner) RunWorkspaceCommand(ctx context.Context, command WorkspaceCommand) (WorkspaceCommandOutput, error) {
	if err := command.Validate(); err != nil {
		return WorkspaceCommandOutput{}, err
	}
	timeout := command.Timeout
	if timeout <= 0 {
		timeout = DefaultWorkspaceCommandTimeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, command.Argv[0], command.Argv[1:]...)
	if strings.TrimSpace(command.CWD) != "" {
		cmd.Dir = strings.TrimSpace(command.CWD)
	}
	cmd.Env = workspaceCommandEnv(os.Environ(), command.Env)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if runCtx.Err() != nil {
		return WorkspaceCommandOutput{}, runCtx.Err()
	}
	output := WorkspaceCommandOutput{
		ExitCode: exitCodeFromError(err),
		Stdout:   commandOutputString(stdout.Bytes(), command),
		Stderr:   commandOutputString(stderr.Bytes(), command),
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return output, nil
		}
		return output, err
	}
	return output, nil
}

func DisableHooksConfig() string {
	if runtime.GOOS == "windows" {
		return "core.hooksPath=NUL"
	}
	return "core.hooksPath=/dev/null"
}

func GitNullDevice() string {
	if runtime.GOOS == "windows" {
		return "NUL"
	}
	return "/dev/null"
}

func BuildGitDiffCommand(cwd string, fsmonitor GitFsmonitorOverride, configOverrides []GitConfigOverride, args ...string) WorkspaceCommand {
	argv := []string{"git", "-c", fsmonitor.GitConfigArg(), "-c", DisableHooksConfig()}
	argv = append(argv, args...)
	command := NewWorkspaceCommand(argv...).
		WithCWD(cwd).
		WithTimeout(GitDiffCommandTimeout).
		WithDisabledOutputCap()
	if len(configOverrides) > 0 {
		command = command.WithEnv("GIT_CONFIG_COUNT", fmt.Sprintf("%d", len(configOverrides)))
		for index, override := range configOverrides {
			command = command.
				WithEnv(fmt.Sprintf("GIT_CONFIG_KEY_%d", index), override.Key).
				WithEnv(fmt.Sprintf("GIT_CONFIG_VALUE_%d", index), override.Value)
		}
	}
	return command
}

func BuildGitProbeCommand(cwd string, args ...string) WorkspaceCommand {
	argv := append([]string{"git"}, args...)
	return NewWorkspaceCommand(argv...).WithCWD(cwd)
}

func ReadGitDiff(cwd string) (string, bool, error) {
	return ReadGitDiffWithRunner(context.Background(), LocalWorkspaceCommandRunner{}, cwd)
}

func ReadGitDiffWithRunner(ctx context.Context, runner WorkspaceCommandRunner, cwd string) (string, bool, error) {
	if runner == nil {
		runner = LocalWorkspaceCommandRunner{}
	}
	cwd = strings.TrimSpace(cwd)
	inside, err := insideGitRepo(ctx, runner, cwd)
	if err != nil {
		return "", false, err
	}
	if !inside {
		return "", false, nil
	}

	fsmonitor := DetectGitFsmonitorOverride(ctx, runner, cwd)
	overrides, err := diffFilterConfigOverridesForGit(ctx, runner, cwd, fsmonitor)
	if err != nil {
		return "", true, err
	}
	trackedDiff, err := runGitCaptureDiff(ctx, runner, cwd, fsmonitor, overrides, GitTrackedDiffArgs())
	if err != nil {
		return "", true, err
	}
	untrackedOutput, err := runGitCaptureStdout(ctx, runner, cwd, fsmonitor, nil, GitUntrackedListArgs())
	if err != nil {
		return "", true, err
	}
	untrackedDiffs := []string{}
	for _, file := range ParseUntrackedGitFiles(untrackedOutput) {
		diff, err := runGitCaptureDiff(ctx, runner, cwd, fsmonitor, overrides, GitUntrackedDiffArgs(file))
		if err != nil {
			return "", true, err
		}
		untrackedDiffs = append(untrackedDiffs, diff)
	}
	return ComposeGitDiff(trackedDiff, untrackedDiffs), true, nil
}

func DetectGitFsmonitorOverride(ctx context.Context, runner WorkspaceCommandRunner, cwd string) GitFsmonitorOverride {
	configBytes, ok := runGitProbe(ctx, runner, cwd, "config", "--null", "--get", "core.fsmonitor")
	if !ok || !bytes.HasSuffix(configBytes, []byte{0}) {
		return GitFsmonitorDisabled
	}
	configBytes = bytes.TrimSuffix(configBytes, []byte{0})
	if bytes.Contains(configBytes, []byte{0}) {
		return GitFsmonitorDisabled
	}
	config := string(configBytes)
	configured := false
	switch {
	case strings.EqualFold(config, "true"), strings.EqualFold(config, "yes"), strings.EqualFold(config, "on"):
		configured = true
	case strings.EqualFold(config, "false"), strings.EqualFold(config, "no"), strings.EqualFold(config, "off"):
		configured = false
	default:
		typed, ok := runGitProbe(ctx, runner, cwd, "config", "--null", "--type=bool", "--fixed-value", "--get", "core.fsmonitor", config)
		configured = ok && bytes.Equal(typed, []byte("true\x00"))
	}
	if !configured {
		return GitFsmonitorDisabled
	}
	buildOptions, ok := runGitProbe(ctx, runner, cwd, "version", "--build-options")
	if !ok {
		return GitFsmonitorDisabled
	}
	for _, line := range bytes.Split(buildOptions, []byte{'\n'}) {
		if string(bytes.TrimSpace(line)) == "feature: fsmonitor--daemon" {
			return GitFsmonitorBuiltIn
		}
	}
	return GitFsmonitorDisabled
}

func GitTrackedDiffArgs() []string {
	return []string{
		"diff",
		"--no-textconv",
		"--no-ext-diff",
		"--submodule=short",
		"--ignore-submodules=dirty",
		"--color",
	}
}

func insideGitRepo(ctx context.Context, runner WorkspaceCommandRunner, cwd string) (bool, error) {
	output, err := runner.RunWorkspaceCommand(ctx, BuildGitProbeCommand(cwd, "rev-parse", "--is-inside-work-tree"))
	if err != nil {
		return false, err
	}
	return output.Success(), nil
}

func diffFilterConfigOverridesForGit(ctx context.Context, runner WorkspaceCommandRunner, cwd string, fsmonitor GitFsmonitorOverride) ([]GitConfigOverride, error) {
	args := []string{"config", "--null", "--name-only", "--get-regexp", ExecutableFilterConfigPattern}
	output, err := runGitCommand(ctx, runner, cwd, fsmonitor, nil, args)
	if err != nil {
		return nil, err
	}
	if output.ExitCode != 0 && output.ExitCode != 1 {
		return nil, fmt.Errorf("git %s failed with status %d", quoteArgs(args), output.ExitCode)
	}
	return DiffFilterConfigOverrides(output.Stdout), nil
}

func runGitCaptureStdout(ctx context.Context, runner WorkspaceCommandRunner, cwd string, fsmonitor GitFsmonitorOverride, configOverrides []GitConfigOverride, args []string) (string, error) {
	output, err := runGitCommand(ctx, runner, cwd, fsmonitor, configOverrides, args)
	if err != nil {
		return "", err
	}
	return GitCaptureStdout(args, GitCommandResult{Stdout: output.Stdout, ExitCode: output.ExitCode})
}

func runGitCaptureDiff(ctx context.Context, runner WorkspaceCommandRunner, cwd string, fsmonitor GitFsmonitorOverride, configOverrides []GitConfigOverride, args []string) (string, error) {
	output, err := runGitCommand(ctx, runner, cwd, fsmonitor, configOverrides, args)
	if err != nil {
		return "", err
	}
	return GitCaptureDiff(args, GitCommandResult{Stdout: output.Stdout, ExitCode: output.ExitCode})
}

func runGitCommand(ctx context.Context, runner WorkspaceCommandRunner, cwd string, fsmonitor GitFsmonitorOverride, configOverrides []GitConfigOverride, args []string) (WorkspaceCommandOutput, error) {
	return runner.RunWorkspaceCommand(ctx, BuildGitDiffCommand(cwd, fsmonitor, configOverrides, args...))
}

func runGitProbe(ctx context.Context, runner WorkspaceCommandRunner, cwd string, args ...string) ([]byte, bool) {
	output, err := runner.RunWorkspaceCommand(ctx, BuildGitProbeCommand(cwd, args...))
	if err != nil || !output.Success() {
		return nil, false
	}
	return []byte(output.Stdout), true
}

func GitUntrackedListArgs() []string {
	return []string{"ls-files", "--others", "--exclude-standard"}
}

func GitUntrackedDiffArgs(file string) []string {
	return []string{
		"diff",
		"--no-textconv",
		"--no-ext-diff",
		"--submodule=short",
		"--ignore-submodules=dirty",
		"--color",
		"--no-index",
		"--",
		GitNullDevice(),
		strings.TrimSpace(file),
	}
}

func DiffFilterConfigOverrides(configStdout string) []GitConfigOverride {
	drivers := map[string]bool{}
	for _, key := range strings.Split(configStdout, "\x00") {
		key = strings.TrimSpace(key)
		if key == "" {
			continue
		}
		if driver, ok := strings.CutSuffix(key, ".clean"); ok {
			drivers[driver] = true
			continue
		}
		if driver, ok := strings.CutSuffix(key, ".process"); ok {
			drivers[driver] = true
		}
	}

	names := make([]string, 0, len(drivers))
	for driver := range drivers {
		names = append(names, driver)
	}
	sort.Strings(names)

	overrides := make([]GitConfigOverride, 0, len(names)*3)
	for _, driver := range names {
		overrides = append(overrides,
			GitConfigOverride{Key: driver + ".clean"},
			GitConfigOverride{Key: driver + ".process"},
			GitConfigOverride{Key: driver + ".required", Value: "false"},
		)
	}
	return overrides
}

func GitCaptureStdout(args []string, result GitCommandResult) (string, error) {
	if result.ExitCode == 0 {
		return result.Stdout, nil
	}
	return "", fmt.Errorf("git %s failed with status %d", quoteArgs(args), result.ExitCode)
}

func GitCaptureDiff(args []string, result GitCommandResult) (string, error) {
	if result.ExitCode == 0 || result.ExitCode == 1 {
		return result.Stdout, nil
	}
	return "", fmt.Errorf("git %s failed with status %d", quoteArgs(args), result.ExitCode)
}

func ParseUntrackedGitFiles(stdout string) []string {
	files := []string{}
	for _, line := range strings.Split(stdout, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			files = append(files, line)
		}
	}
	return files
}

func ComposeGitDiff(trackedDiff string, untrackedDiffs []string) string {
	var builder strings.Builder
	builder.WriteString(trackedDiff)
	for _, diff := range untrackedDiffs {
		builder.WriteString(diff)
	}
	return builder.String()
}

func quoteArgs(args []string) string {
	quoted := make([]string, 0, len(args))
	for _, arg := range args {
		quoted = append(quoted, fmt.Sprintf("%q", arg))
	}
	return "[" + strings.Join(quoted, ", ") + "]"
}

func workspaceCommandEnv(base []string, updates map[string]*string) []string {
	if len(updates) == 0 {
		return base
	}
	values := map[string]string{}
	order := []string{}
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok {
			continue
		}
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		values[key] = value
	}
	updateKeys := make([]string, 0, len(updates))
	for key := range updates {
		updateKeys = append(updateKeys, key)
	}
	sort.Strings(updateKeys)
	for _, key := range updateKeys {
		value := updates[key]
		if value == nil {
			delete(values, key)
			continue
		}
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		values[key] = *value
	}
	out := make([]string, 0, len(values))
	for _, key := range order {
		value, ok := values[key]
		if !ok {
			continue
		}
		out = append(out, key+"="+value)
	}
	return out
}

func exitCodeFromError(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func commandOutputString(data []byte, command WorkspaceCommand) string {
	if command.DisableOutputCap || command.OutputBytesCap <= 0 || len(data) <= command.OutputBytesCap {
		return string(data)
	}
	return string(data[:command.OutputBytesCap])
}
