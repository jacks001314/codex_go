package mcp

import "testing"

// Rust #60905: same-named MCP servers contributed by different environments
// have distinct catalog keys, while local servers keep the legacy bare name.
func TestResolveCatalogScopesSameNamedServersByEnvironmentLikeRust(t *testing.T) {
	local := ServerConfig{Command: "docs-local", Enabled: true}
	envA := ServerConfig{Command: "docs-a", Enabled: true, EnvironmentID: "env-a"}
	envB := ServerConfig{Command: "docs-b", Enabled: true, EnvironmentID: "env-b"}
	catalog := ResolveCatalog([]CatalogAction{
		{Name: "docs", Source: CatalogSourceConfig, Config: local, RegistrationOrder: 1},
		{Name: "docs", Source: CatalogSourceConfig, Config: envA, RegistrationOrder: 2},
		{Name: "docs", Source: CatalogSourceConfig, Config: envB, RegistrationOrder: 3},
	})
	if len(catalog.Servers) != 3 {
		t.Fatalf("resolved servers = %d, want local, env-a and env-b", len(catalog.Servers))
	}
	if got := catalog.Servers["docs"].Config.Command; got != "docs-local" {
		t.Fatalf("local command = %q", got)
	}
	keyA := ServerRegistrationKey("docs", "env-a")
	keyB := ServerRegistrationKey("docs", "env-b")
	if got := catalog.Servers[keyA].Config.Command; got != "docs-a" {
		t.Fatalf("env-a command = %q", got)
	}
	if got := catalog.Servers[keyB].Config.Command; got != "docs-b" {
		t.Fatalf("env-b command = %q", got)
	}
}

func TestResolveCatalogScopesConflictsLikeRust(t *testing.T) {
	catalog := ResolveCatalog([]CatalogAction{
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "a1", Enabled: true, EnvironmentID: "env-a"}, RegistrationOrder: 1},
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "a2", Enabled: true, EnvironmentID: "env-a"}, RegistrationOrder: 2},
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "b1", Enabled: true, EnvironmentID: "env-b"}, RegistrationOrder: 3},
	})
	if len(catalog.Servers) != 2 {
		t.Fatalf("resolved servers = %d, want one per environment", len(catalog.Servers))
	}
	if len(catalog.Conflicts) != 1 {
		t.Fatalf("conflicts = %d, want one env-a conflict", len(catalog.Conflicts))
	}
	if got := catalog.Servers[ServerRegistrationKey("docs", "env-a")].Config.Command; got != "a2" {
		t.Fatalf("env-a winner = %q", got)
	}
}

func TestMaterializedServersPreserveEnvironmentScopeLikeRust(t *testing.T) {
	catalog := ResolveCatalog([]CatalogAction{
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "docs-local", Enabled: true}, RegistrationOrder: 1},
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "docs-a", Enabled: true, EnvironmentID: "env-a"}, RegistrationOrder: 2},
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "docs-b", Enabled: true, EnvironmentID: "env-b"}, RegistrationOrder: 3},
	})
	keyA := ServerRegistrationKey("docs", "env-a")
	rebuilt := catalog.WithMaterializedServers(map[string]ServerConfig{
		"docs":                                 {Command: "docs-local", Enabled: true},
		keyA:                                   {Command: "docs-a2", Enabled: true, EnvironmentID: "env-a"},
		ServerRegistrationKey("docs", "env-b"): {Command: "docs-b", Enabled: true, EnvironmentID: "env-b"},
	})
	if len(rebuilt.Servers) != 3 {
		t.Fatalf("materialized servers = %d, want 3", len(rebuilt.Servers))
	}
	if got := rebuilt.Servers[keyA].Config.Command; got != "docs-a2" {
		t.Fatalf("materialized env-a command = %q", got)
	}
	if got := rebuilt.Servers["docs"].Config.Command; got != "docs-local" {
		t.Fatalf("materialized local command = %q", got)
	}
	if got := rebuilt.Servers[ServerRegistrationKey("docs", "env-b")].Config.Command; got != "docs-b" {
		t.Fatalf("materialized env-b command = %q", got)
	}
}
