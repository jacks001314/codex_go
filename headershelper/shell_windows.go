//go:build windows

package headershelper

import (
	"context"
	"os"
	"os/exec"
	"strings"

	"golang.org/x/sys/windows"
)

// shellCommand builds the cmd.exe invocation. The command text is passed
// through SysProcAttr.CmdLine because Go would otherwise escape the helper's
// own quotes, which cmd.exe does not understand.
func shellCommand(ctx context.Context, command string) *exec.Cmd {
	comspec := strings.TrimSpace(os.Getenv("COMSPEC"))
	if comspec == "" {
		comspec = "cmd.exe"
	}
	cmd := exec.CommandContext(ctx, comspec)
	cmd.SysProcAttr = &windows.SysProcAttr{CmdLine: `"` + comspec + `" /Q /D /C ` + command}
	return cmd
}
