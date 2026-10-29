package app

// Application network policy for the CLI host.
//
// Rust parity: the embedded application policy (codex-rs/app-server/src/in_process_bootstrap.rs
// `EmbeddedNetworkPolicy::load`/`activate` plus `config_manager`'s
// `replace_cloud_config_bundle_loader`): the host owns the policy composed from
// its managed requirements, binds it to every config it produces, and limits
// cloud bootstrap with it.

import (
	"net/http"

	"codex_go/config"
	"codex_go/network"
)

// publishApplicationNetworkPolicy publishes the policy composed from the
// config's managed application requirements and binds it to the config, so every
// client built from that config shares one generation. It returns the read
// policy callers bind to their bootstrap transports.
func publishApplicationNetworkPolicy(cfg *config.Config) network.NetworkPolicy {
	if cfg == nil {
		return network.UnmanagedNetworkPolicy()
	}
	if owner := cfg.ApplicationNetworkPolicyOwner; owner != nil {
		return owner.Activate(cfg)
	}
	cfg.BindApplicationNetworkPolicy()
	return cfg.NetworkPolicy()
}

// publishHostApplicationNetworkPolicy publishes through the host-owned policy
// when the caller has one, so every config the host loads shares the same live
// generation (Rust `EmbeddedNetworkPolicy::activate`). Without an owner it
// keeps the per-config behavior used by one-shot commands.
func publishHostApplicationNetworkPolicy(owner *config.EmbeddedNetworkPolicy, cfg *config.Config) network.NetworkPolicy {
	if owner != nil {
		return owner.Activate(cfg)
	}
	return publishApplicationNetworkPolicy(cfg)
}

// policyHTTPClient publishes the host policy for the config when it has none yet
// and binds it to a concrete client, which the CLI host's transports (MCP,
// exec-server, code-mode host) need.
func policyHTTPClient(cfg *config.Config, client *http.Client) *http.Client {
	if cfg == nil {
		return client
	}
	if !cfg.NetworkPolicy().IsScoped() {
		cfg.BindApplicationNetworkPolicy()
	}
	if !cfg.RestrictsApplicationTraffic() {
		// An unrestricted generation permits every destination, so the caller
		// keeps its client unwrapped (and shared).
		return client
	}
	return network.PolicyHTTPClient(cfg.NetworkPolicy(), client)
}

// hostPolicyHTTPClient binds the host owner's live policy to a client even when
// the current generation is unrestricted, so a later reload that restricts
// destinations also reaches this client. A nil owner keeps the per-config
// behavior.
func hostPolicyHTTPClient(owner *config.EmbeddedNetworkPolicy, client *http.Client) *http.Client {
	if owner == nil {
		return client
	}
	return network.PolicyHTTPClient(owner.Effective(), client)
}
