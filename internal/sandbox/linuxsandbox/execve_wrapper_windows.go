//go:build windows

package linuxsandbox

import (
	"fmt"
	"io"
)

func RunExecveWrapperHelper(args []string, stdout, stderr io.Writer) int {
	_ = args
	_ = stdout
	fmt.Fprintln(stderr, "codex-execve-wrapper is only implemented for UNIX")
	return 1
}
