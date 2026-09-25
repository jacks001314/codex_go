package appserver

// Installs application requirements for the app-server's own transports.
//
// Rust parity: codex-rs/app-server/src/application_network.rs, whose
// `destination_policy` composes the managed requirements into the shared
// destination policy, plus codex-rs/http-client's managed request execution
// (#47407/#47389).

import (
	"net/http"

	"codex_go/config"
	"codex_go/model"
	"codex_go/network"
	"codex_go/remotecontrol"
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

// localApplicationNetworkRequirements returns the application requirements the
// home's own file supplies, before any effective override a host installed.
// Rust loads them independently of user, project and cloud configuration
// (config/src/loader/application.rs) because bootstrap and login traffic must be
// authorized before the effective policy - which can depend on a cloud fetch -
// is known.
func (r *RuntimeRouter) localApplicationNetworkRequirements() *config.ApplicationRequirements {
	if r == nil || r.services.Config == nil {
		return nil
	}
	requirements := r.services.Config.LocalRequirements()
	if requirements == nil {
		return nil
	}
	return requirements.Application
}

// refreshLocalApplicationNetworkPolicy publishes the policy composed from the
// local application requirements, mirroring Rust's second
// `EmbeddedNetworkPolicy::local` controller. Publication is revision-guarded
// exactly like the effective policy.
func (r *RuntimeRouter) refreshLocalApplicationNetworkPolicy() (network.NetworkPolicy, network.DestinationPolicy) {
	if r == nil || r.localNetworkPolicy == nil {
		return network.UnmanagedNetworkPolicy(), network.UnrestrictedDestinationPolicy()
	}
	r.localNetworkPolicyMu.Lock()
	defer r.localNetworkPolicyMu.Unlock()
	policy := r.localNetworkPolicy.Policy()
	composed := destinationPolicyFromApplicationRequirements(r.localApplicationNetworkRequirements())
	r.localNetworkPolicy.Publish(policy.Revision(), composed)
	return policy, composed
}

// bootstrapAuthHTTPClient binds the local application policy to a client used
// before the effective policy exists (Rust's
// `EmbeddedNetworkPolicy::bind_bootstrap_auth`): a restricted local policy limits
// login and cloud-bootstrap traffic to the destinations administrators allowed
// locally, while an unrestricted one leaves the caller's client shared.
func (r *RuntimeRouter) bootstrapAuthHTTPClient(client *http.Client) *http.Client {
	if client == nil || r == nil {
		return client
	}
	policy, composed := r.refreshLocalApplicationNetworkPolicy()
	if !policy.IsScoped() || !composed.IsRestricted() {
		return client
	}
	return network.PolicyHTTPClient(policy, client)
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
	// Rust #47408 routes the AWS SDK's credential and region requests through the
	// application client, so the process-scoped client follows the published
	// generation.
	if composed.IsRestricted() {
		model.SetAWSApplicationClient(network.PolicyHTTPClient(policy, http.DefaultClient), policy)
	} else {
		model.SetAWSApplicationClient(nil, network.UnmanagedNetworkPolicy())
	}
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

// applicationNetworkPolicyHandle returns the published policy handle the host's
// non-router transports (OTLP exports, for example) bind to.
func (r *RuntimeRouter) applicationNetworkPolicyHandle() network.NetworkPolicy {
	if r == nil || r.networkPolicy == nil {
		return network.UnmanagedNetworkPolicy()
	}
	return r.networkPolicy.Policy()
}

// remoteControlRestrictedPolicy returns the router's published policy when it
// restricts application destinations. Rust #47410 routes the remote-control
// server API and WebSocket dial through that policy.
func (r *RuntimeRouter) remoteControlRestrictedPolicy() (network.NetworkPolicy, bool) {
	if r == nil {
		return network.UnmanagedNetworkPolicy(), false
	}
	policy, composed := r.refreshApplicationNetworkPolicy()
	if !policy.IsManaged() || !composed.IsRestricted() {
		return policy, false
	}
	return policy, true
}

// remoteControlHTTPDoer returns the doer the remote-control server API uses for
// enrollment and server requests; a nil result keeps the package default.
func (r *RuntimeRouter) remoteControlHTTPDoer() remotecontrol.HTTPDoer {
	policy, restricted := r.remoteControlRestrictedPolicy()
	if !restricted {
		return nil
	}
	return &network.PolicyHTTPDoer{Policy: policy, Next: http.DefaultClient}
}

// remoteControlWebsocketHTTPClient returns the client the remote-control
// WebSocket dial uses; a nil result keeps the websocket package default.
func (r *RuntimeRouter) remoteControlWebsocketHTTPClient() *http.Client {
	policy, restricted := r.remoteControlRestrictedPolicy()
	if !restricted {
		return nil
	}
	return network.PolicyHTTPClient(policy, http.DefaultClient)
}
