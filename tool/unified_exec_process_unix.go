//go:build !windows

package tool

import (
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"syscall"

	"github.com/creack/pty"

	"codex_go/processgroup"
	"codex_go/ptyio"
)

func startUnifiedExecWindowsSandboxCommand(req *ShellRequest) (*startedUnifiedExecSandboxCommand, error) {
	_ = req
	return nil, fmt.Errorf("Windows sandbox unified exec is unavailable on this platform")
}

type startedUnifiedExecCommand struct {
	stdin   io.WriteCloser
	readers []io.ReadCloser
}

func startUnifiedExecCommand(cmd *osexec.Cmd, tty bool) (*startedUnifiedExecCommand, error) {
	if tty {
		// Rust #47605: the launch carries an explicit descriptor policy, so the
		// terminal child starts without the daemon's unrelated descriptors.
		restoreDescriptors := processgroup.HideInheritedDescriptors()
		terminal, err := pty.Start(cmd)
		restoreDescriptors()
		if err != nil {
			return nil, err
		}
		// Rust bf3eb2ec91 (#40460): the master is driven by readiness, so a read
		// parked on a detached child can be woken instead of outliving the session.
		master, masterErr := ptyio.New(terminal)
		if masterErr != nil {
			_ = terminal.Close()
			return nil, masterErr
		}
		return &startedUnifiedExecCommand{stdin: master, readers: []io.ReadCloser{master}}, nil
	}
	// Rust #47605: piped unified-exec children go through the shared child
	// launcher with ProcessMode::NewSession and Linux parent-death termination.
	processgroup.PrepareChildSession(cmd)
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
	restoreDescriptors := processgroup.HideInheritedDescriptors()
	startErr := cmd.Start()
	restoreDescriptors()
	if startErr != nil {
		_ = stdout.Close()
		_ = stdoutWriter.Close()
		_ = stderr.Close()
		_ = stderrWriter.Close()
		return nil, startErr
	}
	// The child owns its duplicated ends now; the parent's copies must close or
	// the readers never see EOF.
	_ = stdoutWriter.Close()
	_ = stderrWriter.Close()
	return &startedUnifiedExecCommand{readers: []io.ReadCloser{stdout, stderr}}, nil
}

func interruptUnifiedExecProcess(process *os.Process) error {
	if process == nil {
		return ErrUnifiedExecStdinClosed
	}
	return processgroup.InterruptProcessTree(process)
}

func terminateUnifiedExecProcess(process *os.Process) error {
	if process == nil {
		return nil
	}
	return processgroup.TerminateProcessTree(process)
}

func unifiedExecExitCode(state *os.ProcessState, err error) int {
	if state == nil {
		return -1
	}
	if status, ok := state.Sys().(syscall.WaitStatus); ok && status.Signaled() {
		return 128 + int(status.Signal())
	}
	return state.ExitCode()
}
