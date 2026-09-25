package config

// Application network policy carrier and composition.
//
// Rust parity: `codex_config::Config::application_network_policy` - the live
// policy a runtime publishes so every client built from the config binds the
// same generation - and
// `app-server/src/application_network.rs::destination_policy`, the one place
// managed application requirements become a destination policy.

import (
	"codex_go/network"
)

// DestinationPolicyForApplicationRequirements composes the managed application
// requirements into a destination policy. An enabled requirement restricts
// application traffic to its explicitly allowed domains; a missing, empty or
// disabled requirement leaves it unrestricted.
func DestinationPolicyForApplicationRequirements(application *ApplicationRequirements) network.DestinationPolicy {
	requirements := (*ApplicationNetworkRequirements)(nil)
	if application != nil {
		requirements = application.Network
	}
	if requirements == nil || !requirements.Enabled {
		return network.UnrestrictedDestinationPolicy()
	}
	allowedHosts := make([]string, 0, len(requirements.Domains))
	for host, permission := range requirements.Domains {
		if permission == NetworkAllow {
			allowedHosts = append(allowedHosts, host)
		}
	}
	return network.RestrictedDestinationPolicy(allowedHosts)
}

// NetworkPolicy returns the application network policy bound to this config, or
// an unmanaged policy when the runtime published none.
func (c *Config) NetworkPolicy() network.NetworkPolicy {
	if c == nil || c.ApplicationNetworkPolicy == nil {
		return network.UnmanagedNetworkPolicy()
	}
	return *c.ApplicationNetworkPolicy
}

// SetNetworkPolicy binds the runtime's live application network policy to this
// config so every client built from it shares the same policy generation
// (Rust `EmbeddedNetworkPolicy::bind_config`).
func (c *Config) SetNetworkPolicy(policy network.NetworkPolicy) {
	if c == nil {
		return
	}
	c.ApplicationNetworkPolicy = &policy
}

// BindApplicationNetworkPolicy publishes the policy composed from this config's
// managed requirements and binds it to the config, returning the controller the
// caller keeps for account-change invalidation (Rust
// `EmbeddedNetworkPolicy::activate`). The effective requirements come from the
// config's requirements snapshot, which is nil when the host has none.
func (c *Config) BindApplicationNetworkPolicy() *network.NetworkPolicyController {
	controller := network.NewNetworkPolicyController()
	if c == nil {
		return controller
	}
	var application *ApplicationRequirements
	if c.Requirements != nil {
		application = c.Requirements.Application
	}
	policy := controller.Policy()
	controller.Publish(policy.Revision(), DestinationPolicyForApplicationRequirements(application))
	c.SetNetworkPolicy(policy)
	return controller
}
