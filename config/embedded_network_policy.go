package config

// Host-owned application network policy.
//
// Rust parity: codex-rs/app-server/src/in_process_bootstrap.rs
// `EmbeddedNetworkPolicy`, whose two controllers outlive every config the host
// produces: `effective` carries the policy once the managed requirements are
// known, and `local` authorizes login and cloud-bootstrap traffic that runs
// before they are. A host creates one owner, activates it for each config it
// loads, and every client built from any of those configs keeps the same live
// read handle. Reloads and failures therefore reach existing transports instead
// of leaving each config with a private generation.

import (
	"sync"

	"codex_go/network"
)

// EmbeddedNetworkPolicy is the policy pair a CLI/TUI host owns for its
// lifetime. The zero value is unusable; call NewEmbeddedNetworkPolicy.
type EmbeddedNetworkPolicy struct {
	effective *network.NetworkPolicyController
	local     *network.NetworkPolicyController

	mu          sync.Mutex
	destination network.DestinationPolicy
}

// NewEmbeddedNetworkPolicy creates an owner whose effective and local policies
// start unavailable, matching Rust's default `NetworkPolicyController`s: no
// traffic is authorized until a successful load publishes a policy.
func NewEmbeddedNetworkPolicy() *EmbeddedNetworkPolicy {
	return &EmbeddedNetworkPolicy{
		effective:   network.NewNetworkPolicyController(),
		local:       network.NewNetworkPolicyController(),
		destination: network.UnrestrictedDestinationPolicy(),
	}
}

// Effective returns the read handle every application transport binds. Copies
// observe later Publish and Unavailable calls on the same owner.
func (p *EmbeddedNetworkPolicy) Effective() network.NetworkPolicy {
	if p == nil || p.effective == nil {
		return network.UnmanagedNetworkPolicy()
	}
	return p.effective.Policy()
}

// Local returns the read handle login and cloud-bootstrap transports bind.
func (p *EmbeddedNetworkPolicy) Local() network.NetworkPolicy {
	if p == nil || p.local == nil {
		return network.UnmanagedNetworkPolicy()
	}
	return p.local.Policy()
}

// EffectiveController returns the owner's effective controller so a host can
// invalidate it when the account or configuration generation changes.
func (p *EmbeddedNetworkPolicy) EffectiveController() *network.NetworkPolicyController {
	if p == nil {
		return nil
	}
	return p.effective
}

// LocalController returns the owner's local bootstrap controller so an
// embedded app-server can publish login/cloud policy through the same owner
// (Rust `ConfigManager::with_embedded_network_policy`).
func (p *EmbeddedNetworkPolicy) LocalController() *network.NetworkPolicyController {
	if p == nil {
		return nil
	}
	return p.local
}

// LoadLocal publishes the application policy composed from the home's local
// requirements (Rust `EmbeddedNetworkPolicy::load`). The local sources are the
// ones that exist before a cloud bootstrap: requirements.toml plus the legacy
// managed config layer. A malformed file leaves the previous local policy in
// place and returns the error.
func (p *EmbeddedNetworkPolicy) LoadLocal(codexHome string) error {
	if p == nil || p.local == nil {
		return nil
	}
	requirements, err := LoadManagedRequirements(codexHome, nil)
	if err != nil {
		return err
	}
	var application *ApplicationRequirements
	if requirements != nil {
		application = requirements.Application
	}
	composed := DestinationPolicyForApplicationRequirements(application)
	p.local.Publish(p.Local().Revision(), composed)
	return nil
}

// Activate publishes the policy composed from the config's effective managed
// requirements into the host owner and binds that live handle to the config
// (Rust `EmbeddedNetworkPolicy::activate`). Every config the host later loads
// through this owner must call Activate or BindConfig so its transports keep
// observing reloads.
func (p *EmbeddedNetworkPolicy) Activate(cfg *Config) network.NetworkPolicy {
	if p == nil || p.effective == nil {
		return network.UnmanagedNetworkPolicy()
	}
	if cfg == nil {
		return p.Effective()
	}
	var application *ApplicationRequirements
	if cfg.Requirements != nil {
		application = cfg.Requirements.Application
	}
	composed := DestinationPolicyForApplicationRequirements(application)
	p.mu.Lock()
	p.destination = composed
	p.mu.Unlock()
	policy := p.Effective()
	p.effective.Publish(policy.Revision(), composed)
	p.BindConfig(cfg)
	return p.Effective()
}

// BindConfig keeps a rebuilt config on the owner's live policy without
// publishing a new destination set (Rust `EmbeddedNetworkPolicy::bind_config`).
func (p *EmbeddedNetworkPolicy) BindConfig(cfg *Config) {
	if p == nil || cfg == nil || p.effective == nil {
		return
	}
	cfg.ApplicationNetworkPolicyOwner = p
	cfg.SetNetworkPolicy(p.Effective())
	p.mu.Lock()
	destination := p.destination
	p.mu.Unlock()
	cfg.SetApplicationDestinationPolicy(destination)
}

// Invalidate revokes every outstanding operation before an account change and
// keeps existing transports bound to the same owner.
func (p *EmbeddedNetworkPolicy) Invalidate() {
	if p == nil || p.effective == nil {
		return
	}
	p.Effective().Invalidate()
}

// Unavailable withdraws the effective policy after a failed reload, matching
// Rust's `fail_application_policy`: existing transports must stop working
// until a later load publishes a policy again.
func (p *EmbeddedNetworkPolicy) Unavailable() {
	if p == nil || p.effective == nil {
		return
	}
	policy := p.Effective()
	p.effective.Unavailable(policy.Revision())
}
