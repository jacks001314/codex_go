//go:build windows

package tool

import (
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"strings"

	"codex_go/internal/sandbox"
	"codex_go/internal/sandbox/windowssandbox"
	windowsunified "codex_go/internal/sandbox/windowssandbox/unified_exec"
)

type startedUnifiedExecCommand struct {
	stdin   io.WriteCloser
	readers []io.ReadCloser
}

func startUnifiedExecWindowsSandboxCommand(req *ShellRequest) (*startedUnifiedExecSandboxCommand, error) {
	if req == nil || req.PermissionProfile == nil {
		return nil, windowssandbox.ErrInvalidRequest
	}
	codexHome := strings.TrimSpace(defaultLocalShellRunnerCodexHome())
	if codexHome == "" {
		return nil, fmt.Errorf("codex home is required for Windows sandbox unified exec")
	}
	env := shellRunnerEnvMap(os.Environ(), req.Env)
	if profileID := strings.TrimSpace(req.PermissionProfileID); profileID != "" {
		env["CODEX_PERMISSION_PROFILE"] = profileID
	}
	level := windowsUnifiedExecSandboxLevel(req.PermissionProfile, req.WindowsSandboxLevel)
	session, err := windowsunified.SpawnWindowsSandboxLiveSessionForLevel(&windowsunified.WindowsSandboxSessionRequest{
		Capture: windowssandbox.CaptureRequest{
			PermissionProfileID: req.PermissionProfileID,
			PermissionProfile:   req.PermissionProfile,
			WorkspaceRoots:      []string{req.CWD},
			CodexHome:           codexHome,
			Command:             append([]string(nil), req.Command...),
			CWD:                 req.CWD,
			Env:                 env,
			TTY:                 req.TTY,
			StdinOpen:           req.TTY,
			UsePrivateDesktop:   req.WindowsSandboxPrivateDesktop,
		},
		PTY:                 req.TTY,
		WindowsSandboxLevel: level,
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
	if configured == sandbox.WindowsSandboxElevated || (profile != nil && profile.HasDenyReadEntries()) {
		return windowsunified.WindowsSandboxLevelElevated
	}
	return windowsunified.WindowsSandboxLevelLegacy
}

func startUnifiedExecCommand(cmd *osexec.Cmd, tty bool) (*startedUnifiedExecCommand, error) {
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	var stdin io.WriteCloser
	if tty {
		stdin, err = cmd.StdinPipe()
		if err != nil {
			return nil, err
		}
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &startedUnifiedExecCommand{stdin: stdin, readers: []io.ReadCloser{stdout, stderr}}, nil
}

func interruptUnifiedExecProcess(process *os.Process) error {
	_ = process
	return fmt.Errorf("interrupt is not supported on windows for non-tty unified exec sessions")
}

func unifiedExecExitCode(state *os.ProcessState, err error) int {
	_ = err
	if state == nil {
		return -1
	}
	return state.ExitCode()
}
