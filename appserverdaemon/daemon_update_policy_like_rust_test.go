package appserverdaemon

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #62690: daemon updates are allowed by default and only local managed
// requirements may block them.
func TestDaemonUpdatesPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	allowed, err := DaemonUpdatesAllowed(home)
	if err != nil || !allowed {
		t.Fatalf("default policy = %v, %v; want updates allowed", allowed, err)
	}
	if err := EnsureDaemonUpdatesAllowed(home); err != nil {
		t.Fatalf("EnsureDaemonUpdatesAllowed(default) = %v", err)
	}

	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("[features]\ndaemon_updates = true\n"), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	if allowed, err := DaemonUpdatesAllowed(home); err != nil || !allowed {
		t.Fatalf("explicit true policy = %v, %v", allowed, err)
	}

	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("[features]\ndaemon_updates = false\n"), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	allowed, err = DaemonUpdatesAllowed(home)
	if err != nil || allowed {
		t.Fatalf("disabled policy = %v, %v; want updates blocked", allowed, err)
	}
	err = EnsureDaemonUpdatesAllowed(home)
	if err == nil || !strings.Contains(err.Error(), DaemonUpdatesDisabledMessage) {
		t.Fatalf("EnsureDaemonUpdatesAllowed(disabled) = %v", err)
	}
}

// Rust #62690 surfaces an unreadable policy instead of guessing.
func TestDaemonUpdatesPolicyReportsInvalidRequirementsLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("[invalid toml"), 0o600); err != nil {
		t.Fatalf("write requirements: %v", err)
	}
	if _, err := DaemonUpdatesAllowed(home); err == nil {
		t.Fatal("DaemonUpdatesAllowed(invalid requirements) = nil error")
	}
	if err := EnsureDaemonUpdatesAllowed(home); err == nil {
		t.Fatal("EnsureDaemonUpdatesAllowed(invalid requirements) = nil error")
	}
}
