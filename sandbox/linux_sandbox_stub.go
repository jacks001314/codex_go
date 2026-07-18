//go:build !linux

package sandbox

import (
	"fmt"
	"io"
)

func runLinuxSandboxHelper(args []string, stdout, stderr io.Writer) int {
	_ = args
	_ = stdout
	fmt.Fprintln(stderr, "codex-linux-sandbox is only supported on Linux")
	return 1
}
