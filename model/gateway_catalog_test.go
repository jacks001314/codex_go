package model

import "testing"

func gatewayCatalogProvider(base, catalog string) ProviderInfo {
	return ProviderInfo{
		Name:            "corp-gateway",
		BaseURL:         base,
		ModelCatalogURL: catalog,
		GatewayOAuth:    &GatewayOAuthConfig{},
	}
}

// Rust #60414: gateway catalog discovery requires the catalog and inference
// URLs to share an origin, and only applies to custom gateway providers.
func TestSupportsGatewayCatalogLikeRust(t *testing.T) {
	cases := []struct {
		name   string
		info   ProviderInfo
		expect bool
	}{
		{"same origin", gatewayCatalogProvider("https://gateway.example.com/v1", "https://gateway.example.com/v1/models"), true},
		{"different host", gatewayCatalogProvider("https://gateway.example.com/v1", "https://catalog.example.com/models"), false},
		{"different scheme", gatewayCatalogProvider("https://gateway.example.com/v1", "http://gateway.example.com/models"), false},
		{"missing catalog", gatewayCatalogProvider("https://gateway.example.com/v1", ""), false},
		{"missing base", gatewayCatalogProvider("", "https://gateway.example.com/models"), false},
		{"invalid url", gatewayCatalogProvider("not a url", "https://gateway.example.com/models"), false},
	}
	for _, test := range cases {
		if got := SupportsGatewayCatalog(test.info); got != test.expect {
			t.Fatalf("%s: SupportsGatewayCatalog = %v, want %v", test.name, got, test.expect)
		}
	}

	noGateway := gatewayCatalogProvider("https://gateway.example.com/v1", "https://gateway.example.com/models")
	noGateway.GatewayOAuth = nil
	if SupportsGatewayCatalog(noGateway) {
		t.Fatal("a provider without gateway OAuth has no gateway catalog")
	}
	openAI := gatewayCatalogProvider("https://api.openai.com/v1", "https://api.openai.com/v1/models")
	openAI.Name = OpenAIProviderName
	if SupportsGatewayCatalog(openAI) {
		t.Fatal("the built-in OpenAI provider is not a gateway catalog provider")
	}
}

// Rust #60414: an unrelated saved API key must not make gateway catalog
// discovery depend on the API-key discovery setting, while an explicit provider
// key or a requires_openai_auth provider still opts in.
func TestUsesAPIKeyAuthKeepsGatewayCatalogIndependentLikeRust(t *testing.T) {
	gateway := gatewayCatalogProvider("https://gateway.example.com/v1", "https://gateway.example.com/v1/models")
	if UsesAPIKeyAuth(gateway, true) {
		t.Fatal("a saved API key must not gate gateway catalog discovery")
	}
	if UsesAPIKeyAuth(gateway, false) {
		t.Fatal("gateway catalog discovery is not API-key auth")
	}
	explicitKey := gateway
	explicitKey.ExperimentalBearerToken = "token"
	if !UsesAPIKeyAuth(explicitKey, false) {
		t.Fatal("an explicit provider key opts into API-key auth")
	}
	requiresOpenAI := gateway
	requiresOpenAI.RequiresOpenAIAuth = true
	if !UsesAPIKeyAuth(requiresOpenAI, true) {
		t.Fatal("requires_openai_auth keeps the saved API key in play")
	}
	plain := ProviderInfo{Name: "plain", BaseURL: "https://plain.example.com/v1"}
	if !UsesAPIKeyAuth(plain, true) || UsesAPIKeyAuth(plain, false) {
		t.Fatal("a provider without gateway OAuth follows the API-key login")
	}
}
