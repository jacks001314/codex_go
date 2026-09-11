package appserver

import (
	"context"

	"codex_go/apps"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/sandbox"
	"codex_go/turn"
)

// mcpElicitationsAllowedForApproval mirrors Rust's approval-policy gate for
// agent-initiated MCP elicitations: Never disables them, Granular requires the
// granular configuration to allow them, and the remaining policies permit them
// (Rust maybe_request_codex_apps_auth_elicitation).
func (r *RuntimeRouter) mcpElicitationsAllowedForApproval(cfg *config.Config, approvalPolicy sandbox.AskForApproval, active *turn.TurnStartParams) bool {
	switch approvalPolicy {
	case sandbox.ApprovalNever:
		return false
	case sandbox.ApprovalGranular:
		return granularMCPElicitationsAllowed(cfg, active)
	default:
		return true
	}
}

// mcpAuthElicitationOptions builds the turn's Codex Apps connector auth
// elicitation hooks. Request routes through the service's client-facing
// elicitation handler; RefreshCodexApps invalidates the service so the next
// turn re-lists the refreshed Codex Apps catalog (Go lists tools per turn).
func (r *RuntimeRouter) mcpAuthElicitationOptions(mcpService *mcp.MCPService) *mcp.AuthElicitationOptions {
	if r == nil || mcpService == nil {
		return nil
	}
	return &mcp.AuthElicitationOptions{
		Request: func(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
			handler := mcpService.ElicitationHandler()
			if handler == nil {
				return nil, nil
			}
			return handler.HandleMCPElicitation(ctx, request)
		},
		RefreshCodexApps: func(context.Context) error {
			mcpService.Refresh()
			return nil
		},
		InstallURL: apps.ConnectorInstallURL,
	}
}
