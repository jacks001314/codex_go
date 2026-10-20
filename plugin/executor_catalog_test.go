package plugin

import (
	"testing"

	"codex_go/execserver"
)

func TestMergePluginCatalogsLikeRust(t *testing.T) {
	remote := PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: "remote-1"}
	local := PluginIdentity{Kind: PluginIdentityLocal, PluginID: "local@market"}
	cloud := &PluginCatalog{Entries: []PluginCatalogEntry{{
		ID:          remote,
		DisplayName: "Cloud",
		Version:     "1.0.0",
		Locations:   []PluginSourceLocation{{Kind: PluginSourceCloud, ResourceURI: "cloud://1"}},
	}}}
	executor := &PluginCatalog{
		Warnings: []string{"executor warning"},
		Entries: []PluginCatalogEntry{
			{ID: remote, Version: "1.0.0", Locations: []PluginSourceLocation{{Kind: PluginSourceExecutor, EnvironmentID: "env", PluginID: "remote@local", Root: "/p"}}},
			{ID: PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: "remote-2"}, Version: "2.0.0"},
			{ID: remote, Version: "9.0.0"},
			{ID: local, Version: "0.1.0", Locations: []PluginSourceLocation{{Kind: PluginSourceExecutor, EnvironmentID: "env", PluginID: "local@market", Root: "/l"}}},
		},
	}
	merged := MergePluginCatalogs(executor, cloud)
	if len(merged.Entries) != 3 {
		t.Fatalf("entries = %#v", merged.Entries)
	}
	if merged.Entries[0].ID != remote || len(merged.Entries[0].Locations) != 2 {
		t.Fatalf("merged remote = %#v", merged.Entries[0])
	}
	if !merged.Entries[1].Disabled || merged.Entries[1].ID.RemotePluginID != "remote-2" {
		t.Fatalf("unmatched remote = %#v", merged.Entries[1])
	}
	if merged.Entries[2].ID != local || merged.Entries[2].Disabled {
		t.Fatalf("local executor = %#v", merged.Entries[2])
	}
	if len(merged.Warnings) != 2 {
		t.Fatalf("warnings = %#v", merged.Warnings)
	}
}

func TestPluginCatalogEntryIsEnabledLikeRust(t *testing.T) {
	disabled := false
	executor := PluginCatalogEntry{
		Locations: []PluginSourceLocation{{Kind: PluginSourceExecutor, PluginID: "a@m"}},
	}
	if !executor.IsEnabled(map[string]PluginConfig{"a@m": {}}) {
		t.Fatal("omitted enablement should inherit true")
	}
	if executor.IsEnabled(map[string]PluginConfig{"a@m": {Enabled: &disabled}}) {
		t.Fatal("explicitly disabled executor plugin projected")
	}
	cloud := PluginCatalogEntry{Locations: []PluginSourceLocation{{Kind: PluginSourceCloud}}}
	if !cloud.IsEnabled(nil) {
		t.Fatal("cloud membership should supply enablement")
	}
}

func TestCatalogFromExecutorSnapshotsLikeRust(t *testing.T) {
	mcpConfig := `{"mcpServers":{"docs":{"command":"docs-mcp"}}}`
	appsConfig := `{"apps":[{"connectorId":"connector-1"},{"connector_id":"connector-2"}]}`
	catalog := CatalogFromExecutorSnapshots([]execserver.ExecutorCapabilityDiscoveryV2Snapshot{
		{EnvironmentID: "env", Response: &execserver.DiscoverV2CapabilitiesResponse{
			Plugins: []execserver.ExecutorPlugin{{
				ID:          "demo@market",
				DisplayName: "Demo",
				Version:     "1.0.0",
				Root:        "/plugins/demo",
				Files: execserver.DiscoveredPluginFiles{
					MCPConfig:  &execserver.CapabilityTextFile{Contents: mcpConfig},
					AppsConfig: &execserver.CapabilityTextFile{Contents: appsConfig},
				},
			}},
		}},
		{EnvironmentID: "failed", Err: "boom"},
	})
	if len(catalog.Entries) != 1 || len(catalog.Warnings) != 1 {
		t.Fatalf("catalog = %#v", catalog)
	}
	entry := catalog.Entries[0]
	if entry.ID.Kind != PluginIdentityLocal || entry.ID.PluginID != "demo@market" {
		t.Fatalf("identity = %#v", entry.ID)
	}
	if entry.MCPServers["docs"] == "" || len(entry.ConnectorIDs) != 2 {
		t.Fatalf("entry = %#v", entry)
	}
}

func TestCatalogMCPServerContributionsUseFirstExecutorInstallationLikeRust(t *testing.T) {
	entry := PluginCatalogEntry{
		ID:          PluginIdentity{Kind: PluginIdentityLocal, PluginID: "demo@market"},
		DisplayName: "Demo",
		MCPServers: map[string]string{
			"docs": `{"command":"docs-mcp"}`,
			"ema":  `{"auth":"ema_auth"}`,
		},
		Locations: []PluginSourceLocation{
			{Kind: PluginSourceExecutor, EnvironmentID: "env-1", PluginID: "demo@market", Root: "/first"},
			{Kind: PluginSourceExecutor, EnvironmentID: "env-2", PluginID: "demo@market", Root: "/second"},
		},
	}
	contributions := (&PluginCatalog{Entries: []PluginCatalogEntry{entry}}).MCPServerContributions(map[string]PluginConfig{"demo@market": {}})
	if len(contributions) != 1 || contributions[0].Name != "docs" || contributions[0].PluginRoot != "/first" {
		t.Fatalf("contributions = %#v", contributions)
	}
}

func TestCatalogCapabilitySummariesCarryLocalAndRemoteIdentityLikeRust(t *testing.T) {
	remote := "plugins~Plugin_demo"
	entry := PluginCatalogEntry{
		ID:          PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: remote},
		DisplayName: "Demo",
		MCPServers:  map[string]string{"docs": `{"command":"docs"}`},
		Locations:   []PluginSourceLocation{{Kind: PluginSourceExecutor, PluginID: "demo@market", Root: "/p"}},
	}
	catalog := &PluginCatalog{Entries: []PluginCatalogEntry{entry}}
	summaries := catalog.CapabilitySummaries(map[string]PluginConfig{"demo@market": {}})
	if len(summaries) != 1 || summaries[0].ConfigName != "demo@market" || summaries[0].RemotePluginID != remote || len(summaries[0].MCPServers) != 1 {
		t.Fatalf("summaries = %#v", summaries)
	}
	base := []CapabilitySummary{{RemotePluginID: remote, ConfigName: remote, DisplayName: "Cloud Demo"}}
	merged := MergeCapabilitySummaries(base, summaries)
	if len(merged) != 1 || merged[0].DisplayName != "Cloud Demo" {
		t.Fatalf("merged summaries = %#v", merged)
	}
}
