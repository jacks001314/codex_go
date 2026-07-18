//go:build !windows

package appserver

import (
	"context"
	osexec "os/exec"

	"github.com/creack/pty"
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
		reader: file,
		writer: file,
		closeInput: func() error {
			_, err := file.Write([]byte{4})
			return err
		},
		closePTY: func() error {
			return file.Close()
		},
		resize: func(size *TerminalSize) error {
			terminalSize := terminalSizeOrDefault(size)
			return pty.Setsize(file, &pty.Winsize{Rows: terminalSize.Rows, Cols: terminalSize.Cols})
		},
	}
	go monitorPTYContext(ctx.Done(), done, process)
	return process, handle, nil
}
