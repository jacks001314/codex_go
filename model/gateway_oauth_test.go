package model

import (
	"strings"
	"testing"
)

// gatewayOAuthProviderValues mirrors Rust's gateway_oauth_tests::provider
// fixture: a provider whose gateway requires secondary OAuth credentials.
func gatewayOAuthProviderValues(gatewayExtra map[string]any, providerExtra map[string]any) map[string]any {
	gateway := map[string]any{
		"authorization_url": "https://login.example.test/authorize",
		"token_url":         "https://login.example.test/token",
		"client_id":         "codex",
	}
	for key, value := range gatewayExtra {
		gateway[key] = value
	}
	values := map[string]any{
		"name":          "gateway",
		"base_url":      "https://gateway.example.test/v1",
		"wire_api":      "responses",
		"gateway_oauth": gateway,
	}
	for key, value := range providerExtra {
		values[key] = value
	}
	return values
}

// TestGatewayOAuthParsesHeaderAndCookieDelivery mirrors Rust's
// parses_header_and_cookie_delivery.
func TestGatewayOAuthParsesHeaderAndCookieDelivery(t *testing.T) {
	header, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(map[string]any{
		"delivery": map[string]any{"kind": "header", "name": "x-gateway-auth"},
	}, nil))
	if err != nil {
		t.Fatalf("header delivery error = %v", err)
	}
	if header.GatewayOAuth == nil {
		t.Fatal("gateway_oauth = nil")
	}
	if header.GatewayOAuth.Delivery.Kind != "header" || header.GatewayOAuth.Delivery.Name != "x-gateway-auth" ||
		header.GatewayOAuth.Delivery.HeaderScheme() != "Bearer" {
		t.Fatalf("header delivery = %#v", header.GatewayOAuth.Delivery)
	}
	if got := header.GatewayOAuth.Delivery.EffectiveHeaderName(); got != "x-gateway-auth" {
		t.Fatalf("effective header = %q", got)
	}

	cookie, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(map[string]any{
		"delivery": map[string]any{"kind": "cookie", "name": "gateway_session"},
	}, nil))
	if err != nil {
		t.Fatalf("cookie delivery error = %v", err)
	}
	if cookie.GatewayOAuth == nil || cookie.GatewayOAuth.Delivery.Kind != "cookie" ||
		cookie.GatewayOAuth.Delivery.Name != "gateway_session" {
		t.Fatalf("cookie delivery = %#v", cookie.GatewayOAuth)
	}
	if got := cookie.GatewayOAuth.Delivery.EffectiveHeaderName(); got != "cookie" {
		t.Fatalf("effective cookie header = %q", got)
	}
	// An explicit scheme is preserved for the header kind.
	explicit, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(map[string]any{
		"delivery": map[string]any{"kind": "header", "name": "x-gateway-auth", "scheme": "Token"},
	}, nil))
	if err != nil {
		t.Fatalf("explicit scheme error = %v", err)
	}
	if got := explicit.GatewayOAuth.Delivery.HeaderScheme(); got != "Token" {
		t.Fatalf("explicit scheme = %q, want Token", got)
	}
}

// TestGatewayOAuthRejectsUnsafeEndpointsAndDeliveryCollisions mirrors Rust's
// rejects_unsafe_endpoints_and_delivery_collisions.
func TestGatewayOAuthRejectsUnsafeEndpointsAndDeliveryCollisions(t *testing.T) {
	valid, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(map[string]any{
		"delivery": map[string]any{"kind": "header", "name": "x-gateway-auth"},
	}, nil))
	if err != nil {
		t.Fatalf("fixture error = %v", err)
	}
	for _, unsafeURL := range []string{
		"http://remote.example.test",
		"https://user:secret@gateway.example.test",
		"https://gateway.example.test/#secret",
		"file:///token",
	} {
		invalid := *valid
		invalid.GatewayOAuth = cloneGatewayOAuthForTest(valid.GatewayOAuth)
		invalid.BaseURL = unsafeURL
		if err := invalid.Validate(); err == nil {
			t.Fatalf("base_url %q was accepted", unsafeURL)
		}
		invalid = *valid
		invalid.GatewayOAuth = cloneGatewayOAuthForTest(valid.GatewayOAuth)
		invalid.GatewayOAuth.TokenURL = unsafeURL
		validationErr := invalid.Validate()
		if validationErr == nil {
			t.Fatalf("token_url %q was accepted", unsafeURL)
		}
		if strings.Contains(validationErr.Error(), unsafeURL) {
			t.Fatalf("error %q echoed the URL", validationErr.Error())
		}
	}
	for _, name := range []string{
		"HOST",
		"x-openai-actor-authorization",
		"content-length",
		"sec-websocket-key",
		"bad name",
	} {
		invalid := *valid
		invalid.GatewayOAuth = cloneGatewayOAuthForTest(valid.GatewayOAuth)
		invalid.GatewayOAuth.Delivery = GatewayOAuthDelivery{Kind: "header", Name: name}
		if err := invalid.Validate(); err == nil {
			t.Fatalf("header name %q was accepted", name)
		}
	}
	collision := *valid
	collision.GatewayOAuth = cloneGatewayOAuthForTest(valid.GatewayOAuth)
	collision.HTTPHeaders = map[string]string{"X-Gateway-Auth": "static"}
	if err := collision.Validate(); err == nil {
		t.Fatal("a static header collision was accepted")
	}
	envCollision, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(map[string]any{
		"delivery": map[string]any{"kind": "cookie", "name": "gateway"},
	}, map[string]any{
		"env_http_headers": map[string]any{"COOKIE": "COOKIE_ENV"},
	}))
	if err == nil && envCollision != nil {
		t.Fatal("an env header collision was accepted")
	}
}

// TestGatewayOAuthRejectsAWSAndIncompleteConfigs covers the remaining
// validation arms of Rust's GatewayOAuthConfig::validate.
func TestGatewayOAuthRejectsAWSAndIncompleteConfigs(t *testing.T) {
	base, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(map[string]any{
		"delivery": map[string]any{"kind": "header", "name": "x-gateway-auth"},
	}, nil))
	if err != nil {
		t.Fatalf("fixture error = %v", err)
	}
	aws := *base
	aws.GatewayOAuth = cloneGatewayOAuthForTest(base.GatewayOAuth)
	aws.AWS = &ProviderAWSAuthInfo{Profile: "default"}
	if err := aws.Validate(); err == nil || !strings.Contains(err.Error(), "cannot be combined with AWS authentication") {
		t.Fatalf("aws error = %v", err)
	}
	missingBase := *base
	missingBase.GatewayOAuth = cloneGatewayOAuthForTest(base.GatewayOAuth)
	missingBase.BaseURL = ""
	if err := missingBase.Validate(); err == nil || !strings.Contains(err.Error(), "requires base_url") {
		t.Fatalf("missing base_url error = %v", err)
	}
	emptyClient := *base
	emptyClient.GatewayOAuth = cloneGatewayOAuthForTest(base.GatewayOAuth)
	emptyClient.GatewayOAuth.ClientID = "   "
	if err := emptyClient.Validate(); err == nil || !strings.Contains(err.Error(), "nonempty client_id") {
		t.Fatalf("empty client_id error = %v", err)
	}
	zeroPort := *base
	zeroPort.GatewayOAuth = cloneGatewayOAuthForTest(base.GatewayOAuth)
	port := uint16(0)
	zeroPort.GatewayOAuth.RedirectPort = &port
	if err := zeroPort.Validate(); err == nil || !strings.Contains(err.Error(), "nonzero redirect_port") {
		t.Fatalf("zero redirect_port error = %v", err)
	}
	badScheme := *base
	badScheme.GatewayOAuth = cloneGatewayOAuthForTest(base.GatewayOAuth)
	badScheme.GatewayOAuth.Delivery.Scheme = "Bad Scheme"
	if err := badScheme.Validate(); err == nil || !strings.Contains(err.Error(), "reserved gateway_oauth delivery header or scheme") {
		t.Fatalf("bad scheme error = %v", err)
	}
	badCookie := *base
	badCookie.GatewayOAuth = cloneGatewayOAuthForTest(base.GatewayOAuth)
	badCookie.GatewayOAuth.Delivery = GatewayOAuthDelivery{Kind: "cookie", Name: "bad name"}
	if err := badCookie.Validate(); err == nil || !strings.Contains(err.Error(), "invalid gateway_oauth cookie name") {
		t.Fatalf("bad cookie error = %v", err)
	}
}

// TestGatewayOAuthDefaultsToAuthorizationBearer mirrors Rust #57313: omitting
// delivery sends the gateway token as Authorization: Bearer.
func TestGatewayOAuthDefaultsToAuthorizationBearer(t *testing.T) {
	provider, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(nil, nil))
	if err != nil {
		t.Fatalf("default delivery error = %v", err)
	}
	if provider.GatewayOAuth == nil {
		t.Fatal("gateway_oauth = nil")
	}
	if got := provider.GatewayOAuth.Delivery; got.Kind != "header" || got.Name != "Authorization" || got.HeaderScheme() != "Bearer" {
		t.Fatalf("default delivery = %#v", got)
	}
	if !provider.GatewayOAuth.RequiresGatewayOrigin() {
		t.Fatal("default delivery must require the gateway origin")
	}
	name, value, err := gatewayAuthHeader(provider.GatewayOAuth, "gateway-token")
	if err != nil || name != "Authorization" || value != "Bearer gateway-token" {
		t.Fatalf("gateway header = %q %q, %v", name, value, err)
	}
	// An explicitly configured custom header keeps its own config.
	custom, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(map[string]any{
		"delivery": map[string]any{"kind": "header", "name": "x-gateway-auth"},
	}, nil))
	if err != nil {
		t.Fatalf("custom delivery error = %v", err)
	}
	if custom.GatewayOAuth.RequiresGatewayOrigin() {
		t.Fatal("a custom header must not require the gateway origin")
	}
}

// TestGatewayOAuthBearerRequiresCatalogOrigin mirrors Rust #57313: bearer
// delivery refuses to send gateway credentials to another catalog origin.
func TestGatewayOAuthBearerRequiresCatalogOrigin(t *testing.T) {
	provider, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(nil, nil))
	if err != nil {
		t.Fatalf("fixture error = %v", err)
	}
	otherOrigin := *provider
	otherOrigin.ModelCatalogURL = "https://gateway.example.test:8443/catalog"
	if err := otherOrigin.Validate(); err == nil || !strings.Contains(err.Error(), "same scheme, host, and port as base_url") {
		t.Fatalf("cross-origin catalog error = %v", err)
	}
	sameOrigin := *provider
	sameOrigin.ModelCatalogURL = "https://GATEWAY.example.test:443/custom/catalog"
	if err := sameOrigin.Validate(); err != nil {
		t.Fatalf("same-origin catalog error = %v", err)
	}
	legacy, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(map[string]any{
		"delivery": map[string]any{"kind": "header", "name": "x-gateway-auth"},
	}, nil))
	if err != nil {
		t.Fatalf("legacy fixture error = %v", err)
	}
	legacy.ModelCatalogURL = "https://gateway.example.test:8443/catalog"
	if err := legacy.Validate(); err != nil {
		t.Fatalf("custom-header catalog error = %v", err)
	}
}

// TestGatewayOAuthBearerRejectsProviderCredentials mirrors Rust #57313: a
// provider credential sharing the Authorization header is a conflict.
func TestGatewayOAuthBearerRejectsProviderCredentials(t *testing.T) {
	base, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(nil, nil))
	if err != nil {
		t.Fatalf("fixture error = %v", err)
	}
	for name, mutate := range map[string]func(*ProviderInfo){
		"requires_openai_auth":      func(info *ProviderInfo) { info.RequiresOpenAIAuth = true },
		"env_key":                   func(info *ProviderInfo) { info.EnvKey = "PROVIDER_API_KEY" },
		"experimental_bearer_token": func(info *ProviderInfo) { info.ExperimentalBearerToken = "token" },
		"command_auth":              func(info *ProviderInfo) { info.Auth = &ProviderAuthInfo{Command: "print-token"} },
	} {
		t.Run(name, func(t *testing.T) {
			info := *base
			info.GatewayOAuth = cloneGatewayOAuthForTest(base.GatewayOAuth)
			mutate(&info)
			if err := info.Validate(); err == nil || !strings.Contains(err.Error(), "gateway_oauth.delivery") {
				t.Fatalf("conflict error = %v", err)
			}
		})
	}
	explicit, err := ProviderInfoFromConfig(gatewayOAuthProviderValues(map[string]any{
		"delivery": map[string]any{"kind": "header", "name": "x-gateway-auth"},
	}, nil))
	if err != nil {
		t.Fatalf("explicit fixture error = %v", err)
	}
	explicit.EnvKey = "PROVIDER_API_KEY"
	if err := explicit.Validate(); err != nil {
		t.Fatalf("custom header with a provider key error = %v", err)
	}
}

func cloneGatewayOAuthForTest(config *GatewayOAuthConfig) *GatewayOAuthConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	cloned.Scopes = append([]string(nil), config.Scopes...)
	return &cloned
}
