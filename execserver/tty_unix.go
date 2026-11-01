//go:build !windows

package execserver

import (
	"io"
	"os/exec"
	"syscall"

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

// startExecServerTTY starts cmd on a pseudo-terminal. beforeStart runs with the
// master after the pair is open but before the child exists, so the caller can
// park its capture reader on the master first: Linux discards buffered master
// input once the last slave closes, so a command that writes and exits
// immediately loses its output when no read is in flight yet.
func startExecServerTTY(cmd *exec.Cmd, beforeStart func(io.ReadWriteCloser)) (*startedExecServerTTY, bool, error) {
	terminal, slave, err := pty.Open()
	if err != nil {
		return nil, true, err
	}
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setsid = true
	cmd.SysProcAttr.Setctty = true
	if cmd.Stdin == nil {
		cmd.Stdin = slave
	}
	if cmd.Stdout == nil {
		cmd.Stdout = slave
	}
	if cmd.Stderr == nil {
		cmd.Stderr = slave
	}
	// Rust bf3eb2ec91 (#40460): the master is driven by readiness, so closing the
	// terminal wakes the capture reader instead of leaving it parked when a
	// detached child keeps the slave open.
	master, masterErr := ptyio.New(terminal)
	if masterErr != nil {
		_ = terminal.Close()
		_ = slave.Close()
		return nil, true, masterErr
	}
	if beforeStart != nil {
		beforeStart(master)
	}
	startErr := cmd.Start()
	_ = slave.Close()
	if startErr != nil {
		_ = master.Close()
		return nil, true, startErr
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
