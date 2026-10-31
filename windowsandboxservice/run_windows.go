//go:build windows

package windowsandboxservice

import "errors"

// runServiceMode hosts the SCM service with the production provisioning broker
// (Rust lib::run's RunMode::Service arm).
func runServiceMode(mode RunMode) error {
	if mode != RunModeService {
		return errors.New("unsupported sandbox service run mode")
	}
	return RunService(NewProvisioningBroker(NewLifecycle(), nil))
}
