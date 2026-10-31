// Package servicepolicy validates Windows sandbox provisioning against managed
// policy, mirroring the windows-sandbox-service machine_policy module. A
// registration-only refresh retains the existing sandbox and is admitted by the
// registered-runtime transaction instead.
package servicepolicy

import (
	"errors"
	"fmt"

	"codex_go/config"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

// ValidateProvisioningRequirements refuses provisioning that managed policy
// does not permit. Callers must not use it for registration-only refreshes.
func ValidateProvisioningRequirements(settings provisionservice.Settings, listeners provisionservice.Listeners, requirements *config.ConfigRequirements) error {
	if requirements == nil {
		return nil
	}
	mode, fellBack, ok := config.ResolveWindowsSandboxMode(
		map[string]any{"windows": map[string]any{"sandbox": string(config.WindowsSandboxModeElevated)}},
		requirements,
	)
	if !ok || fellBack || mode != config.WindowsSandboxModeElevated {
		return errors.New("managed policy does not permit the elevated Windows sandbox")
	}
	network := requirements.Network
	if network == nil {
		return nil
	}
	if network.Enabled != nil && !*network.Enabled &&
		(settings.AllowLocalBinding || len(settings.ProxyPorts) > 0) {
		return errors.New("managed policy disables sandbox network access")
	}
	if settings.AllowLocalBinding && network.AllowLocalBinding != nil && !*network.AllowLocalBinding {
		return errors.New("managed policy does not permit local network binding")
	}
	if network.HTTPPort != nil {
		for _, port := range listeners.HTTPPorts {
			if port != *network.HTTPPort {
				return fmt.Errorf("managed policy does not permit HTTP proxy port %d", port)
			}
		}
	}
	if network.SOCKSPort != nil {
		for _, port := range listeners.SOCKSPorts {
			if port != *network.SOCKSPort {
				return fmt.Errorf("managed policy does not permit SOCKS proxy port %d", port)
			}
		}
	}
	// Omitting listener identities must not bypass managed port restrictions.
	if network.HTTPPort == nil && network.SOCKSPort == nil {
		return nil
	}
	for _, port := range settings.ProxyPorts {
		if containsPort(listeners.HTTPPorts, port) || containsPort(listeners.SOCKSPorts, port) {
			continue
		}
		if network.HTTPPort != nil && *network.HTTPPort == port {
			continue
		}
		if network.SOCKSPort != nil && *network.SOCKSPort == port {
			continue
		}
		return fmt.Errorf("managed policy does not permit unclassified proxy port %d", port)
	}
	return nil
}

func containsPort(ports []uint16, port uint16) bool {
	for _, candidate := range ports {
		if candidate == port {
			return true
		}
	}
	return false
}
