package mcp

import (
	"reflect"
	"testing"
)

func TestCatalogSourcePriorityMatchesRustTiers(t *testing.T) {
	// Rust tiers: Extension(4) > Compatibility(3) > Config(2) > SelectedPlugin(1) > Plugin(0)
	tests := []struct {
		source   CatalogSource
		expected int
	}{
		{CatalogSourcePlugin, 0},
		{CatalogSourceSelectedPlugin, 1},
		{CatalogSourceConfig, 2},
		{CatalogSourceCompatibility, 3},
		{CatalogSourceExtension, 4},
	}
	for _, test := range tests {
		if got := CatalogSourcePriority(test.source); got != test.expected {
			t.Fatalf("CatalogSourcePriority(%q) = %d, want %d", test.source, got, test.expected)
		}
	}
}

func TestResolveCatalogExtensionWins(t *testing.T) {
	// Extension should win over Config for the same server name.
	actions := []CatalogAction{
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "config-docs", Enabled: true}, RegistrationOrder: 1},
		{Name: "docs", Source: CatalogSourceExtension, Config: ServerConfig{Command: "ext-docs", Enabled: true}, RegistrationOrder: 2},
	}
	catalog := ResolveCatalog(actions)
	if len(catalog.Servers) != 1 {
		t.Fatalf("expected 1 server, got %d", len(catalog.Servers))
	}
	if catalog.Servers["docs"].Source != CatalogSourceExtension {
		t.Fatalf("Extension should win over Config: got %q", catalog.Servers["docs"].Source)
	}
	if catalog.Servers["docs"].Config.Command != "ext-docs" {
		t.Fatalf("Extension config should be used: got %q", catalog.Servers["docs"].Config.Command)
	}
}

func TestResolveCatalogConfigOverPlugin(t *testing.T) {
	// Config should win over Plugin.
	actions := []CatalogAction{
		{Name: "shell", Source: CatalogSourcePlugin, Config: ServerConfig{Command: "plugin-shell", Enabled: true}, RegistrationOrder: 1},
		{Name: "shell", Source: CatalogSourceConfig, Config: ServerConfig{Command: "cfg-shell", Enabled: true}, RegistrationOrder: 2},
	}
	catalog := ResolveCatalog(actions)
	if catalog.Servers["shell"].Source != CatalogSourceConfig {
		t.Fatalf("Config should win over Plugin: got %q", catalog.Servers["shell"].Source)
	}
}

func TestResolveCatalogRemoveAction(t *testing.T) {
	actions := []CatalogAction{
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "docs", Enabled: true}, RegistrationOrder: 1},
		{Name: "docs", Source: CatalogSourceExtension, Config: ServerConfig{}, Remove: true, RegistrationOrder: 2},
	}
	catalog := ResolveCatalog(actions)
	if _, ok := catalog.Servers["docs"]; ok {
		t.Fatal("docs should be removed by Extension remove action")
	}
}

func TestResolveCatalogDisabledServerAppearsInCatalog(t *testing.T) {
	// A disabled Config server should still appear in the catalog with Enabled=false.
	actions := []CatalogAction{
		{Name: "disabled-srv", Source: CatalogSourceConfig, Config: ServerConfig{Command: "disabled-srv", Enabled: false}, RegistrationOrder: 1},
	}
	catalog := ResolveCatalog(actions)
	srv, ok := catalog.Servers["disabled-srv"]
	if !ok {
		t.Fatal("disabled server should appear in catalog with Enabled=false")
	}
	if srv.Config.Enabled {
		t.Fatal("disabled server should have Enabled=false")
	}
	if !catalog.DisabledServerNames["disabled-srv"] {
		t.Fatal("disabled server should be in veto set")
	}
}

func TestResolveCatalogDisabledServerVeto(t *testing.T) {
	// In a single build pass, the highest-priority winner determines the result.
	// Even if Config disables a server, a higher-priority Extension can re-enable it.
	// The veto only works across separate build passes (Rust builder pattern).
	actions := []CatalogAction{
		{Name: "vetoed", Source: CatalogSourceConfig, Config: ServerConfig{Command: "vetoed", Enabled: false}, RegistrationOrder: 1},
		{Name: "vetoed", Source: CatalogSourceExtension, Config: ServerConfig{Command: "revived", Enabled: true}, RegistrationOrder: 2},
	}
	catalog := ResolveCatalog(actions)
	srv, ok := catalog.Servers["vetoed"]
	if !ok {
		t.Fatal("server should appear in catalog (winner determines result)")
	}
	if srv.Config.Command != "revived" {
		t.Fatalf("Extension config should be used: got %q", srv.Config.Command)
	}
	if !srv.Config.Enabled {
		t.Fatal("Extension's enabled=true should win in single build")
	}
}

func TestResolveCatalogDisabledVetoAcrossBuilds(t *testing.T) {
	// First build: Config disables "docs". This creates a name-level veto.
	first := ResolveCatalog([]CatalogAction{
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "disabled-docs", Enabled: false}, RegistrationOrder: 1},
	})
	srv, ok := first.Servers["docs"]
	if !ok {
		t.Fatal("disabled Config server should still appear in catalog (with Enabled=false)")
	}
	if srv.Config.Enabled {
		t.Fatal("disabled Config should have Enabled=false")
	}
	if !first.DisabledServerNames["docs"] {
		t.Fatal("disabled Config should create veto for docs")
	}

	// Second build: Extension tries to re-enable "docs" but the veto persists.
	second := ResolveCatalogWithDisabled(first.DisabledServerNames, []CatalogAction{
		{Name: "docs", Source: CatalogSourceExtension, Config: ServerConfig{Command: "ext-docs", Enabled: true}, RegistrationOrder: 1},
	})
	srv2, ok2 := second.Servers["docs"]
	if !ok2 {
		t.Fatal("vetoed server should still appear in catalog")
	}
	if srv2.Config.Enabled {
		t.Fatal("veto from first build should force enabled=false in second build")
	}
	if srv2.Config.Command != "ext-docs" {
		t.Fatal("Extension config should be used but with enabled forced to false")
	}
}

func TestResolveCatalogSelectedPluginNoVeto(t *testing.T) {
	// When SelectedPlugin disables, later Config can re-enable it.
	actions := []CatalogAction{
		{Name: "tool", Source: CatalogSourceSelectedPlugin, Config: ServerConfig{Command: "plugin-tool", Enabled: false}, RegistrationOrder: 1},
		{Name: "tool", Source: CatalogSourceConfig, Config: ServerConfig{Command: "cfg-tool", Enabled: true}, RegistrationOrder: 2},
	}
	catalog := ResolveCatalog(actions)
	if catalog.Servers["tool"].Source != CatalogSourceConfig {
		t.Fatalf("SelectedPlugin disable should not veto Config re-enable: got %q", catalog.Servers["tool"].Source)
	}
}

func TestResolveCatalogConflicts(t *testing.T) {
	// Two Config registrations for the same name = same-tier conflict.
	actions := []CatalogAction{
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "docs-a", Enabled: true}, RegistrationOrder: 1},
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "docs-b", Enabled: true}, RegistrationOrder: 2},
	}
	catalog := ResolveCatalog(actions)
	if len(catalog.Conflicts) != 1 {
		t.Fatalf("expected 1 conflict, got %d", len(catalog.Conflicts))
	}
	if catalog.Conflicts[0].Name != "docs" {
		t.Fatalf("conflict name = %q", catalog.Conflicts[0].Name)
	}
	if len(catalog.Conflicts[0].Contender) != 2 {
		t.Fatalf("conflict contenders = %d", len(catalog.Conflicts[0].Contender))
	}
	// Last Config wins within same tier.
	if catalog.Conflicts[0].Outcome.Config.Command != "docs-b" {
		t.Fatalf("outcome = %q, want docs-b", catalog.Conflicts[0].Outcome.Config.Command)
	}
}

func TestResolveCatalogMultiServerDiverseSources(t *testing.T) {
	// From Rust catalog_tests.rs: five sources for one name, Extension wins.
	actions := []CatalogAction{
		{Name: "docs", Source: CatalogSourcePlugin, Config: ServerConfig{Command: "plugin", Enabled: true}, RegistrationOrder: 1},
		{Name: "docs", Source: CatalogSourceSelectedPlugin, Config: ServerConfig{Command: "sel-plugin", Enabled: true}, RegistrationOrder: 2},
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "config", Enabled: true}, RegistrationOrder: 3},
		{Name: "docs", Source: CatalogSourceCompatibility, Config: ServerConfig{Command: "compat", Enabled: true}, RegistrationOrder: 4},
		{Name: "docs", Source: CatalogSourceExtension, Config: ServerConfig{Command: "ext", Enabled: true}, RegistrationOrder: 5},
	}
	catalog := ResolveCatalog(actions)
	if catalog.Servers["docs"].Source != CatalogSourceExtension {
		t.Fatalf("Extension should win all: got %q", catalog.Servers["docs"].Source)
	}
	if catalog.Servers["docs"].Config.Command != "ext" {
		t.Fatalf("Extension config should win: got %q", catalog.Servers["docs"].Config.Command)
	}
}

func TestResolveCatalogWithinTierLastWins(t *testing.T) {
	// Within the same tier, the last registration (highest RegistrationOrder) wins.
	actions := []CatalogAction{
		{Name: "shell", Source: CatalogSourceExtension, Config: ServerConfig{Command: "ext-early", Enabled: true}, RegistrationOrder: 1},
		{Name: "shell", Source: CatalogSourceExtension, Config: ServerConfig{Command: "ext-late", Enabled: true}, RegistrationOrder: 2},
	}
	catalog := ResolveCatalog(actions)
	if catalog.Servers["shell"].Config.Command != "ext-late" {
		t.Fatalf("last Extension should win: got %q", catalog.Servers["shell"].Config.Command)
	}
}

func TestResolveCatalogWithInitialDisabled(t *testing.T) {
	initialDisabled := map[string]bool{"blocked": true}
	actions := []CatalogAction{
		{Name: "blocked", Source: CatalogSourceConfig, Config: ServerConfig{Command: "blocked", Enabled: true}, RegistrationOrder: 1},
		{Name: "allowed", Source: CatalogSourceConfig, Config: ServerConfig{Command: "allowed", Enabled: true}, RegistrationOrder: 2},
	}
	catalog := ResolveCatalogWithDisabled(initialDisabled, actions)
	// Blocked server should still appear but with Enabled=false (matching Rust behavior)
	srv, ok := catalog.Servers["blocked"]
	if !ok {
		t.Fatal("blocked server should appear in catalog (with Enabled forced to false)")
	}
	if srv.Config.Enabled {
		t.Fatal("blocked server should have Enabled=false due to initialDisabled veto")
	}
	if _, ok := catalog.Servers["allowed"]; !ok {
		t.Fatal("allowed server should be in resolved catalog")
	}
}

func TestSourceFromRegistration(t *testing.T) {
	tests := []struct {
		registration ServerRegistration
		expected     CatalogSource
	}{
		{ServerRegistration{Source: "config"}, CatalogSourceConfig},
		{ServerRegistration{Source: "plugin"}, CatalogSourcePlugin},
		{ServerRegistration{Source: "selected_plugin"}, CatalogSourceSelectedPlugin},
		{ServerRegistration{Source: "compatibility"}, CatalogSourceCompatibility},
		{ServerRegistration{Source: "extension"}, CatalogSourceExtension},
		{ServerRegistration{Source: "", PluginID: "my-plugin"}, CatalogSourcePlugin},
		{ServerRegistration{Source: "", ContributorID: legacyCodexAppsRegistrationID}, CatalogSourceCompatibility},
		{ServerRegistration{Source: "plugin", SelectionOrder: 1}, CatalogSourceSelectedPlugin},
	}
	for _, test := range tests {
		if got := SourceFromRegistration(&test.registration); got != test.expected {
			t.Fatalf("SourceFromRegistration(%#v) = %q, want %q", test.registration, got, test.expected)
		}
	}
}

func TestResolveCatalogEmpty(t *testing.T) {
	catalog := ResolveCatalog(nil)
	if len(catalog.Servers) != 0 || len(catalog.Conflicts) != 0 {
		t.Fatal("empty actions should produce empty catalog")
	}
}

func TestResolvedCatalogMatchesExpectedShape(t *testing.T) {
	actions := []CatalogAction{
		{Name: "docs", Source: CatalogSourceConfig, Config: ServerConfig{Command: "docs", Enabled: true}, RegistrationOrder: 1},
	}
	catalog := ResolveCatalog(actions)
	if !reflect.DeepEqual(catalog.Servers["docs"], ResolvedServer{
		Source: CatalogSourceConfig,
		Config: ServerConfig{Command: "docs", Enabled: true},
	}) {
		t.Fatalf("resolved server = %#v", catalog.Servers["docs"])
	}
}
