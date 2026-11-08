//go:build windows

package mxc

import (
	"os"
	"testing"
)

// TestMain dispatches LauncherArg1 before ordinary flag parsing, exactly like
// the production entry point (cmd/internal/codexmain/codexmain.go compares
// argv1 with mxc.LauncherArg1 and calls RunMain).
//
// The two-stage launcher starts *this* test binary as the helper through
// os.Executable(), so without this shim an end-to-end probe would spawn a
// process that dies in the test flag parser instead of entering the sandbox.
// Rust keeps the same shim in exec-server/tests/common/mod.rs and
// core/tests/suite/mod.rs.
func TestMain(m *testing.M) {
	if len(os.Args) > 1 && os.Args[1] == LauncherArg1 {
		os.Exit(RunMain(os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}
