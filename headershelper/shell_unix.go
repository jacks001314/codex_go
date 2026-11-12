//go:build !windows

package headershelper

import (
	"context"
	"os/exec"
)

// shellCommand builds the POSIX shell invocation the helper contract uses.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	return exec.CommandContext(ctx, "sh", "-c", command)
}
