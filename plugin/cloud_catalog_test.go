package plugin

import "testing"

func TestCloudPluginCatalogProjectsAppsOnlyCapabilitiesLikeRust(t *testing.T) {
	service := NewPluginService()
	service.SetCloudPluginCatalog(&PluginCatalog{Entries: []PluginCatalogEntry{
		{
			ID:           PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: "plugins~Plugin_cloud"},
			DisplayName:  "Cloud Plugin",
			MCPServers:   map[string]string{"ignored": "http://example.test"},
			ConnectorIDs: []string{"calendar", "calendar", "drive"},
		},
		{
			ID:          PluginIdentity{Kind: PluginIdentityLocal, PluginID: "local@test"},
			DisplayName: "Local Plugin",
		},
	}}, "scope")
	capabilities := service.EnabledCapabilities()
	if len(capabilities) != 1 {
		t.Fatalf("capabilities = %#v", capabilities)
	}
	capability := capabilities[0]
	if capability.RemotePluginID != "plugins~Plugin_cloud" || capability.DisplayName != "Cloud Plugin" {
		t.Fatalf("cloud capability = %#v", capability)
	}
	if len(capability.MCPServers) != 0 || len(capability.AppConnectors) != 2 {
		t.Fatalf("cloud projection = %#v", capability)
	}
	if service.CloudPluginCatalogKey() != "scope" {
		t.Fatalf("cache key = %q", service.CloudPluginCatalogKey())
	}

	service.SetCloudPluginCatalog(&PluginCatalog{}, "scope")
	if len(service.EnabledCapabilities()) != 0 {
		t.Fatal("empty cloud catalog did not clear projected capabilities")
	}
}

func TestCloudPluginCatalogRetainsCompleteSnapshotOnPartialRefreshLikeRust(t *testing.T) {
	service := NewPluginService()
	service.SetCloudPluginCatalog(&PluginCatalog{Entries: []PluginCatalogEntry{{
		ID:           PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: "plugins~Plugin_complete"},
		DisplayName:  "Complete",
		ConnectorIDs: []string{"calendar"},
	}}}, "scope")
	service.SetCloudPluginCatalog(&PluginCatalog{IsPartial: true, Entries: []PluginCatalogEntry{{
		ID:          PluginIdentity{Kind: PluginIdentityRemote, RemotePluginID: "plugins~Plugin_partial"},
		DisplayName: "Partial",
	}}}, "scope")
	capabilities := service.EnabledCapabilities()
	if len(capabilities) != 1 || capabilities[0].RemotePluginID != "plugins~Plugin_complete" {
		t.Fatalf("partial refresh replaced the complete catalog: %#v", capabilities)
	}
}
