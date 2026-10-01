//go:build windows

package codemode

import (
	"os/exec"

	"codex_go/envutil"
)

// suppressCodeModeHostConsoleWindow mirrors Rust #48138: the code-mode host is a
// detached helper, so launching it must not create a console window.
func suppressCodeModeHostConsoleWindow(cmd *exec.Cmd) {
	envutil.SuppressConsoleWindow(cmd)
}
