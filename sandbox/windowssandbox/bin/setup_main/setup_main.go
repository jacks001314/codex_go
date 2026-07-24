package setup_main

import (
	"fmt"
	"io"

	"codex_go/sandbox/windowssandbox/bin/setup_main/win"
)

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	if err := win.Run(args, stdout, stderr); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	return 0
}
