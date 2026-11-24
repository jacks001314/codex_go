package mcp

import (
	"testing"
)

// TestMcpRequestAuthBypassesSharedStartupCatalogLikeRust mirrors Rust #63532:
// a provider-backed server never shares the process-wide startup tool catalog,
// because its tool list belongs to the identity that opened the connection.
func TestMcpRequestAuthBypassesSharedStartupCatalogLikeRust(t *testing.T) {
	plain := ServerConfig{URL: "https://example.com/mcp", Enabled: true}
	if _, ok := mcpToolCatalogIdentity("srv", &plain); !ok {
		t.Fatal("plain HTTP server should share the startup catalog")
	}
	if _, eligible := mcpToolCatalogGraceKey("srv", &plain, false, false, false); !eligible {
		t.Fatal("plain HTTP server should be grace-key eligible")
	}

	provider := NewMcpRequestAuthProvider(&rotatingAuth{owner: "owner-1", token: "one"})
	config, err := SetMcpRequestAuthProvider(&ServerConfig{URL: "https://example.com/mcp", Enabled: true}, provider)
	if err != nil {
		t.Fatalf("SetMcpRequestAuthProvider() error = %v", err)
	}
	if _, ok := mcpToolCatalogIdentity("srv", config); ok {
		t.Fatal("provider-backed server must not use the shared startup catalog identity")
	}
	if _, eligible := mcpToolCatalogGraceKey("srv", config, false, false, false); eligible {
		t.Fatal("provider-backed server must not share the startup grace catalog")
	}
}

// TestMcpRequestAuthSurvivesMaterializationForSameTransportLikeRust mirrors
// Rust #63532: refreshing a catalog entry keeps the provider while the
// transport is unchanged.
func TestMcpRequestAuthSurvivesMaterializationForSameTransportLikeRust(t *testing.T) {
	provider := NewMcpRequestAuthProvider(&rotatingAuth{owner: "owner-1", token: "one"})
	config, err := SetMcpRequestAuthProvider(&ServerConfig{URL: "https://example.com/mcp", Enabled: true}, provider)
	if err != nil {
		t.Fatalf("SetMcpRequestAuthProvider() error = %v", err)
	}
	catalog := ResolveCatalog([]CatalogAction{{
		Name:   "srv",
		Source: CatalogSourceConfig,
		Config: *config,
	}})
	if got := catalog.Servers["srv"].RequestAuthProvider; got != provider {
		t.Fatalf("resolved provider = %v, want %v", got, provider)
	}

	rebuilt := catalog.WithMaterializedServers(map[string]ServerConfig{
		"srv": {URL: "https://example.com/mcp", Enabled: true},
	})
	server, ok := rebuilt.Servers["srv"]
	if !ok {
		t.Fatal("materialized catalog dropped srv")
	}
	if !server.Config.Enabled {
		t.Fatal("same-transport materialization disabled the provider-backed server")
	}
	if server.Config.RequestAuthProvider != provider {
		t.Fatalf("materialized provider = %v, want the original provider", server.Config.RequestAuthProvider)
	}
}

// TestMcpRequestAuthDroppedAfterTransportChangeLikeRust mirrors Rust #63532:
// a materialized registration cannot retarget in-process credentials, so a
// changed transport disables the server instead of keeping a stale provider.
func TestMcpRequestAuthDroppedAfterTransportChangeLikeRust(t *testing.T) {
	provider := NewMcpRequestAuthProvider(&rotatingAuth{owner: "owner-1", token: "one"})
	config, err := SetMcpRequestAuthProvider(&ServerConfig{URL: "https://example.com/mcp", Enabled: true}, provider)
	if err != nil {
		t.Fatalf("SetMcpRequestAuthProvider() error = %v", err)
	}
	catalog := ResolveCatalog([]CatalogAction{
		{Name: "srv", Source: CatalogSourceConfig, Config: *config},
		{Name: "plain", Source: CatalogSourceConfig, Config: ServerConfig{URL: "https://example.org/mcp", Enabled: true}},
	})

	rebuilt := catalog.WithMaterializedServers(map[string]ServerConfig{
		"srv":   {URL: "https://other.example/mcp", Enabled: true},
		"plain": {URL: "https://example.org/mcp", Enabled: true},
	})
	server, ok := rebuilt.Servers["srv"]
	if !ok {
		t.Fatal("materialized catalog dropped srv")
	}
	if server.Config.Enabled {
		t.Fatal("transport change kept a provider-backed registration enabled")
	}
	if server.Config.RequestAuthProvider != nil || server.RequestAuthProvider != nil {
		t.Fatal("transport change kept the in-process provider attached")
	}
	plain, ok := rebuilt.Servers["plain"]
	if !ok || !plain.Config.Enabled {
		t.Fatalf("plain server after materialization = %#v, want enabled", plain)
	}
}
