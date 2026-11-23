package app

import (
	"strings"

	"codex_go/appserverdaemon"
	"codex_go/auth"
)

// daemonUpdatesPolicyError mirrors Rust #62690's ensure_updates_allowed check
// for the `/daemon` menu: it returns managed policy's error only when this
// session can manage the local daemon, so remote and cloud targets keep their
// own guidance.
func daemonUpdatesPolicyError(applicable bool) string {
	if !applicable {
		return ""
	}
	if err := appserverdaemon.EnsureDaemonUpdatesAllowed(strings.TrimSpace(auth.DefaultCodexHome())); err != nil {
		return err.Error()
	}
	return ""
}
