//go:build !windows

package appserver

import (
	"context"
	osexec "os/exec"

	"github.com/creack/pty"

	"codex_go/ptyio"
)

func startPTYCommand(ctx context.Context, cmd *osexec.Cmd, size *TerminalSize) (*ptyProcess, *ptyHandle, error) {
	terminalSize := terminalSizeOrDefault(size)
	file, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: terminalSize.Rows,
		Cols: terminalSize.Cols,
	})
	if err != nil {
		return nil, nil, err
	}
	// Rust bf3eb2ec91 (#40460): the master is duplicated, marked O_NONBLOCK and
	// driven by readiness, so a detached child that keeps the terminal open can
	// never leave the reader parked.
	master, masterErr := ptyio.New(file)
	if masterErr != nil {
		_ = file.Close()
		return nil, nil, masterErr
	}
	done := make(chan struct{})
	process := &ptyProcess{
		wait: func() error {
			defer close(done)
			return cmd.Wait()
		},
		kill: func() error {
			if cmd.Process == nil {
				return nil
			}
			return cmd.Process.Kill()
		},
	}
	handle := &ptyHandle{
		reader: master,
		writer: master,
		closeInput: func() error {
			return master.SendEOF()
		},
		closePTY: func() error {
			return master.Close()
		},
		closeReader: func() error {
			return master.InterruptRead()
		},
		cleanup: func() error {
			return master.Close()
		},
		resize: func(size *TerminalSize) error {
			terminalSize := terminalSizeOrDefault(size)
			return master.SetSize(terminalSize.Rows, terminalSize.Cols)
		},
	}
	go monitorPTYContext(ctx.Done(), done, process)
	return process, handle, nil
}
