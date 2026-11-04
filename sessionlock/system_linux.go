//go:build linux

package sessionlock

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// SystemDetector returns the systemd-logind session lock detector. It asks
// loginctl for the session's LockedHint, the same property Rust's D-Bus
// subscription watches (#59330 linux::observe).
func SystemDetector() Detector { return logindDetector{} }

type logindDetector struct{}

func (logindDetector) Locked() (bool, error) {
	args := []string{"show-session"}
	if sessionID := strings.TrimSpace(os.Getenv("XDG_SESSION_ID")); sessionID != "" {
		args = append(args, sessionID)
	} else {
		args = append(args, "self")
	}
	args = append(args, "-p", "LockedHint", "--value")
	output, err := exec.Command("loginctl", args...).Output()
	if err != nil {
		return false, fmt.Errorf("loginctl LockedHint: %w", err)
	}
	switch strings.TrimSpace(string(output)) {
	case "yes":
		return true, nil
	case "no":
		return false, nil
	default:
		return false, fmt.Errorf("unexpected loginctl LockedHint value %q", strings.TrimSpace(string(output)))
	}
}
