//go:build !windows

package tool

import (
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"syscall"

	"github.com/creack/pty"
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
		terminal, err := pty.Start(cmd)
		if err != nil {
			return nil, err
		}
		return &startedUnifiedExecCommand{stdin: terminal, readers: []io.ReadCloser{terminal}}, nil
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	return &startedUnifiedExecCommand{readers: []io.ReadCloser{stdout, stderr}}, nil
}

func interruptUnifiedExecProcess(process *os.Process) error {
	if process == nil {
		return ErrUnifiedExecStdinClosed
	}
	return process.Signal(os.Interrupt)
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
