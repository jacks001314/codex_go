package config

import (
	"strings"
	"testing"
)

func mcpEMATrustedTable(issuer string, clientID string) map[string]any {
	return map[string]any{
		"mcp_enterprise_managed_auth": map[string]any{
			"idp": map[string]any{"issuer": issuer, "client_id": clientID},
		},
	}
}

func TestMCPEnterpriseManagedAuthIdPPrecedence(t *testing.T) {
	system := LayerSource{Type: LayerSourceSystem, File: "system.toml"}
	user := LayerSource{Type: LayerSourceUser, File: "user.toml"}
	project := LayerSource{Type: LayerSourceProject, DotCodexFolder: ".codex"}
	trusted := mcpEMATrustedTable("https://idp.example", "enterprise")
	replacement := mcpEMATrustedTable("https://other.example", "other")
	expected := &MCPEnterpriseManagedAuthConfig{
		IDP: MCPServerIdPOAuthConfig{Issuer: "https://idp.example", ClientID: "enterprise"},
	}

	cases := []struct {
		name   string
		layers []Layer
		want   *MCPEnterpriseManagedAuthConfig
	}{
		{
			"managed wins over user and project",
			[]Layer{{Name: system, Config: trusted}, {Name: user, Config: replacement}, {Name: project, Config: replacement}},
			expected,
		},
		{
			"user wins over project",
			[]Layer{{Name: user, Config: trusted}, {Name: project, Config: replacement}},
			expected,
		},
		{
			"project setting alone is ignored",
			[]Layer{{Name: project, Config: trusted}},
			nil,
		},
		{
			"managed wins over partial user section",
			[]Layer{
				{Name: system, Config: trusted},
				{Name: user, Config: map[string]any{
					"mcp_enterprise_managed_auth": map[string]any{"idp": map[string]any{"client_id": "partial"}},
				}},
			},
			expected,
		},
	}
	for _, tc := range cases {
		got, err := mcpEnterpriseManagedAuthFromLayers(tc.layers, nil)
		if err != nil {
			t.Fatalf("%s: unexpected error %v", tc.name, err)
		}
		if (got == nil) != (tc.want == nil) {
			t.Fatalf("%s: got %#v want %#v", tc.name, got, tc.want)
		}
		if got != nil && *got != *tc.want {
			t.Fatalf("%s: got %#v want %#v", tc.name, got, tc.want)
		}
	}

	incomplete := []Layer{{
		Name: LayerSource{Type: LayerSourceSessionFlags},
		Config: map[string]any{
			"mcp_enterprise_managed_auth": map[string]any{"idp": map[string]any{"client_id": "partial"}},
		},
	}}
	if _, err := mcpEnterpriseManagedAuthFromLayers(incomplete, nil); err == nil {
		t.Fatal("an incomplete non-project registration must fail")
	}
}

func mcpEMAManagedEnterpriseLayer() Layer {
	return Layer{
		// EnterpriseManaged has lower precedence than a trusted project layer
		// (mirrors Rust's cloud-bundle managed layer), so a project override
		// must be rejected rather than silently winning.
		Name: LayerSource{Type: LayerSourceEnterpriseManaged, ID: "enterprise", Name: "Enterprise"},
		Config: map[string]any{
			"features": map[string]any{"use_xaa": true},
			"mcp_enterprise_managed_auth": map[string]any{
				"idp": map[string]any{"issuer": "https://idp.example", "client_id": "idp-client"},
			},
			"mcp_servers": map[string]any{
				"enterprise": map[string]any{
					"url":            "https://resource.example/mcp",
					"auth":           MCPServerAuthEMAAuth,
					"scopes":         []any{"files.read"},
					"oauth_resource": "https://resource.example/mcp",
					"oauth":          map[string]any{"client_id": "mcp-client"},
				},
			},
		},
	}
}

func TestMCPEnterpriseManagedAuthTrustedRegistrationPasses(t *testing.T) {
	layers := []Layer{mcpEMAManagedEnterpriseLayer()}
	effective := mcpEMAMergedConfig(layers)
	servers, _ := effective["mcp_servers"].(map[string]any)
	resolved, err := ResolveMCPEnterpriseManagedAuth(layers, nil, servers, true, nil)
	if err != nil {
		t.Fatalf("trusted registration error = %v", err)
	}
	if resolved == nil || resolved.IDP.ClientID != "idp-client" {
		t.Fatalf("resolved = %#v", resolved)
	}
}

func TestMCPEnterpriseManagedAuthRejectsProjectDowngrade(t *testing.T) {
	managed := mcpEMAManagedEnterpriseLayer()
	for _, projectAuth := range []string{"oauth", "chatgpt"} {
		project := Layer{
			Name: LayerSource{Type: LayerSourceProject, DotCodexFolder: ".codex"},
			Config: map[string]any{
				"mcp_servers": map[string]any{"enterprise": map[string]any{"auth": projectAuth}},
			},
		}
		layers := []Layer{managed, project}
		effective := mcpEMAMergedConfig(layers)
		servers, _ := effective["mcp_servers"].(map[string]any)
		_, err := ResolveMCPEnterpriseManagedAuth(layers, nil, servers, true, nil)
		if err == nil || !strings.Contains(err.Error(), "one non-project config layer") {
			t.Fatalf("project %q downgrade error = %v, want non-project layer error", projectAuth, err)
		}
	}
}

func TestMCPXAAOptInRequiresNonProjectSource(t *testing.T) {
	project := Layer{
		Name:   LayerSource{Type: LayerSourceProject, DotCodexFolder: ".codex"},
		Config: map[string]any{"features": map[string]any{"use_xaa": true}},
	}
	if _, err := ResolveMCPEnterpriseManagedAuth([]Layer{project}, nil, nil, true, nil); err == nil ||
		!strings.Contains(err.Error(), "must be selected in a non-project config layer") {
		t.Fatalf("project-only opt-in error = %v", err)
	}

	required := true
	if _, err := ResolveMCPEnterpriseManagedAuth([]Layer{project}, nil, nil, true, &required); err != nil {
		t.Fatalf("managed requirement opt-in error = %v", err)
	}

	user := Layer{
		Name:   LayerSource{Type: LayerSourceUser, File: "user.toml"},
		Config: map[string]any{"features": map[string]any{"use_xaa": true}},
	}
	if _, err := ResolveMCPEnterpriseManagedAuth([]Layer{user, project}, nil, nil, true, nil); err != nil {
		t.Fatalf("user opt-in error = %v", err)
	}
}

func TestMCPEMAAuthorizationServerIssuerRequiresEMAAuth(t *testing.T) {
	servers := map[string]any{
		"enterprise": map[string]any{
			"url":   "https://resource.example",
			"auth":  "oauth",
			"oauth": map[string]any{"authorization_server_issuer": "https://as.example"},
		},
	}
	if _, err := ResolveMCPEnterpriseManagedAuth(nil, nil, servers, false, nil); err == nil ||
		!strings.Contains(err.Error(), "requires auth = \"ema_auth\"") {
		t.Fatalf("issuer without ema_auth error = %v", err)
	}
}
