package tool

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"codex_go/execserver"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

type ShellRunner interface {
	Run(ctx context.Context, req *ShellRequest) (*ShellResult, error)
}

type LocalShellRunner struct{}

var (
	runWindowsShellSandboxCapture = func(capture *windowssandbox.CaptureRequest, elevated bool) (*windowssandbox.CaptureResult, error) {
		if elevated {
			return windowssandbox.RunWindowsSandboxCaptureForPermissionProfileElevated(
				&windowssandbox.ElevatedSandboxProfileCaptureRequest{Capture: *capture},
			)
		}
		return windowssandbox.RunWindowsSandboxCaptureWithFilesystemOverrides(capture)
	}
	defaultShellRunnerCodexHome = defaultLocalShellRunnerCodexHome
)

func NewLocalShellRunner() *LocalShellRunner {
	return &LocalShellRunner{}
}

func (r *LocalShellRunner) Run(ctx context.Context, req *ShellRequest) (*ShellResult, error) {
	if req == nil {
		return nil, errors.New("shell request is required")
	}
	if len(req.Command) == 0 {
		return nil, errors.New("command is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if req.TimeoutMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(req.TimeoutMS)*time.Millisecond)
		defer cancel()
	}

	started := time.Now()
	env := shellRunnerEnvMap(os.Environ(), req.Env)
	if req.PermissionProfile != nil {
		return r.runWithPermissionProfile(ctx, req, env, started)
	}
	return r.runDirect(ctx, req.Command, req.CWD, env, started)
}

func (r *LocalShellRunner) runWithPermissionProfile(ctx context.Context, req *ShellRequest, env map[string]string, started time.Time) (*ShellResult, error) {
	runReq := &sandbox.CommandRunRequest{
		ResolvedPermissionProfile:     req.PermissionProfile,
		ResolvedPermissionProfileID:   req.PermissionProfileID,
		ResolvedPermissionProfileJSON: req.PermissionProfileJSON,
		CWD:                           req.CWD,
		Command:                       append([]string(nil), req.Command...),
	}
	plan, err := sandbox.BuildCommandRunPlan(runReq)
	if err != nil {
		return nil, err
	}
	if err := plan.UnsupportedError(); err != nil {
		return nil, err
	}
	if strings.TrimSpace(plan.PermissionProfileID) != "" {
		env["CODEX_PERMISSION_PROFILE"] = plan.PermissionProfileID
	}
	var result *ShellResult
	if runtime.GOOS == "windows" && plan.PermissionProfile != nil && !plan.PermissionProfile.Disabled {
		result, err = r.runWindowsSandbox(ctx, req, plan, env, started)
	} else {
		result, err = r.runDirect(ctx, plan.Command, plan.CWD, env, started)
	}
	if err == nil && result != nil {
		sandbox.RecordFileSystemSandboxViolation(plan.SandboxType, sandbox.SandboxExecOutput{
			ExitCode:         result.ExitCode,
			Stdout:           result.Stdout,
			Stderr:           result.Stderr,
			AggregatedOutput: shellOutputText(result),
		})
	}
	return result, err
}

func (r *LocalShellRunner) runWindowsSandbox(ctx context.Context, req *ShellRequest, plan *sandbox.CommandRunPlan, env map[string]string, started time.Time) (*ShellResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	commandCWD := filepath.Clean(plan.CWD)
	if strings.TrimSpace(commandCWD) == "" {
		return nil, errors.New("windows sandbox cwd is required")
	}
	codexHome, err := shellRunnerAbsPath(defaultShellRunnerCodexHome())
	if err != nil {
		return nil, err
	}
	var timeout *int64
	if req.TimeoutMS > 0 {
		value := int64(req.TimeoutMS)
		timeout = &value
	}
	capture := &windowssandbox.CaptureRequest{
		PermissionProfileID:    plan.PermissionProfileID,
		PermissionProfile:      plan.PermissionProfile,
		WorkspaceRoots:         []string{commandCWD},
		CodexHome:              codexHome,
		Command:                append([]string(nil), plan.Command...),
		CWD:                    commandCWD,
		Env:                    env,
		TimeoutMS:              timeout,
		UsePrivateDesktop:      req.WindowsSandboxPrivateDesktop,
		TTY:                    req.TTY,
		ProxyEnforced:          req.EnforceManagedNetwork,
		ProxySettingsMode:      windowsSandboxProxySettingsMode(req.WindowsSandboxProxySettingsMode),
		DisallowSetupElevation: req.ApprovalPolicy == sandbox.ApprovalNever,
		Cancellation: windowssandbox.CancellationToken{
			IsCancelled: func() bool {
				return ctx.Err() != nil
			},
		},
	}
	result, err := runWindowsShellSandboxCapture(
		capture,
		windowsShellSandboxUsesElevated(plan.PermissionProfile, req.WindowsSandboxLevel, req.EnforceManagedNetwork),
	)
	if err != nil {
		return nil, err
	}
	if result == nil {
		return nil, errors.New("windows sandbox returned nil result")
	}
	return &ShellResult{
		ExitCode: result.ExitCode,
		Stdout:   string(result.Stdout),
		Stderr:   string(result.Stderr),
		Duration: time.Since(started),
		TimedOut: result.TimedOut || errors.Is(ctx.Err(), context.DeadlineExceeded),
	}, nil
}

func windowsShellSandboxUsesElevated(profile *sandbox.PermissionProfile, configured sandbox.WindowsSandboxLevel, proxyEnforced bool) bool {
	return proxyEnforced || configured == sandbox.WindowsSandboxElevated || profile != nil && profile.HasDenyReadEntries()
}

func windowsSandboxProxySettingsMode(mode execserver.WindowsSandboxProxySettingsMode) windowssandbox.ProxySettingsMode {
	if mode == execserver.WindowsSandboxProxySettingsPreserve {
		return windowssandbox.ProxySettingsPreserve
	}
	return windowssandbox.ProxySettingsReconcile
}

func (r *LocalShellRunner) runDirect(ctx context.Context, command []string, cwd string, env map[string]string, started time.Time) (*ShellResult, error) {
	cmd := exec.CommandContext(ctx, command[0], command[1:]...)
	cmd.Dir = cwd
	if len(env) > 0 {
		cmd.Env = envSlice(env)
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	result := &ShellResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		Duration: time.Since(started),
		TimedOut: errors.Is(ctx.Err(), context.DeadlineExceeded),
	}
	if cmd.ProcessState != nil {
		result.ExitCode = cmd.ProcessState.ExitCode()
	}
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return result, nil
		}
		if result.TimedOut {
			return result, nil
		}
		return result, err
	}
	return result, nil
}

func envSlice(values map[string]string) []string {
	out := make([]string, 0, len(values))
	for key, value := range values {
		out = append(out, key+"="+value)
	}
	return out
}

func shellRunnerEnvMap(base []string, overrides map[string]string) map[string]string {
	env := map[string]string{}
	for _, entry := range base {
		key, value, ok := strings.Cut(entry, "=")
		if !ok || key == "" {
			continue
		}
		env[key] = value
	}
	for key, value := range overrides {
		if strings.TrimSpace(key) == "" {
			continue
		}
		env[key] = value
	}
	return env
}

func shellRunnerAbsPath(path string) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return "", errors.New("path is required")
	}
	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func defaultLocalShellRunnerCodexHome() string {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		return value
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return ".codex"
	}
	return filepath.Join(home, ".codex")
}
