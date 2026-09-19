package model

import (
	"context"
	"errors"
	"strings"
	"testing"
)

type stubGatewayTokenProvider struct {
	token string
	err   error
}

func (s stubGatewayTokenProvider) ResolveAccessToken(context.Context) (string, error) {
	return s.token, s.err
}

func gatewayHeaderConfig(name string, scheme string) *GatewayOAuthConfig {
	return &GatewayOAuthConfig{
		AuthorizationURL: "https://issuer.example.com/authorize",
		TokenURL:         "https://issuer.example.com/token",
		ClientID:         "client-1",
		Delivery:         GatewayOAuthDelivery{Kind: "header", Name: name, Scheme: scheme},
	}
}

// Mirrors Rust combined_auth: the gateway token is attached through the
// configured header (with scheme) or cookie while primary auth stays intact.
func TestComposeGatewayAuthHeaderAndCookieLikeRust(t *testing.T) {
	primary := BearerAuthHeaders("primary-token", "account-1", false)
	composed, err := composeGatewayAuth(context.Background(), gatewayHeaderConfig("x-gateway-token", ""), stubGatewayTokenProvider{token: "gateway-token"}, primary)
	if err != nil {
		t.Fatalf("composeGatewayAuth(header) error = %v", err)
	}
	if got := composed.Headers.Get("Authorization"); got != "Bearer primary-token" {
		t.Fatalf("primary auth = %q", got)
	}
	if got := composed.Headers.Get("X-Gateway-Token"); got != "Bearer gateway-token" {
		t.Fatalf("gateway header = %q", got)
	}
	if primary.Headers.Get("X-Gateway-Token") != "" {
		t.Fatal("composition mutated the primary headers")
	}

	cookieConfig := gatewayHeaderConfig("gateway_session", "")
	cookieConfig.Delivery = GatewayOAuthDelivery{Kind: "cookie", Name: "gateway_session"}
	composed, err = composeGatewayAuth(context.Background(), cookieConfig, stubGatewayTokenProvider{token: "gateway-token"}, primary)
	if err != nil {
		t.Fatalf("composeGatewayAuth(cookie) error = %v", err)
	}
	if got := composed.Headers.Get("Cookie"); got != "gateway_session=gateway-token" {
		t.Fatalf("gateway cookie = %q", got)
	}
	if got := composed.Headers.Get("Authorization"); got != "Bearer primary-token" {
		t.Fatalf("primary auth after cookie = %q", got)
	}
}

// Mirrors Rust composition failures: empty tokens, invalid cookie values,
// invalid header names, primary-auth conflicts, missing runtime configuration,
// and sanitized issuer errors.
func TestComposeGatewayAuthFailuresLikeRust(t *testing.T) {
	primary := BearerAuthHeaders("primary-token", "", false)
	cases := []struct {
		name    string
		config  *GatewayOAuthConfig
		provider gatewayAuthTokenProvider
		want    string
	}{
		{
			name:     "empty token",
			config:   gatewayHeaderConfig("x-gateway-token", ""),
			provider: stubGatewayTokenProvider{},
			want:     "gateway OAuth returned an empty token",
		},
		{
			name:     "provider failure",
			config:   gatewayHeaderConfig("x-gateway-token", ""),
			provider: stubGatewayTokenProvider{err: errors.New("issuer leaked secret")},
			want:     "Gateway OAuth authentication failed; check the gateway configuration and credential store.",
		},
		{
			name:     "missing runtime configuration",
			config:   gatewayHeaderConfig("x-gateway-token", ""),
			provider: nil,
			want:     "gateway_oauth requires auth runtime configuration",
		},
		{
			name:     "invalid header name",
			config:   gatewayHeaderConfig("bad header", ""),
			provider: stubGatewayTokenProvider{token: "token"},
			want:     "invalid gateway header",
		},
		{
			name:     "conflicting primary header",
			config:   gatewayHeaderConfig("authorization", ""),
			provider: stubGatewayTokenProvider{token: "token"},
			want:     "gateway OAuth conflicts with primary auth headers",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := composeGatewayAuth(context.Background(), testCase.config, testCase.provider, primary)
			if err == nil || err.Error() != testCase.want {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
		})
	}

	cookieConfig := gatewayHeaderConfig("session", "")
	cookieConfig.Delivery = GatewayOAuthDelivery{Kind: "cookie", Name: "session"}
	_, err := composeGatewayAuth(context.Background(), cookieConfig, stubGatewayTokenProvider{token: "not a cookie,value"}, primary)
	if err == nil || err.Error() != "gateway OAuth token is not a valid cookie value" {
		t.Fatalf("cookie token error = %v", err)
	}
}

// Mirrors Rust shared_state::gateway_auth: matching gateway configurations and
// hosts share one manager, and different configurations get their own.
func TestSharedGatewayAuthManagerLikeRust(t *testing.T) {
	first, err := sharedGatewayAuthManager(gatewayHeaderConfig("x-gateway-token", ""), "/home/user/.codex")
	if err != nil || first == nil {
		t.Fatalf("sharedGatewayAuthManager() = %v, %v", first, err)
	}
	again, err := sharedGatewayAuthManager(gatewayHeaderConfig("x-gateway-token", ""), "/home/user/.codex")
	if err != nil || again != first {
		t.Fatalf("matching configuration must share the manager: %v vs %v (%v)", again, first, err)
	}
	otherHost, err := sharedGatewayAuthManager(gatewayHeaderConfig("x-gateway-token", ""), "/home/other/.codex")
	if err != nil || otherHost == first {
		t.Fatalf("a different codex home must not share the manager: %v (%v)", otherHost, err)
	}
	// A different OAuth configuration (issuer/client) gets its own manager; the
	// delivery header is not part of the credential identity.
	other := gatewayHeaderConfig("x-gateway-token", "")
	other.ClientID = "client-2"
	otherConfig, err := sharedGatewayAuthManager(other, "/home/user/.codex")
	if err != nil || otherConfig == first {
		t.Fatalf("a different configuration must not share the manager: %v (%v)", otherConfig, err)
	}
	if manager, err := sharedGatewayAuthManager(nil, "/home/user/.codex"); err != nil || manager != nil {
		t.Fatalf("no gateway configuration = %v, %v", manager, err)
	}
}

// Mirrors Rust models_identity: the gateway OAuth configuration participates in
// the model catalog cache identity.
func TestModelsCatalogIdentityIncludesGatewayOAuthLikeRust(t *testing.T) {
	provider := ProviderInfo{
		Name:    "custom",
		BaseURL: "https://gateway.example.com/v1",
	}
	withoutGateway := ModelsCatalogIdentity(&provider, nil, nil, "")
	withGateway := provider
	withGateway.GatewayOAuth = gatewayHeaderConfig("x-gateway-token", "")
	first := ModelsCatalogIdentity(&withGateway, nil, nil, "")
	if withoutGateway == "" || first == "" {
		t.Fatalf("identities = %q / %q, want non-empty", withoutGateway, first)
	}
	if first == withoutGateway {
		t.Fatal("adding gateway OAuth must change the catalog identity")
	}
	changed := withGateway
	changed.GatewayOAuth = gatewayHeaderConfig("x-other-token", "")
	if second := ModelsCatalogIdentity(&changed, nil, nil, ""); second == first {
		t.Fatal("a different gateway configuration must change the catalog identity")
	}
}

// Mirrors Rust provider setup: a provider whose gateway configuration fails
// validation reports the failure when authentication is requested.
func TestConfiguredProviderReportsGatewaySetupFailureLikeRust(t *testing.T) {
	info := ProviderInfo{
		Name:    "custom",
		BaseURL: "https://gateway.example.com/v1",
		GatewayOAuth: &GatewayOAuthConfig{
			AuthorizationURL: "https://issuer.example.com/authorize",
			TokenURL:         "https://issuer.example.com/token",
			Delivery:         GatewayOAuthDelivery{Kind: "header", Name: "x-gateway-token"},
		},
	}
	provider := CreateRuntimeProviderForID("custom", info, nil)
	configured, ok := provider.(*ConfiguredProvider)
	if !ok {
		t.Fatalf("provider = %T", provider)
	}
	if configured.gatewayAuthErr == nil {
		t.Fatal("a gateway configuration without a client id must record a setup failure")
	}
	if _, err := configured.APIAuth(); err == nil || !strings.Contains(err.Error(), "client_id") {
		t.Fatalf("APIAuth() error = %v, want the gateway validation error", err)
	}
}
