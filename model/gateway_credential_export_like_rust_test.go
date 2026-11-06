package model

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

// TestGatewayCredentialExportLikeRust mirrors Rust #59787: a configured export
// command supplies the gateway token without an OAuth session, the token is
// cached after the first run, and mixing OAuth settings with an exporter is
// rejected.
func TestGatewayCredentialExportLikeRust(t *testing.T) {
	config := &GatewayOAuthConfig{
		CredentialExport: &GatewayCredentialExportConfig{Command: "cmd", Args: []string{"/c", "echo gw-token"}},
		Delivery:         GatewayOAuthDelivery{Kind: "header", Name: "x-gateway-token"},
	}
	if !config.UsesCredentialExport() || !config.RequiresGatewayOrigin() {
		t.Fatalf("export config = %+v, want credential-export origin restriction", config)
	}
	provider := &ProviderInfo{Name: "custom", BaseURL: "https://gateway.example/v1", GatewayOAuth: config}
	if err := provider.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	if runtime.GOOS != "windows" {
		t.Skip("the export command fixture is Windows cmd syntax")
	}
	export := newGatewayCredentialExport(config.CredentialExport)
	token, err := export.ResolveAccessToken(context.Background())
	if err != nil {
		t.Fatalf("ResolveAccessToken() error = %v", err)
	}
	if token != "gw-token" {
		t.Fatalf("token = %q, want the exported token", token)
	}
	export = newGatewayCredentialExport(&GatewayCredentialExportConfig{Command: "cmd", Args: []string{"/c", "exit 1"}})
	export.token = "gw-token"
	cached, err := export.ResolveAccessToken(context.Background())
	if err != nil || cached != "gw-token" {
		t.Fatalf("cached token = %q, %v; want the in-memory token", cached, err)
	}
	export.invalidate()
	if _, err := export.ResolveAccessToken(context.Background()); err == nil {
		t.Fatalf("failed export was cached; want a retry that surfaces the failure")
	}

	mixed := &GatewayOAuthConfig{
		AuthorizationURL: "https://gateway.example/authorize",
		TokenURL:         "https://gateway.example/token",
		ClientID:         "client",
		CredentialExport: &GatewayCredentialExportConfig{Command: "cmd"},
	}
	err = mixed.Validate(&ProviderInfo{BaseURL: "https://gateway.example/v1"})
	if err == nil || !strings.Contains(err.Error(), "choose one gateway credential source") {
		t.Fatalf("mixed config error = %v, want the mutual-exclusion message", err)
	}
}
