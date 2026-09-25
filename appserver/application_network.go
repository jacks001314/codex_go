package appserver

// Installs application requirements for the app-server's own transports.
//
// Rust parity: codex-rs/app-server/src/application_network.rs, whose
// `destination_policy` composes the managed requirements into the shared
// destination policy, plus codex-rs/http-client's managed request execution
// (#47407/#47389).

import (
	"codex_go/config"
	"codex_go/network"
)

// destinationPolicyFromApplicationRequirements mirrors Rust's
// `destination_policy`: an enabled application network requirement restricts
// traffic to its explicitly allowed domains, while a missing or disabled
// requirement leaves application traffic unrestricted.
func destinationPolicyFromApplicationRequirements(application *config.ApplicationRequirements) network.DestinationPolicy {
	// The composition lives with the requirements type so every host (the
	// app-server, codex exec, the CLI) publishes the same policy.
	return config.DestinationPolicyForApplicationRequirements(application)
}

// applicationNetworkRequirements returns the effective managed application
// requirements, or nil when the host has none.
func (r *RuntimeRouter) applicationNetworkRequirements() *config.ApplicationRequirements {
	if r == nil || r.services.Config == nil {
		return nil
	}
	read := r.requireConfig().Requirements()
	if read == nil || read.Requirements == nil {
		return nil
	}
	return read.Requirements.Application
}

// refreshApplicationNetworkPolicy publishes the policy composed from the
// current managed requirements and returns both the read handle transports bind
// to and the composed policy. Rust installs the same policy at startup and at
// explicit config/account reloads; Go re-derives it whenever a transport is
// built, so a request can never run under a superseded policy. Publication is
// revision-guarded: a concurrent invalidation makes this load fail and the next
// one republish.
func (r *RuntimeRouter) refreshApplicationNetworkPolicy() (network.NetworkPolicy, network.DestinationPolicy) {
	if r == nil || r.networkPolicy == nil {
		return network.UnmanagedNetworkPolicy(), network.UnrestrictedDestinationPolicy()
	}
	r.networkPolicyMu.Lock()
	defer r.networkPolicyMu.Unlock()
	policy := r.networkPolicy.Policy()
	composed := destinationPolicyFromApplicationRequirements(r.applicationNetworkRequirements())
	r.networkPolicy.Publish(policy.Revision(), composed)
	return policy, composed
}

// invalidateApplicationNetworkPolicy revokes every outstanding operation before
// an account change (Rust `NetworkPolicy::invalidate`); the next transport
// build republishes the current requirements.
func (r *RuntimeRouter) invalidateApplicationNetworkPolicy() {
	if r == nil || r.networkPolicy == nil {
		return
	}
	r.networkPolicyMu.Lock()
	defer r.networkPolicyMu.Unlock()
	r.networkPolicy.Policy().Invalidate()
}
