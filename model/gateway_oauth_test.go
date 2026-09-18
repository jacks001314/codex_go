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
		"Authorization",
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

func cloneGatewayOAuthForTest(config *GatewayOAuthConfig) *GatewayOAuthConfig {
	if config == nil {
		return nil
	}
	cloned := *config
	cloned.Scopes = append([]string(nil), config.Scopes...)
	return &cloned
}
