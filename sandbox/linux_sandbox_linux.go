//go:build linux

package sandbox

import (
	"io"

	"codex_go/sandbox/linuxsandbox"
)

func runLinuxSandboxHelper(args []string, stdout, stderr io.Writer) int {
	return linuxsandbox.RunHelper(args, stdout, stderr)
}
