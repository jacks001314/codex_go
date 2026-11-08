//go:build windows

package execserver

import (
	"os"
	"testing"

	"codex_go/sandbox/mxc"
)

// TestMain dispatches the MXC launcher argument before ordinary flag parsing,
// mirroring cmd/internal/codexmain/codexmain.go.
//
// A TTY MXC launch starts os.Executable() -- this test binary -- as the stage
// one helper, so the dispatch has to exist here too, exactly like Rust's
// exec-server/tests/common/mod.rs. Without it the TTY launch tests would spawn a
// process that fails in the test flag parser and never reaches the sandbox.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == mxc.LauncherArg1 {
		os.Exit(mxc.RunMain(os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
