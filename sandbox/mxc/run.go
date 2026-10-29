package mxc

import (
	"fmt"
	"io"
	"sort"
)

// UnsupportedPlatformMessage mirrors Rust's diagnostic when the helper is
// invoked on a host that cannot execute the native MXC sandbox.
const UnsupportedPlatformMessage = "MXC sandbox execution requires Windows"

// LauncherFailurePrefix mirrors Rust's `MXC launcher: {error:#}` wrapper. The
// helper reports only the launch error: MXC's own diagnostic buffer may contain
// command or environment data and is never published.
const LauncherFailurePrefix = "MXC launcher: "

// RunMain is the helper entry point Codex dispatches before ordinary CLI
// parsing (Rust `mxc_sandbox::run_main`). It returns the child exit code, or 1
// when the helper cannot launch the request.
func RunMain(stdin io.Reader, stdout, stderr io.Writer) int {
	if !helperSupported {
		fmt.Fprintln(stderr, UnsupportedPlatformMessage)
		return 1
	}
	exitCode, err := runHelper(stdin, stdout, stderr)
	if err != nil {
		fmt.Fprintf(stderr, "%s%v\n", LauncherFailurePrefix, err)
		return 1
	}
	return exitCode
}

// envList renders the filtered child environment as sorted KEY=VALUE entries,
// or nil when the request carries no child environment. Rust forwards
// `(!env.is_empty()).then_some(env)` after the launcher transport has removed
// its own state.
func envList(env map[string]string) []string {
	if len(env) == 0 {
		return nil
	}
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	entries := make([]string, 0, len(keys))
	for _, key := range keys {
		entries = append(entries, key+"="+env[key])
	}
	return entries
}
