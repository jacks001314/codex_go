//go:build !windows

package execserver

import (
	"io"
	"os/exec"

	"github.com/creack/pty"
)

type startedExecServerTTY struct {
	stdin    io.WriteCloser
	reader   io.ReadCloser
	wait     func() (int, error)
	kill     func() error
	closePTY func() error
	cleanup  func() error
}

func startExecServerTTY(cmd *exec.Cmd) (*startedExecServerTTY, bool, error) {
	terminal, err := pty.Start(cmd)
	if err != nil {
		return nil, true, err
	}
	return &startedExecServerTTY{
		stdin:  terminal,
		reader: terminal,
		wait: func() (int, error) {
			err := cmd.Wait()
			code := -1
			if cmd.ProcessState != nil {
				code = cmd.ProcessState.ExitCode()
			}
			return code, err
		},
		kill: func() error {
			if cmd.Process == nil {
				return nil
			}
			return cmd.Process.Kill()
		},
		cleanup: terminal.Close,
	}, true, nil
}
