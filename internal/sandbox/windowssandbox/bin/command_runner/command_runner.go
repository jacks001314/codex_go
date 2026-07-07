package command_runner

import (
	"fmt"
	"io"

	"codex_go/internal/sandbox/windowssandbox/bin/command_runner/win"
)

func Run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) int {
	if err := win.Run(args, stdin, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
