package mcp

import "testing"

// Mirrors Rust #62876 McpConnectionSet::has_failed_startup_for_retry: only a
// completed, non-auth startup failure of an ordinary server is retryable.
func TestMCPHasFailedStartupForRetryLikeRust(t *testing.T) {
	reason := mcpFailureReasonReauthenticationRequired
	tests := []struct {
		name   string
		status MCPServerStatus
		want   bool
	}{
		{name: "starting handshake", status: MCPServerStatus{Name: "server", State: MCPServerStarting}},
		{name: "cancelled", status: MCPServerStatus{Name: "server", State: MCPServerCancelled}},
		{name: "ordinary failure", status: MCPServerStatus{Name: "server", State: MCPServerFailed, AuthStatus: MCPAuthUnsupported}, want: true},
		{name: "oauth server transient failure", status: MCPServerStatus{Name: "server", State: MCPServerFailed, AuthStatus: MCPAuthOAuth}, want: true},
		{name: "reauthentication required", status: MCPServerStatus{Name: "server", State: MCPServerFailed, AuthStatus: MCPAuthNotLoggedIn, FailureReason: &reason}},
		{name: "logged out", status: MCPServerStatus{Name: "server", State: MCPServerFailed, AuthStatus: MCPAuthNotLoggedIn}},
		{name: "apps owns reconnect", status: MCPServerStatus{Name: RuntimeCodexAppsMCPServerName, State: MCPServerFailed, AuthStatus: MCPAuthUnsupported}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service := NewMCPService(nil)
			service.SetServer(tt.status)
			if got := service.HasFailedStartupForRetry(); got != tt.want {
				t.Fatalf("HasFailedStartupForRetry() = %v, want %v", got, tt.want)
			}
		})
	}
	var nilService *MCPService
	if nilService.HasFailedStartupForRetry() {
		t.Fatal("nil service reported a retryable startup failure")
	}
}
