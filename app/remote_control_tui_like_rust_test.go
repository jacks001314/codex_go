package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"codex_go/appserverdaemon"
)

// TestRemoteControlSettingsFileLikeRust derives the daemon's settings file from
// its control socket: <codex home>/app-server-daemon/settings.json sits two
// directories above the socket.
func TestRemoteControlSettingsFileLikeRust(t *testing.T) {
	socketPath := filepath.Join(string(filepath.Separator)+"home", "user", ".codex", "app-server-daemon", "daemon.sock")
	want := filepath.Join(string(filepath.Separator)+"home", "user", ".codex", "app-server-daemon", "settings.json")
	if got := remoteControlSettingsFile(socketPath); got != want {
		t.Fatalf("remoteControlSettingsFile(%q) = %q, want %q", socketPath, got, want)
	}
	if got := remoteControlSettingsFile("   "); got != "" {
		t.Fatalf("empty socket path produced %q", got)
	}
}

// TestRemoteControlSavedPreferenceLikeRust reports the persisted preference and
// leaves it unknown when the file is missing or unreadable.
func TestRemoteControlSavedPreferenceLikeRust(t *testing.T) {
	home := t.TempDir()
	daemonDir := filepath.Join(home, "app-server-daemon")
	// SaveSettings requires a directory private to the current user, like the
	// daemon's own socket directory.
	if err := os.MkdirAll(daemonDir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	socketPath := filepath.Join(daemonDir, "daemon.sock")

	// Rust maps a missing settings file to Some(false): the daemon has no saved
	// preference, so a disabled state is not a pending change.
	if got := remoteControlSavedPreference(socketPath); got == nil || *got {
		t.Fatalf("missing settings file = %v, want false", got)
	}
	settingsFile := filepath.Join(daemonDir, appserverdaemon.SettingsFileName)
	// The daemon writes this file itself, so the test only has to place it.
	if err := os.WriteFile(settingsFile, []byte(`{"remoteControlEnabled":true}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	got := remoteControlSavedPreference(socketPath)
	if got == nil || !*got {
		t.Fatalf("saved preference = %v, want true", got)
	}
	// An unreadable file leaves the preference unknown, which Rust reports as None.
	if err := os.WriteFile(settingsFile, []byte("{not json"), 0o600); err != nil {
		t.Fatalf("write corrupt settings: %v", err)
	}
	if got := remoteControlSavedPreference(socketPath); got != nil {
		t.Fatalf("corrupt settings file = %v, want nil", *got)
	}
}

// TestRemoteControlDaemonSupportedLikeRust applies Rust's version gate: a 0.0.0
// development daemon is accepted, and persistent remote control needs at least
// 0.163.0-alpha.2.
func TestRemoteControlDaemonSupportedLikeRust(t *testing.T) {
	for version, want := range map[string]bool{
		"":                true,
		"0.0.0":           true,
		"0.162.9":         false,
		"0.163.0-alpha.1": false,
		"0.163.0-alpha.2": true,
		"0.163.0":         true,
		"v0.164.1":        true,
		"not-a-version":   true,
	} {
		if got := remoteControlDaemonSupported(version); got != want {
			t.Fatalf("remoteControlDaemonSupported(%q) = %v, want %v", version, got, want)
		}
	}
}

// TestRemoteControlServerRefusalLikeRust keeps the daemon refusals the command
// words itself apart from connection and verification failures.
func TestRemoteControlServerRefusalLikeRust(t *testing.T) {
	for _, message := range []string{
		"remote control is disabled by managed requirements",
		"remote control requires ChatGPT authentication",
		"remote control requires ChatGPT authentication; API key auth is not supported",
	} {
		if got, ok := remoteControlServerRefusal(errors.New(message)); !ok || got != message {
			t.Fatalf("refusal(%q) = %q/%v", message, got, ok)
		}
	}
	if _, ok := remoteControlServerRefusal(errors.New("dial unix: no such file or directory")); ok {
		t.Fatal("a connection failure was treated as a daemon refusal")
	}
	if _, ok := remoteControlServerRefusal(nil); ok {
		t.Fatal("a nil error was treated as a refusal")
	}
}

// TestInteractiveRemoteControlRunnerNeedsVerifiedDaemonLikeRust keeps `/rc`
// unavailable for targets that are not the verified local background server.
func TestInteractiveRemoteControlRunnerNeedsVerifiedDaemonLikeRust(t *testing.T) {
	if run := interactiveRemoteControlRunner(nil, "0.164.0"); run != nil {
		t.Fatal("a nil endpoint produced a runner")
	}
	remote := appserverdaemon.NewWebSocketEndpoint("wss://example.invalid", nil)
	if run := interactiveRemoteControlRunner(remote, "0.164.0"); run != nil {
		t.Fatal("a remote endpoint produced a runner")
	}
	other := appserverdaemon.NewUnixSocketEndpoint(filepath.Join(t.TempDir(), "other.sock"))
	if run := interactiveRemoteControlRunner(other, "0.164.0"); run != nil {
		t.Fatal("an unverified socket produced a runner")
	}
}
