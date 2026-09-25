package exec

// Application network policy for the non-interactive runner.
//
// Rust parity: codex-rs/exec/src/lib.rs plus the embedded application policy in
// codex-rs/app-server/src/in_process_bootstrap.rs
// (`EmbeddedNetworkPolicy::load`/`activate`): the running host owns one policy,
// publishes it from the effective managed requirements, and every client the run
// builds binds that generation.

import (
	"net/http"

	"codex_go/config"
	"codex_go/model"
	"codex_go/network"
)

// publishApplicationNetworkPolicy publishes the policy composed from the
// config's managed application requirements and binds it to the config. The run
// keeps the returned controller so an account change can revoke outstanding
// operations.
func publishApplicationNetworkPolicy(cfg *config.Config) *network.NetworkPolicyController {
	if cfg == nil {
		return nil
	}
	controller := cfg.BindApplicationNetworkPolicy()
	// Rust #47408: the AWS SDK's credential and region requests use the
	// application client, so the run installs the policy-bound one.
	if cfg.RestrictsApplicationTraffic() {
		model.SetAWSHTTPClient(network.PolicyHTTPClient(cfg.NetworkPolicy(), http.DefaultClient))
	} else {
		model.SetAWSHTTPClient(nil)
	}
	return controller
}
