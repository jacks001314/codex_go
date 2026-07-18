package sandbox

import (
	"io"

	"codex_go/sandbox/linuxsandbox"
)

func RunLinuxSandboxHelper(args []string, stdout, stderr io.Writer) int {
	return runLinuxSandboxHelper(args, stdout, stderr)
}

func RunExecveWrapperHelper(args []string, stdout, stderr io.Writer) int {
	return linuxsandbox.RunExecveWrapperHelper(args, stdout, stderr)
}
