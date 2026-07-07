//go:build !windows

package win

import (
	"io"

	"codex_go/internal/sandbox/windowssandbox"
)

func run(args []string, stdout io.Writer, stderr io.Writer) error {
	return windowssandbox.Unsupported("bin.setup_main.win.run")
}
