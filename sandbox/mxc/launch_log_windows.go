//go:build windows

package mxc

import (
	"fmt"
	"os"

	"codex_go/sandbox/windowssandbox"
)

// logLaunch mirrors Rust's mxc_sandbox::windows::log_launch: every launcher
// phase lands in the sandbox runtime log tagged with the launcher process id,
// so a failed or slow launch can be attributed to one stage (Rust #62439).
func logLaunch(event string) {
	_ = windowssandbox.LogRuntimeNote(fmt.Sprintf("MXC pid=%d %s", os.Getpid(), event))
}

// runtimeLogCodexHome returns the home the launcher pins its runtime log to.
// Rust passes CODEX_HOME as a launcher argument; Go's launcher transport keeps
// it in the inherited environment, and InitRuntimeLog ignores a relative value.
func runtimeLogCodexHome() string {
	return os.Getenv("CODEX_HOME")
}
