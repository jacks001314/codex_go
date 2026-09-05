//go:build windows

package appserverdaemon

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestRunWindowsUpdateInstallerRunsInsideJob(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := runWindowsUpdateInstaller(ctx, "powershell", []string{
		"-NoProfile",
		"-NonInteractive",
		"-Command",
		"Start-Sleep -Milliseconds 50",
	})
	if err != nil {
		t.Fatalf("runWindowsUpdateInstaller error = %v", err)
	}
}

func TestRunWindowsUpdateInstallerMissingCommandFails(t *testing.T) {
	err := runWindowsUpdateInstaller(context.Background(), "codex-no-such-updater-command", nil)
	if err == nil || !strings.Contains(err.Error(), "resolve standalone Codex updater command") {
		t.Fatalf("missing command error = %v", err)
	}
}
