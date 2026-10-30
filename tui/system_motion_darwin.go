//go:build darwin

package tui

import (
	"context"
	"os/exec"
	"time"
)

// systemMotionProbeTimeout bounds the host preference probe (Rust #44666).
const systemMotionProbeTimeout = 250 * time.Millisecond

// platformSystemMotion reads the macOS Reduce Motion preference without cgo.
// An absent key or failed probe preserves the configured animations.
func platformSystemMotion() (MotionMode, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), systemMotionProbeTimeout)
	defer cancel()
	output, err := exec.CommandContext(ctx, "defaults", "read", "com.apple.universalaccess", "reduceMotion").Output()
	if err != nil {
		return MotionAnimated, false
	}
	return parseMacOSReduceMotion(string(output))
}
