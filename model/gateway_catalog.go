package model

// Rust #60414: catalog requests use the model provider's credentials. Before a
// refresh may run with gateway OAuth alone, the catalog URL must share its
// origin with the gateway the provider already trusts.

import (
	"net/url"
	"strings"
)

// SupportsGatewayCatalog mirrors Rust's supports_gateway_catalog: a custom
// gateway provider with an explicit model catalog discovers models without an
// API key when the catalog and inference URLs have the same origin.
func SupportsGatewayCatalog(info ProviderInfo) bool {
	if info.IsOpenAI() || info.GatewayOAuth == nil {
		return false
	}
	base := strings.TrimSpace(info.BaseURL)
	catalog := strings.TrimSpace(info.ModelCatalogURL)
	if base == "" || catalog == "" {
		return false
	}
	baseURL, err := url.Parse(base)
	if err != nil || !validEndpointURL(baseURL) {
		return false
	}
	catalogURL, err := url.Parse(catalog)
	if err != nil || !validEndpointURL(catalogURL) {
		return false
	}
	return baseURL.Scheme == catalogURL.Scheme && baseURL.Host == catalogURL.Host
}

// validEndpointURL mirrors the http(s)-with-host check Rust's
// validate_endpoint_url performs before comparing origins.
func validEndpointURL(parsed *url.URL) bool {
	if parsed == nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}
	return parsed.Host != ""
}

// UsesAPIKeyAuth mirrors Rust's uses_api_key_auth(login) (Rust #60414). An
// explicit provider key opts the session into API-key discovery; an unrelated
// saved API key must not make gateway catalog discovery depend on the API-key
// discovery setting.
func UsesAPIKeyAuth(info ProviderInfo, apiKeyLogin bool) bool {
	if info.HasProviderAPIKey() {
		return true
	}
	return apiKeyLogin && (!SupportsGatewayCatalog(info) || info.RequiresOpenAIAuth)
}
