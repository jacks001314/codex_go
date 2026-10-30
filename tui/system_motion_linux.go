//go:build linux

package tui

import (
	"context"
	"os/exec"
	"time"
)

// systemMotionProbeTimeout bounds the host preference probe (Rust #44666).
const systemMotionProbeTimeout = 250 * time.Millisecond

// platformSystemMotion reads the XDG desktop portal's reduced-motion setting
// through gdbus, the closest no-cgo equivalent of Rust's zbus call. A missing
// session bus or tool preserves the configured animations.
func platformSystemMotion() (MotionMode, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), systemMotionProbeTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "gdbus", "call",
		"--session",
		"--dest", "org.freedesktop.portal.Desktop",
		"--object-path", "/org/freedesktop/portal/desktop",
		"--method", "org.freedesktop.portal.Settings.ReadOne",
		"org.freedesktop.appearance", "reduced-motion",
	).Output()
	if err != nil {
		return MotionAnimated, false
	}
	return parsePortalReducedMotion(string(output))
}
