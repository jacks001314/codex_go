package windowssandbox

import "testing"

// TestRegisteredSetupIsReadyRequiresTheServiceReceiptLikeRust pins Rust
// registered_setup_is_ready: both managed accounts must exist and be enabled,
// the process must run from an installed package, and the committed receipt
// must name that package and this Codex home.
func TestRegisteredSetupIsReadyRequiresTheServiceReceiptLikeRust(t *testing.T) {
	previousFlags := sandboxAccountFlagsForCredentials
	previousPackage := currentProcessPackageName
	previousRecord := loadInstallationForReadiness
	t.Cleanup(func() {
		sandboxAccountFlagsForCredentials = previousFlags
		currentProcessPackageName = previousPackage
		loadInstallationForReadiness = previousRecord
	})

	healthy := func(string) (uint32, bool, error) { return 0, true, nil }
	installed := func() (string, bool, error) { return readyPackageFullName, true, nil }
	record := func() (*InstallationRecord, error) { return aliasRecord(), nil }

	t.Run("ready", func(t *testing.T) {
		sandboxAccountFlagsForCredentials = healthy
		currentProcessPackageName = installed
		loadInstallationForReadiness = record
		ready, err := registeredSetupIsReady(aliasRecord().CodexHome)
		if err != nil || !ready {
			t.Fatalf("readiness = %v, %v", ready, err)
		}
	})

	t.Run("disabled account", func(t *testing.T) {
		sandboxAccountFlagsForCredentials = func(name string) (uint32, bool, error) {
			if name == OnlineUsername {
				return 0x0002, true, nil
			}
			return 0, true, nil
		}
		currentProcessPackageName = installed
		loadInstallationForReadiness = record
		ready, err := registeredSetupIsReady(aliasRecord().CodexHome)
		if err != nil || ready {
			t.Fatalf("readiness = %v, %v", ready, err)
		}
	})

	t.Run("missing account", func(t *testing.T) {
		sandboxAccountFlagsForCredentials = func(name string) (uint32, bool, error) {
			return 0, false, nil
		}
		currentProcessPackageName = installed
		loadInstallationForReadiness = record
		if ready, err := registeredSetupIsReady(aliasRecord().CodexHome); err != nil || ready {
			t.Fatalf("readiness = %v, %v", ready, err)
		}
	})

	t.Run("unpackaged process", func(t *testing.T) {
		sandboxAccountFlagsForCredentials = healthy
		currentProcessPackageName = func() (string, bool, error) { return "", false, nil }
		loadInstallationForReadiness = record
		if ready, err := registeredSetupIsReady(aliasRecord().CodexHome); err != nil || ready {
			t.Fatalf("readiness = %v, %v", ready, err)
		}
	})

	t.Run("no receipt", func(t *testing.T) {
		sandboxAccountFlagsForCredentials = healthy
		currentProcessPackageName = installed
		loadInstallationForReadiness = func() (*InstallationRecord, error) { return nil, nil }
		if ready, err := registeredSetupIsReady(aliasRecord().CodexHome); err != nil || ready {
			t.Fatalf("readiness = %v, %v", ready, err)
		}
	})

	t.Run("other Codex home", func(t *testing.T) {
		sandboxAccountFlagsForCredentials = healthy
		currentProcessPackageName = installed
		loadInstallationForReadiness = record
		if ready, err := registeredSetupIsReady(`C:\Users\other\.codex`); err != nil || ready {
			t.Fatalf("readiness = %v, %v", ready, err)
		}
	})

	t.Run("receipt for another package", func(t *testing.T) {
		sandboxAccountFlagsForCredentials = healthy
		currentProcessPackageName = func() (string, bool, error) {
			return "OpenAI.Codex_2.0.0.0_arm64__publisher", true, nil
		}
		loadInstallationForReadiness = record
		if ready, err := registeredSetupIsReady(aliasRecord().CodexHome); err != nil || ready {
			t.Fatalf("readiness = %v, %v", ready, err)
		}
	})
}
