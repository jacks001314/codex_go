package appserverdaemon

import (
	"fmt"
	"strings"

	"codex_go/config"
)

// DaemonUpdatesDisabledMessage is the error Rust #62690 reports when managed
// policy disables both scheduled and manual daemon updates.
const DaemonUpdatesDisabledMessage = "Daemon updates are disabled by managed policy (features.daemon_updates = false)."

// DaemonUpdatesAllowed reports whether locally managed requirements permit
// daemon updates (`features.daemon_updates`, Rust #62690 update_policy::allowed).
// Updates are allowed by default; a requirements load failure is surfaced so
// callers can retry instead of silently allowing or blocking an update.
func DaemonUpdatesAllowed(codexHome string) (bool, error) {
	requirements, err := config.LoadManagedRequirements(strings.TrimSpace(codexHome), nil)
	if err != nil {
		return false, fmt.Errorf("failed to load managed daemon update policy: %w", err)
	}
	if requirements == nil || requirements.FeatureRequirements == nil {
		return true, nil
	}
	enabled, ok := requirements.FeatureRequirements["daemon_updates"]
	if !ok {
		return true, nil
	}
	return enabled, nil
}

// EnsureDaemonUpdatesAllowed returns Rust #62690's policy error when managed
// requirements block daemon updates.
func EnsureDaemonUpdatesAllowed(codexHome string) error {
	allowed, err := DaemonUpdatesAllowed(codexHome)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("%s", DaemonUpdatesDisabledMessage)
	}
	return nil
}
