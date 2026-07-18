//go:build windows

package appserver

import (
	"context"
	"fmt"
	"io"
	osexec "os/exec"

	gopty "github.com/aymanbagabas/go-pty"
)

func startPTYCommand(ctx context.Context, cmd *osexec.Cmd, size *TerminalSize) (*ptyProcess, *ptyHandle, error) {
	if cmd == nil || len(cmd.Args) == 0 {
		return nil, nil, fmt.Errorf("%w: pty command is empty", ErrInvalidRequest)
	}
	if cmd.Err != nil {
		return nil, nil, cmd.Err
	}

	terminal, err := gopty.New()
	if err != nil {
		return nil, nil, err
	}
	terminalSize := terminalSizeOrDefault(size)
	if err := terminal.Resize(int(terminalSize.Cols), int(terminalSize.Rows)); err != nil {
		_ = terminal.Close()
		return nil, nil, err
	}

	ptyCommand := terminal.Command(cmd.Path, cmd.Args[1:]...)
	ptyCommand.Dir = cmd.Dir
	ptyCommand.Env = cmd.Env
	ptyCommand.SysProcAttr = cmd.SysProcAttr
	if err := ptyCommand.Start(); err != nil {
		_ = terminal.Close()
		return nil, nil, err
	}

	done := make(chan struct{})
	process := &ptyProcess{
		wait: func() error {
			defer close(done)
			return ptyCommand.Wait()
		},
		kill: func() error {
			if ptyCommand.Process == nil {
				return nil
			}
			return ptyCommand.Process.Kill()
		},
	}
	conpty, _ := terminal.(gopty.ConPty)
	handle := &ptyHandle{
		reader:    terminal,
		writer:    terminal,
		normalize: (&windowsTTYInputNormalizer{}).Normalize,
		closeInput: func() error {
			if conpty == nil || conpty.InputPipe() == nil {
				return nil
			}
			return conpty.InputPipe().Close()
		},
		closePTY: terminal.Close,
		closeReader: func() error {
			if conpty == nil || conpty.OutputPipe() == nil {
				return nil
			}
			return conpty.OutputPipe().Close()
		},
		resize: func(size *TerminalSize) error {
			terminalSize := terminalSizeOrDefault(size)
			return terminal.Resize(int(terminalSize.Cols), int(terminalSize.Rows))
		},
	}
	go monitorPTYContext(ctx.Done(), done, process)
	return process, handle, nil
}

type windowsTTYInputNormalizer struct {
	previousWasCR bool
}

func (n *windowsTTYInputNormalizer) Normalize(data []byte) []byte {
	if n == nil || len(data) == 0 {
		return data
	}
	normalized := make([]byte, 0, len(data))
	for _, b := range data {
		switch b {
		case '\b':
			normalized = append(normalized, 0x7f)
		case '\n':
			if !n.previousWasCR {
				normalized = append(normalized, '\r')
			}
		default:
			normalized = append(normalized, b)
		}
		n.previousWasCR = b == '\r'
	}
	return normalized
}

var _ io.WriteCloser = (*ptyHandle)(nil)
