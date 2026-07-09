package tui

import (
	"fmt"
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
