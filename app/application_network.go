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
	cfg.BindApplicationNetworkPolicy()
	return cfg.NetworkPolicy()
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
