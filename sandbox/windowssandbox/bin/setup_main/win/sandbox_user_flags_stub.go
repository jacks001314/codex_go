//go:build !windows

package win

import "codex_go/sandbox/windowssandbox"

func ReadLocalUserFlags(name string) (uint32, bool, error) {
	return 0, false, windowssandbox.Unsupported("bin.setup_main.win.sandbox_user_flags.read_local_user_flags")
}
