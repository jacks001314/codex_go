//go:build !windows

package win

import (
	"io"

	"codex_go/internal/sandbox/windowssandbox"
)

func run(args []string, stdin io.Reader, stdout io.Writer, stderr io.Writer) error {
	return windowssandbox.Unsupported("bin.command_runner.win.run")
}
