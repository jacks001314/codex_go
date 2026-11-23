package network

import (
	"net/http"
	"testing"
)

// TestProxyServerLocalGrantSatisfiesControllerConstraintsLikeRust mirrors Rust
// #62882's standalone/attached local-grant test: a standalone proxy may narrow a
// managed wildcard to an explicit local grant, while an attached executor's grant
// must also satisfy the controller's allowlist.
func TestProxyServerLocalGrantSatisfiesControllerConstraintsLikeRust(t *testing.T) {
	newServer := func(standalone bool, constraints ProxyConstraints) *ProxyServer {
		settings := DefaultProxySettings()
		settings.Enabled = true
		settings.AllowLocalBinding = false
		settings.AllowLocalBindingSet = true
		settings.SetAllowedDomains([]string{"localhost"})
		server := &ProxyServer{}
		setProxyServerSettingsForTest(server, settings)
		policy := server.runtimePolicy()
		policy.constraints = constraints
		policy.standalone = standalone
		return server
	}
	wildcardAllowed := []string{"**.localhost"}
	constraints := ProxyConstraints{AllowedDomains: &wildcardAllowed}

	if reason := newServer(true, constraints).blockReason(http.MethodGet, ProxyProtocolHTTP, "localhost", 80); reason != "" {
		t.Fatalf("standalone local grant reason = %q, want allowed", reason)
	}
	if reason := newServer(false, constraints).blockReason(http.MethodGet, ProxyProtocolHTTP, "localhost", 80); reason != ProxyReasonNotAllowedLocal {
		t.Fatalf("attached local grant reason = %q, want %q", reason, ProxyReasonNotAllowedLocal)
	}

	expansion := true
	if reason := newServer(false, ProxyConstraints{AllowedDomains: &wildcardAllowed, AllowlistExpansionEnabled: &expansion}).blockReason(http.MethodGet, ProxyProtocolHTTP, "localhost", 80); reason != "" {
		t.Fatalf("expanded attached local grant reason = %q, want allowed", reason)
	}

	explicitAllowed := []string{"localhost"}
	if reason := newServer(false, ProxyConstraints{AllowedDomains: &explicitAllowed}).blockReason(http.MethodGet, ProxyProtocolHTTP, "localhost", 80); reason != "" {
		t.Fatalf("explicit controller local grant reason = %q, want allowed", reason)
	}
}
