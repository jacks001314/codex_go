//go:build windows

package appserver

import (
	"os"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

// TestWindowsSandboxSelectionLogLikeRust mirrors Rust #51974: startup records
// the selected backend (mxc/disabled/unelevated/elevated) in the sandbox log
// directory, resolved from the effective config.
func TestWindowsSandboxSelectionLogLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		level sandbox.WindowsSandboxLevel
		want  string
	}{
		{level: sandbox.WindowsSandboxMxc, want: "mxc"},
		{level: sandbox.WindowsSandboxElevated, want: "elevated"},
		{level: sandbox.WindowsSandboxUnelevated, want: "unelevated"},
		{level: sandbox.WindowsSandboxDisabled, want: "disabled"},
	} {
		if got := windowsSandboxBackendLabel(testCase.level); got != testCase.want {
			t.Fatalf("label(%q) = %q, want %q", testCase.level, got, testCase.want)
		}
	}

	home := t.TempDir()
	logWindowsSandboxSelection(home, sandbox.WindowsSandboxElevated)
	data, err := os.ReadFile(windowssandbox.CurrentLogFilePath(home))
	if err != nil {
		t.Fatalf("ReadFile(sandbox log) error = %v", err)
	}
	if !strings.Contains(string(data), "Windows sandbox selection: elevated") {
		t.Fatalf("sandbox log = %q", data)
	}
}

// TestWindowsSandboxSelectionLogResolvesConfigLikeRust pins that the startup
// log follows the effective `windows.sandbox` config value.
func TestWindowsSandboxSelectionLogResolvesConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("windows.sandbox = \"elevated\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	logWindowsSandboxSelectionForConfig(config.NewConfigService(home))
	data, err := os.ReadFile(windowssandbox.CurrentLogFilePath(home))
	if err != nil {
		t.Fatalf("ReadFile(sandbox log) error = %v", err)
	}
	if !strings.Contains(string(data), "Windows sandbox selection: elevated") {
		t.Fatalf("sandbox log = %q", data)
	}
}
