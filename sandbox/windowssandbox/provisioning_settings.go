package windowssandbox

import (
	"sort"
	"strings"

	coresandbox "codex_go/sandbox"
	"codex_go/sandbox/windowssandbox/provisionservice"
)

// ProvisioningSettingsFromEnvironment derives the provisioning service's
// firewall settings with the same environment handling as elevated setup
// (Rust WindowsSandboxProvisioningSettings::from_environment).
func ProvisioningSettingsFromEnvironment(profile *coresandbox.PermissionProfile, envMap map[string]string) provisionservice.Settings {
	identity := SandboxNetworkIdentityOffline
	if networkSandboxPolicyEnabled(profile) {
		identity = SandboxNetworkIdentityOnline
	}
	settings := OfflineProxySettingsFromEnv(envMap, identity)
	return provisionservice.Settings{
		ProxyPorts:        append([]uint16{}, settings.ProxyPorts...),
		AllowLocalBinding: settings.AllowLocalBinding,
	}
}

// ProvisioningListenersFromEnvironment identifies known listener protocols
// without restricting the ports setup allows. An enabled network sandbox has no
// filtered listeners (Rust WindowsSandboxProxyListeners::from_environment).
func ProvisioningListenersFromEnvironment(profile *coresandbox.PermissionProfile, envMap map[string]string) provisionservice.Listeners {
	if networkSandboxPolicyEnabled(profile) {
		return EmptyProvisioningListeners()
	}
	return ProvisioningListenersFromProxyEnvironment(envMap)
}

// ProvisioningListenersFromProxyEnvironment classifies loopback proxies by URL
// scheme (Rust WindowsSandboxProxyListeners::from_proxy_environment).
func ProvisioningListenersFromProxyEnvironment(envMap map[string]string) provisionservice.Listeners {
	listeners := EmptyProvisioningListeners()
	for _, key := range ProxyEnvKeys() {
		value := envMap[key]
		scheme, _, ok := strings.Cut(strings.TrimSpace(value), "://")
		if !ok {
			continue
		}
		port, ok := LoopbackProxyPortFromURL(value)
		if !ok {
			continue
		}
		switch strings.ToLower(scheme) {
		case "http", "https":
			listeners.HTTPPorts = append(listeners.HTTPPorts, port)
		case "socks4", "socks4a", "socks5", "socks5h":
			listeners.SOCKSPorts = append(listeners.SOCKSPorts, port)
		}
	}
	sort.Slice(listeners.HTTPPorts, func(i, j int) bool { return listeners.HTTPPorts[i] < listeners.HTTPPorts[j] })
	listeners.HTTPPorts = dedupeProvisioningPorts(listeners.HTTPPorts)
	sort.Slice(listeners.SOCKSPorts, func(i, j int) bool { return listeners.SOCKSPorts[i] < listeners.SOCKSPorts[j] })
	listeners.SOCKSPorts = dedupeProvisioningPorts(listeners.SOCKSPorts)
	return listeners
}

// FilterProvisioningListeners keeps only ports setup permits. Preserve-mode
// setup may use saved ports that differ from the current environment.
func FilterProvisioningListeners(listeners provisionservice.Listeners, allowedPorts []uint16) provisionservice.Listeners {
	allowed := make(map[uint16]bool, len(allowedPorts))
	for _, port := range allowedPorts {
		allowed[port] = true
	}
	filtered := EmptyProvisioningListeners()
	for _, port := range listeners.HTTPPorts {
		if allowed[port] {
			filtered.HTTPPorts = append(filtered.HTTPPorts, port)
		}
	}
	for _, port := range listeners.SOCKSPorts {
		if allowed[port] {
			filtered.SOCKSPorts = append(filtered.SOCKSPorts, port)
		}
	}
	return filtered
}

// EmptyProvisioningListeners returns empty, non-nil port lists.
func EmptyProvisioningListeners() provisionservice.Listeners {
	return provisionservice.Listeners{HTTPPorts: []uint16{}, SOCKSPorts: []uint16{}}
}

// networkSandboxPolicyEnabled mirrors Rust
// PermissionProfile::network_sandbox_policy().is_enabled(). A disabled profile
// counts as an enabled network sandbox there, and sandbox.PermissionProfile
// keeps the same contract in AllowsNetwork.
func networkSandboxPolicyEnabled(profile *coresandbox.PermissionProfile) bool {
	return profile != nil && profile.AllowsNetwork()
}

func dedupeProvisioningPorts(ports []uint16) []uint16 {
	if len(ports) < 2 {
		return ports
	}
	out := ports[:1]
	for _, port := range ports[1:] {
		if port != out[len(out)-1] {
			out = append(out, port)
		}
	}
	return out
}
