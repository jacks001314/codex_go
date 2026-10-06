//go:build windows

package appserverdaemon

import (
	"context"
	"strings"
	"testing"
	"time"
)

// TestRunWindowsUpdateInstallerCapturesStderrLikeRust covers Rust #50499 on
// Windows: a failed installer's stderr is captured and appended to the error,
// and the forced UTF-8 console encoding preserves non-ASCII diagnostics.
func TestRunWindowsUpdateInstallerCapturesStderrLikeRust(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := "[Console]::OutputEncoding = [System.Text.UTF8Encoding]::new($false); " +
		"[Console]::Error.Write('installer-boom-\u00e9'); exit 1"
	err := runWindowsUpdateInstallerWithInput(ctx, "powershell.exe", []string{
		"-NoLogo", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", command,
	}, nil, nil)
	if err == nil {
		t.Fatal("runWindowsUpdateInstallerWithInput() succeeded, want a failure")
	}
	message := err.Error()
	if !strings.Contains(message, "installer-boom") {
		t.Fatalf("error %q does not carry the captured stderr tail", message)
	}
	if !strings.Contains(message, "\u00e9") {
		t.Fatalf("error %q lost the non-ASCII diagnostics", message)
	}
}
