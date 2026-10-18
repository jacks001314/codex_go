//go:build darwin

package app

import (
	"context"
	"os/exec"
	"time"
)

func screenReaderProbe() bool {
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	return exec.CommandContext(ctx, "pgrep", "-x", "VoiceOver").Run() == nil
}
