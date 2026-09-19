package mcp

import (
	"testing"

	managedconfig "codex_go/config"
)

func environmentAuthorityService(authority *EnvironmentAuthority, registrations map[string]ServerRegistration) *MCPService {
	return NewMCPService(&RuntimeConfig{Servers: registrations, EnvironmentAuthority: authority})
}

func restrictedAuthority(policy *managedconfig.EnvironmentMCPPolicy, environments ...string) *EnvironmentAuthority {
	restricted := map[string]*managedconfig.EnvironmentMCPPolicy{}
	for _, environmentID := range environments {
		restricted[environmentID] = policy
	}
	return &EnvironmentAuthority{
		Scoped:      true,
		Unlimited:   map[string]bool{},
		Restricted:  restricted,
		Unavailable: map[string]bool{},
	}
}

func urlRequirement(url string) managedconfig.MCPServerRequirement {
	return managedconfig.MCPServerRequirement{Identity: &managedconfig.MCPServerIdentity{URL: &url}}
}

// Mirrors Rust catalog_tests.rs::environment_policy_exempts_only_explicitly_host_owned_apps:
// an environment policy that denies every configured server still leaves the
// controller-owned (compatibility) Codex Apps registration enabled, while an
// extension-provided Apps registration is filtered like any other server.
func TestEnvironmentPolicyExemptsOnlyHostOwnedAppsLikeRust(t *testing.T) {
	denyAll := &managedconfig.EnvironmentMCPPolicy{MCPServers: map[string]managedconfig.MCPServerRequirement{}}
	for _, testCase := range []struct {
		name         string
		registration ServerRegistration
		wantEnabled  bool
	}{
		{
			name: "extension apps",
			registration: ServerRegistration{
				Name:   CodexAppsServerName,
				Source: string(CatalogSourceExtension),
				Config: ServerConfig{Enabled: true, URL: "https://apps.example/mcp", EnvironmentID: DefaultMCPServerEnvironmentID},
			},
			wantEnabled: false,
		},
		{
			name: "hosted apps",
			registration: ServerRegistration{
				Name:          CodexAppsServerName,
				Source:        string(CatalogSourceCompatibility),
				ContributorID: legacyCodexAppsRegistrationID,
				Config:        ServerConfig{Enabled: true, URL: "https://apps.example/mcp", EnvironmentID: DefaultMCPServerEnvironmentID},
			},
			wantEnabled: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := environmentAuthorityService(
				restrictedAuthority(denyAll, DefaultMCPServerEnvironmentID),
				map[string]ServerRegistration{CodexAppsServerName: testCase.registration},
			)
			_, ok := service.ServerConfigForServer(CodexAppsServerName)
			if ok != testCase.wantEnabled {
				t.Fatalf("server enabled = %v, want %v", ok, testCase.wantEnabled)
			}
		})
	}
}

// Mirrors Rust catalog_tests.rs::environment_policy_preserves_selected_plugin_and_empty_server_allowlist_semantics.
func TestEnvironmentPolicyPreservesSelectedPluginAndEmptyAllowlistLikeRust(t *testing.T) {
	selected := ServerRegistration{
		Name:           "selected",
		Source:         string(CatalogSourceSelectedPlugin),
		PluginID:       "selected-plugin",
		SelectionOrder: 1,
		Config:         ServerConfig{Enabled: true, URL: "https://plugin.example/mcp", EnvironmentID: "env-a"},
	}
	metadataOnly := &managedconfig.EnvironmentMCPPolicy{Plugins: map[string]managedconfig.PluginRequirements{
		"metadata-only-plugin": {},
	}}
	denyAll := &managedconfig.EnvironmentMCPPolicy{MCPServers: map[string]managedconfig.MCPServerRequirement{}}
	for _, testCase := range []struct {
		name        string
		policy      *managedconfig.EnvironmentMCPPolicy
		wantEnabled bool
	}{
		{"metadata-only plugins keep the plugin", metadataOnly, true},
		{"an empty configured allowlist denies the plugin", denyAll, false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			service := environmentAuthorityService(
				restrictedAuthority(testCase.policy, "env-a"),
				map[string]ServerRegistration{"selected": selected},
			)
			if _, ok := service.ServerConfigForServer("selected"); ok != testCase.wantEnabled {
				t.Fatalf("server enabled = %v, want %v", ok, testCase.wantEnabled)
			}
		})
	}
}

// Covers the configured/compatibility/extension branch of Rust's Restricted
// filter: no configured allowlist is unrestricted, a non-empty allowlist admits
// only matching identities, and an explicitly empty allowlist denies every
// configured server.
func TestEnvironmentPolicyFiltersConfiguredServersLikeRust(t *testing.T) {
	registrations := map[string]ServerRegistration{
		"allowed": {
			Name:   "allowed",
			Source: string(CatalogSourceConfig),
			Config: ServerConfig{Enabled: true, URL: "https://allowed.example/mcp", EnvironmentID: "env-a"},
		},
		"other": {
			Name:   "other",
			Source: string(CatalogSourceConfig),
			Config: ServerConfig{Enabled: true, URL: "https://other.example/mcp", EnvironmentID: "env-a"},
		},
		"extension": {
			Name:   "extension",
			Source: string(CatalogSourceExtension),
			Config: ServerConfig{Enabled: true, URL: "https://allowed.example/mcp", EnvironmentID: "env-a"},
		},
	}
	service := environmentAuthorityService(&EnvironmentAuthority{
		Scoped:    true,
		Unlimited: map[string]bool{},
		Restricted: map[string]*managedconfig.EnvironmentMCPPolicy{"env-a": {
			MCPServers: map[string]managedconfig.MCPServerRequirement{"allowed": urlRequirement("https://allowed.example/mcp")},
		}},
		Unavailable: map[string]bool{},
	}, registrations)
	if _, ok := service.ServerConfigForServer("allowed"); !ok {
		t.Fatal("the allowlisted configured server must stay available")
	}
	if _, ok := service.ServerConfigForServer("other"); ok {
		t.Fatal("a configured server absent from the allowlist must be disabled")
	}
	// Extension sources follow the configured-server branch in Rust.
	if _, ok := service.ServerConfigForServer("extension"); ok {
		t.Fatal("an extension server absent from the allowlist must be disabled")
	}

	unrestricted := environmentAuthorityService(restrictedAuthority(&managedconfig.EnvironmentMCPPolicy{}, "env-a"), registrations)
	for _, name := range []string{"allowed", "other", "extension"} {
		if _, ok := unrestricted.ServerConfigForServer(name); !ok {
			t.Fatalf("a policy without a configured allowlist must keep %s available", name)
		}
	}
}

// Covers the environment authority outcomes: unavailable selections deny every
// attachment server, an unselected non-default environment keeps only explicitly
// selected plugins, and a nil authority keeps the legacy membership behavior.
func TestEnvironmentAuthorityOutcomesLikeRust(t *testing.T) {
	registrations := map[string]ServerRegistration{
		"configured": {
			Name:   "configured",
			Source: string(CatalogSourceConfig),
			Config: ServerConfig{Enabled: true, URL: "https://configured.example/mcp", EnvironmentID: "env-a"},
		},
		"plugin": {
			Name:     "plugin",
			Source:   string(CatalogSourcePlugin),
			PluginID: "demo",
			Config:   ServerConfig{Enabled: true, URL: "https://plugin.example/mcp", EnvironmentID: "env-a"},
		},
		"selected-plugin": {
			Name:           "selected-plugin",
			Source:         string(CatalogSourceSelectedPlugin),
			PluginID:       "demo",
			SelectionOrder: 1,
			Config:         ServerConfig{Enabled: true, URL: "https://plugin.example/mcp", EnvironmentID: "env-a"},
		},
		"local": {
			Name:   "local",
			Source: string(CatalogSourceConfig),
			Config: ServerConfig{Enabled: true, URL: "https://local.example/mcp"},
		},
	}

	unavailable := &EnvironmentAuthority{
		Scoped:      true,
		Unlimited:   map[string]bool{},
		Restricted:  map[string]*managedconfig.EnvironmentMCPPolicy{},
		Unavailable: map[string]bool{"env-a": true},
	}
	service := environmentAuthorityService(unavailable, registrations)
	for _, name := range []string{"configured", "plugin", "selected-plugin"} {
		if _, ok := service.ServerConfigForServer(name); ok {
			t.Fatalf("a pending or failed owner selection must disable %s", name)
		}
	}
	if _, ok := service.ServerConfigForServer("local"); !ok {
		t.Fatal("the default environment must stay unrestricted")
	}

	// env-a is not selected at all: only explicitly selected plugins survive.
	unselected := &EnvironmentAuthority{
		Scoped:      true,
		Unlimited:   map[string]bool{"env-b": true},
		Restricted:  map[string]*managedconfig.EnvironmentMCPPolicy{},
		Unavailable: map[string]bool{},
	}
	service = environmentAuthorityService(unselected, registrations)
	if _, ok := service.ServerConfigForServer("configured"); ok {
		t.Fatal("an unselected environment's configured server must be disabled")
	}
	if _, ok := service.ServerConfigForServer("plugin"); ok {
		t.Fatal("an unselected environment's unselected plugin server must be disabled")
	}
	if _, ok := service.ServerConfigForServer("selected-plugin"); !ok {
		t.Fatal("an explicitly selected plugin must survive an unselected environment")
	}

	// A legacy runtime without a captured authority keeps the membership list.
	legacy := NewMCPService(&RuntimeConfig{Servers: registrations, AvailableEnvironment: []string{"env-b"}})
	if _, ok := legacy.ServerConfigForServer("configured"); ok {
		t.Fatal("the legacy membership list must still filter attachment servers")
	}
}
