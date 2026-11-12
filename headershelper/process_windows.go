//go:build windows

package headershelper

import (
	"os/exec"
	"strconv"

	"codex_go/envutil"
)

// prepareProcess has no process-group equivalent on Windows; taskkill walks the
// tree by parent process id instead.
func prepareProcess(*exec.Cmd) {}

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/T", "/F", "/PID", strconv.Itoa(cmd.Process.Pid))
	envutil.SuppressConsoleWindow(kill)
	_ = kill.Run()
}
