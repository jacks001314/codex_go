//go:build !windows

package execserver

import (
	"io"
	"os/exec"

	"github.com/creack/pty"

	"codex_go/ptyio"
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
	// Rust bf3eb2ec91 (#40460): the master is driven by readiness, so closing the
	// terminal wakes the capture reader instead of leaving it parked when a
	// detached child keeps the slave open.
	master, masterErr := ptyio.New(terminal)
	if masterErr != nil {
		_ = terminal.Close()
		return nil, true, masterErr
	}
	return &startedExecServerTTY{
		stdin:  master,
		reader: master,
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
		closePTY: master.Close,
		cleanup:  master.Close,
	}, true, nil
}
