//go:build windows

package tool

import (
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"

	"codex_go/envutil"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
	windowsunified "codex_go/sandbox/windowssandbox/unified_exec"
)

type startedUnifiedExecCommand struct {
	stdin   io.WriteCloser
	readers []io.ReadCloser
}

func startUnifiedExecWindowsSandboxCommand(req *ShellRequest) (*startedUnifiedExecSandboxCommand, error) {
	if req == nil || req.PermissionProfile == nil {
		return nil, windowssandbox.ErrInvalidRequest
	}
	// Rust #46271: a selected native MXC backend fails preparation when it is
	// unavailable instead of falling back to the legacy restricted-token path.
	if req.WindowsSandboxLevel == sandbox.WindowsSandboxMxc {
		return nil, errors.New("native MXC is unavailable on this executor")
	}
	codexHome := strings.TrimSpace(defaultLocalShellRunnerCodexHome())
	if codexHome == "" {
		return nil, fmt.Errorf("codex home is required for Windows sandbox unified exec")
	}
	env := shellRequestEnv(req)
	if profileID := strings.TrimSpace(req.PermissionProfileID); profileID != "" {
		env["CODEX_PERMISSION_PROFILE"] = profileID
	}
	level := windowsUnifiedExecSandboxLevel(req.PermissionProfile, req.WindowsSandboxLevel)
	session, err := windowsunified.SpawnWindowsSandboxLiveSessionForLevel(&windowsunified.WindowsSandboxSessionRequest{
		Capture: windowssandbox.CaptureRequest{
			PermissionProfileID:    req.PermissionProfileID,
			PermissionProfile:      req.PermissionProfile,
			WorkspaceRoots:         windowsCaptureWorkspaceRoots(req.WorkspaceRoots, req.CWD),
			CodexHome:              codexHome,
			Command:                append([]string(nil), req.Command...),
			CWD:                    req.CWD,
			Env:                    env,
			TTY:                    req.TTY,
			StdinOpen:              req.TTY,
			ProxyEnforced:          req.EnforceManagedNetwork,
			ProxySettingsMode:      windowsSandboxProxySettingsMode(req.WindowsSandboxProxySettingsMode),
			DisallowSetupElevation: req.ApprovalPolicy == sandbox.ApprovalNever,
		},
		PTY:                 req.TTY,
		WindowsSandboxLevel: level,
		ProxyEnforced:       req.EnforceManagedNetwork,
	})
	if err != nil {
		return nil, err
	}
	return &startedUnifiedExecSandboxCommand{
		process: session,
		stdin:   session.Stdin,
		readers: append([]io.ReadCloser(nil), session.Readers...),
	}, nil
}

func windowsUnifiedExecSandboxLevel(profile *sandbox.PermissionProfile, configured sandbox.WindowsSandboxLevel) windowsunified.WindowsSandboxLevel {
	if windowsShellSandboxUsesElevated(profile, configured) {
		return windowsunified.WindowsSandboxLevelElevated
	}
	return windowsunified.WindowsSandboxLevelLegacy
}

func startUnifiedExecCommand(cmd *osexec.Cmd, tty bool) (*startedUnifiedExecCommand, error) {
	// Rust #48483: a piped unified-exec child must not allocate a console window
	// when the runner itself has no console (shared app-server daemon).
	envutil.SuppressConsoleWindow(cmd)
	// Own the output pipes instead of using cmd.StdoutPipe: os/exec's Wait
	// closes the pipes it created as soon as the child exits, so a process that
	// writes and exits quickly can lose its buffered output before the reader
	// goroutines drain it.
	stdout, stdoutWriter, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	stderr, stderrWriter, err := os.Pipe()
	if err != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		return nil, err
	}
	cmd.Stdout = stdoutWriter
	cmd.Stderr = stderrWriter
	var stdin io.WriteCloser
	if tty {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			_ = stdout.Close()
			_ = stdoutWriter.Close()
			_ = stderr.Close()
			_ = stderrWriter.Close()
			return nil, err
		}
	}
	if err := cmd.Start(); err != nil {
		if stdin != nil {
			_ = stdin.Close()
		}
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		_ = stderr.Close()
		_ = stderrWriter.Close()
		return nil, err
	}
	// The child owns its duplicated ends now; the parent's copies must close or
	// the readers never see EOF.
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	return &startedUnifiedExecCommand{stdin: stdin, readers: []io.ReadCloser{stdout, stderr}}, nil
}

func interruptUnifiedExecProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}

func terminateUnifiedExecProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	return process.Kill()
}

func unifiedExecExitCode(state *os.ProcessState, err error) int {
	_ = err
	if state == nil {
		return -1
	}
	return state.ExitCode()
}
