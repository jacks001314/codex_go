package appserverdaemon

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"

	"codex_go/install"
)

const (
	InitialUpdateDelay    = 5 * time.Minute
	RestartRetryInterval  = 50 * time.Millisecond
	UpdateInterval        = time.Hour
	InstallScriptEndpoint = "https://chatgpt.com/codex/install.sh"
)

type UpdateLoopControl string

const (
	UpdateLoopContinue UpdateLoopControl = "continue"
	UpdateLoopStop     UpdateLoopControl = "stop"
)

type UpdateLoopOptions struct {
	InitialDelay  time.Duration
	UpdateDelay   time.Duration
	RetryDelay    time.Duration
	InstallLatest func(context.Context) error
	CurrentExe    func() (string, error)
	ReadFile      func(string) ([]byte, error)
	ReexecUpdater func(string) error
}

func DefaultUpdateLoopOptions() *UpdateLoopOptions {
	return &UpdateLoopOptions{
		InitialDelay:  InitialUpdateDelay,
		UpdateDelay:   UpdateInterval,
		RetryDelay:    RestartRetryInterval,
		InstallLatest: InstallLatestStandalone,
		CurrentExe:    os.Executable,
		ReadFile:      os.ReadFile,
		ReexecUpdater: ReexecManagedUpdater,
	}
}

func RunPIDUpdateLoop(ctx context.Context, runner *LifecycleRunner, options *UpdateLoopOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if runner == nil {
		runner = &LifecycleRunner{}
	}
	options = normalizeUpdateLoopOptions(options)
	runningIdentity, err := CurrentUpdaterIdentity(options)
	if err != nil {
		return err
	}
	if sleepOrDone(ctx, options.InitialDelay) {
		return nil
	}
	for {
		control, err := UpdateOnce(ctx, runner, runningIdentity, options)
		if err != nil && !errors.Is(err, context.Canceled) {
			// The Rust updater keeps running after transient update failures.
		}
		if err != nil && errors.Is(err, context.Canceled) {
			return nil
		}
		if control == UpdateLoopStop {
			return nil
		}
		if sleepOrDone(ctx, options.UpdateDelay) {
			return nil
		}
	}
}

func UpdateOnce(ctx context.Context, runner *LifecycleRunner, runningIdentity *install.ExecutableIdentity, options *UpdateLoopOptions) (UpdateLoopControl, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if runner == nil {
		runner = &LifecycleRunner{}
	}
	options = normalizeUpdateLoopOptions(options)
	if err := options.InstallLatest(ctx); err != nil {
		return UpdateLoopContinue, err
	}
	managedBin := runner.daemon().Paths.ManagedCodexBin
	managedIdentity, err := ManagedExecutableIdentity(managedBin, options)
	if err != nil {
		return UpdateLoopContinue, err
	}
	restartMode, updaterRefreshMode := UpdateModesForIdentities(runningIdentity, managedIdentity)
	for {
		if ctx.Err() != nil {
			return UpdateLoopStop, ctx.Err()
		}
		outcome, err := runner.TryRestartIfRunning(restartMode, updaterRefreshMode, managedBin)
		if err != nil {
			return UpdateLoopContinue, err
		}
		if ShouldReexecUpdater(updaterRefreshMode, outcome) {
			return UpdateLoopStop, options.ReexecUpdater(managedBin)
		}
		if outcome != RestartBusy {
			return UpdateLoopContinue, nil
		}
		if sleepOrDone(ctx, options.RetryDelay) {
			return UpdateLoopStop, nil
		}
	}
}

func CurrentUpdaterIdentity(options *UpdateLoopOptions) (*install.ExecutableIdentity, error) {
	options = normalizeUpdateLoopOptions(options)
	currentExe, err := options.CurrentExe()
	if err != nil {
		return nil, fmt.Errorf("failed to resolve current updater executable: %w", err)
	}
	return ManagedExecutableIdentity(currentExe, options)
}

func ManagedExecutableIdentity(path string, options *UpdateLoopOptions) (*install.ExecutableIdentity, error) {
	options = normalizeUpdateLoopOptions(options)
	data, err := options.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read executable identity for %s: %w", path, err)
	}
	identity := install.ExecutableIdentityFromBytes(data)
	return &identity, nil
}

func InstallLatestStandalone(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if runtime.GOOS == "windows" {
		action := install.UpdateAction{Kind: install.UpdateActionStandaloneWin}
		command, args := action.CommandArgs()
		return runWindowsUpdateInstaller(ctx, command, args)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, InstallScriptEndpoint, nil)
	if err != nil {
		return err
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return fmt.Errorf("failed to fetch standalone Codex updater: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("standalone Codex updater request failed: %s", response.Status)
	}
	child := exec.CommandContext(ctx, "/bin/sh", "-s")
	stdin, err := child.StdinPipe()
	if err != nil {
		return fmt.Errorf("standalone Codex updater stdin was unavailable: %w", err)
	}
	child.Stdout = io.Discard
	child.Stderr = io.Discard
	if err := child.Start(); err != nil {
		return fmt.Errorf("failed to invoke standalone Codex updater: %w", err)
	}
	if _, err := io.Copy(stdin, response.Body); err != nil {
		_ = stdin.Close()
		_ = child.Wait()
		return fmt.Errorf("failed to pass standalone Codex updater to shell: %w", err)
	}
	if err := stdin.Close(); err != nil {
		_ = child.Wait()
		return fmt.Errorf("failed to close standalone Codex updater stdin: %w", err)
	}
	if err := child.Wait(); err != nil {
		return fmt.Errorf("standalone Codex updater exited with error: %w", err)
	}
	return nil
}

func ReexecManagedUpdater(managedCodexBin string) error {
	if managedCodexBin == "" {
		return fmt.Errorf("managed Codex binary path is empty")
	}
	return exec.Command(managedCodexBin, "app-server", "daemon", "pid-update-loop").Run()
}

func ShouldReexecUpdater(mode UpdaterRefreshMode, outcome RestartIfRunningOutcome) bool {
	return mode == UpdaterRefreshReexecIfManagedBinaryChanged && outcome == RestartRestarted
}

func normalizeUpdateLoopOptions(options *UpdateLoopOptions) *UpdateLoopOptions {
	if options == nil {
		return DefaultUpdateLoopOptions()
	}
	defaults := DefaultUpdateLoopOptions()
	if options.InitialDelay == 0 {
		options.InitialDelay = defaults.InitialDelay
	}
	if options.UpdateDelay == 0 {
		options.UpdateDelay = defaults.UpdateDelay
	}
	if options.RetryDelay == 0 {
		options.RetryDelay = defaults.RetryDelay
	}
	if options.InstallLatest == nil {
		options.InstallLatest = defaults.InstallLatest
	}
	if options.CurrentExe == nil {
		options.CurrentExe = defaults.CurrentExe
	}
	if options.ReadFile == nil {
		options.ReadFile = defaults.ReadFile
	}
	if options.ReexecUpdater == nil {
		options.ReexecUpdater = defaults.ReexecUpdater
	}
	return options
}

func sleepOrDone(ctx context.Context, duration time.Duration) bool {
	if duration <= 0 {
		return false
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return true
	case <-timer.C:
		return false
	}
}
