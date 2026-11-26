//go:build windows

package execserver

import (
	"os"
	"testing"

	"codex_go/sandbox/mxc"
	"codex_go/sandbox/windowssandbox"
)

// TestMain dispatches the MXC launcher argument before ordinary flag parsing,
// mirroring cmd/internal/codexmain/codexmain.go.
//
// A TTY MXC launch starts os.Executable() -- this test binary -- as the stage
// one helper, so the dispatch has to exist here too, exactly like Rust's
// exec-server/tests/common/mod.rs. Without it the TTY launch tests would spawn a
// process that fails in the test flag parser and never reaches the sandbox.
//
// The filesystem helper is dispatched the same way: its generated command is the
// restricted-token wrapper, which re-launches this binary as the helper, so both
// entry points have to exist here for a real end-to-end helper run.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case mxc.LauncherArg1:
			os.Exit(mxc.RunMain(os.Stdin, os.Stdout, os.Stderr))
		case windowssandbox.CodexWindowsSandboxArg1:
			os.Exit(windowssandbox.RunWindowsSandboxWrapperMain(os.Args[1:]))
		case FSHelperArg1:
			os.Exit(RunFSHelper(os.Stdin, os.Stdout, os.Stderr))
		}
	}
	os.Exit(m.Run())
}
