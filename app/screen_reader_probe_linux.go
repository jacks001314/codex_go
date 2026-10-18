//go:build linux

package app

import (
	"context"
	"os/exec"
	"strings"
	"time"
)

func screenReaderProbe() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	for _, process := range []string{"orca", "speech-dispatcher"} {
		if exec.CommandContext(ctx, "pgrep", "-x", process).Run() == nil {
			return true
		}
	}
	out, err := exec.CommandContext(ctx, "gsettings", "get", "org.gnome.desktop.a11y.applications", "screen-reader-enabled").Output()
	return err == nil && strings.EqualFold(strings.TrimSpace(string(out)), "true")
}
