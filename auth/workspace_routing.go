package auth

// Rust parity: codex-rs/app-server-protocol/src/protocol/v2/account.rs
// WorkspaceRouting / AccountRoutingOverride and
// codex-rs/login/src/auth/manager/workspace_routing.rs.

// AccountRoutingOverride is the backend routing policy for a workspace. The
// wire values match the accounts/check contract.
type AccountRoutingOverride string

const (
	AccountRoutingOverrideNoConstraint AccountRoutingOverride = "NO_CONSTRAINT"
	AccountRoutingOverrideUS           AccountRoutingOverride = "us"
	AccountRoutingOverrideUSCR         AccountRoutingOverride = "us_cr"
)

// ValidAccountRoutingOverride reports whether a raw accounts/check override
// value is one the resolver accepts.
func ValidAccountRoutingOverride(value string) bool {
	switch AccountRoutingOverride(value) {
	case AccountRoutingOverrideNoConstraint,
		AccountRoutingOverrideUS,
		AccountRoutingOverrideUSCR:
		return true
	default:
		return false
	}
}

// WorkspaceRouting is the discovered routing for the selected ChatGPT
// workspace: the workspace id, the backend origin model requests must use, and
// the account routing override.
type WorkspaceRouting struct {
	ChatGPTAccountID       string                 `json:"chatgptAccountId"`
	BackendOrigin          string                 `json:"backendOrigin"`
	AccountRoutingOverride AccountRoutingOverride `json:"accountRoutingOverride"`
}
