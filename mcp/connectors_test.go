package mcp

import (
	"testing"
	"time"
)

func TestAccessibleFromMCPToolsMergesConnectors(t *testing.T) {
	connectors := ConnectorAccessibleFromMCPTools([]ConnectorToolInfo{
		{ServerName: ConnectorCodexAppsMCPServerName, ConnectorID: "drive", ConnectorName: "Drive", PluginDisplayNames: []string{"Plugin B"}},
		{ServerName: "other", ConnectorID: "ignored"},
		{ServerName: CodexAppsServerName, ConnectorID: "drive", NamespaceDescription: "Files", PluginDisplayNames: []string{"Plugin A"}},
		{ServerName: ConnectorCodexAppsMCPServerName, ConnectorID: "synthetic", Meta: map[string]any{"synthetic_link": true}},
	})
	if len(connectors) != 1 {
		t.Fatalf("connectors = %+v, want one", connectors)
	}
	if connectors[0].ID != "drive" || connectors[0].Name != "Drive" {
		t.Fatalf("connector = %+v", connectors[0])
	}
	if len(connectors[0].PluginDisplayNames) != 2 || connectors[0].PluginDisplayNames[0] != "Plugin A" || connectors[0].PluginDisplayNames[1] != "Plugin B" {
		t.Fatalf("plugin names = %+v", connectors[0].PluginDisplayNames)
	}
}

func TestConnectorToolInfoFromMCPToolsParsesCodexAppsMeta(t *testing.T) {
	tools := ConnectorToolInfoFromMCPTools(CodexAppsServerName, []MCPToolInfo{
		{
			Name: "search",
			Meta: map[string]any{
				"_codex_apps": map[string]any{
					"connectorId":          "drive",
					"connectorName":        "Google Drive",
					"connectorDescription": "Drive files",
					"branding": map[string]any{
						"iconUrl": "https://example.com/drive.png",
						"color":   "#4285f4",
					},
					"metadata": map[string]any{
						"homepage_url": "https://drive.example.com",
						"docsUrl":      "https://docs.example.com/drive",
					},
					"isEnabled":          false,
					"pluginDisplayNames": []any{"Docs Plugin"},
				},
			},
		},
		{
			Name: "synthetic",
			Meta: map[string]any{
				"_codex_apps": map[string]any{
					"connector_id":   "synthetic",
					"synthetic_link": true,
				},
			},
		},
		{
			Name: "synthetic-camel",
			Meta: map[string]any{
				"codexApps": map[string]any{
					"connectorId":    "synthetic-camel",
					"syntheticLink":  "true",
					"connector_name": "Synthetic Camel",
				},
			},
		},
	})
	connectors := ConnectorAccessibleFromMCPTools(tools)
	if len(connectors) != 1 {
		t.Fatalf("connectors = %+v, want one", connectors)
	}
	if connectors[0].ID != "drive" || connectors[0].Name != "Google Drive" || connectors[0].Description != "Drive files" {
		t.Fatalf("connector = %+v", connectors[0])
	}
	if connectors[0].IsEnabled {
		t.Fatalf("connector IsEnabled = true, want false from metadata")
	}
	if len(connectors[0].PluginDisplayNames) != 1 || connectors[0].PluginDisplayNames[0] != "Docs Plugin" {
		t.Fatalf("plugin names = %+v", connectors[0].PluginDisplayNames)
	}
	if connectors[0].Branding == nil || connectors[0].Branding.IconURL == nil || *connectors[0].Branding.IconURL != "https://example.com/drive.png" || connectors[0].Branding.Color == nil || *connectors[0].Branding.Color != "#4285f4" {
		t.Fatalf("branding = %+v", connectors[0].Branding)
	}
	if connectors[0].Metadata == nil || connectors[0].Metadata.HomepageURL == nil || *connectors[0].Metadata.HomepageURL != "https://drive.example.com" || connectors[0].Metadata.DocsURL == nil || *connectors[0].Metadata.DocsURL != "https://docs.example.com/drive" {
		t.Fatalf("metadata = %+v", connectors[0].Metadata)
	}
}

func TestConnectorToolInfoFromMCPToolsReadsAnnotationsFallback(t *testing.T) {
	tools := ConnectorToolInfoFromMCPTools(CodexAppsServerName, []MCPToolInfo{
		{
			Name: "search",
			Annotations: map[string]any{
				"_codex_apps": map[string]any{
					"connector_id":         "drive",
					"connector_name":       "Drive From Annotations",
					"plugin_display_names": []any{"Annotations Plugin"},
					"branding": map[string]any{
						"icon_url": "https://example.com/annotation.png",
					},
				},
			},
			Meta: map[string]any{
				"_codex_apps": map[string]any{
					"connectorName":      "Drive From Meta",
					"pluginDisplayNames": []any{"Meta Plugin"},
				},
			},
		},
	})
	connectors := ConnectorAccessibleFromMCPTools(tools)
	if len(connectors) != 1 {
		t.Fatalf("connectors = %+v, want one", connectors)
	}
	if connectors[0].ID != "drive" || connectors[0].Name != "Drive From Meta" {
		t.Fatalf("connector = %+v", connectors[0])
	}
	if len(connectors[0].PluginDisplayNames) != 2 || connectors[0].PluginDisplayNames[0] != "Annotations Plugin" || connectors[0].PluginDisplayNames[1] != "Meta Plugin" {
		t.Fatalf("plugin names = %+v", connectors[0].PluginDisplayNames)
	}
	if connectors[0].Branding == nil || connectors[0].Branding.IconURL == nil || *connectors[0].Branding.IconURL != "https://example.com/annotation.png" {
		t.Fatalf("branding = %+v", connectors[0].Branding)
	}
}

func TestWithEnabledState(t *testing.T) {
	enabled := true
	disabled := false
	connectors := []ConnectorAppInfo{{ID: "a", IsEnabled: true}, {ID: "b", IsEnabled: true}}
	out := ConnectorsWithEnabledState(connectors, &ConnectorAppsConfig{
		DefaultEnabled: false,
		Apps: map[string]ConnectorAppConfig{
			"a": {Enabled: &enabled},
		},
	}, &ConnectorAppsConfig{
		DefaultEnabled: true,
		Apps: map[string]ConnectorAppConfig{
			"a": {Enabled: &disabled},
		},
	})
	if out[0].IsEnabled || out[1].IsEnabled {
		t.Fatalf("enabled state = %+v, want both disabled", out)
	}
	if !connectors[0].IsEnabled {
		t.Fatalf("input mutated")
	}
}

func TestWithPluginSources(t *testing.T) {
	provenance := NewConnectorPluginProvenance()
	provenance.Add("drive", "B")
	provenance.Add("drive", "A")
	out := ConnectorsWithPluginSources([]ConnectorAppInfo{{ID: "drive"}}, provenance)
	if len(out[0].PluginDisplayNames) != 2 || out[0].PluginDisplayNames[0] != "A" {
		t.Fatalf("plugin sources = %+v", out[0].PluginDisplayNames)
	}
}

func TestCacheReadWriteExpiryAndKey(t *testing.T) {
	now := time.Unix(100, 0)
	cache := NewConnectorCache(time.Minute)
	cache.SetClock(func() time.Time { return now })
	accountID := "account"
	key := ConnectorCacheKey{ChatGPTBaseURL: "https://chatgpt.com", AccountID: &accountID}
	cache.Write(key, []ConnectorAppInfo{{ID: "drive"}})
	got, ok := cache.Read(key)
	if !ok || len(got) != 1 || got[0].ID != "drive" {
		t.Fatalf("Read() = %+v, %v", got, ok)
	}
	got[0].ID = "mutated"
	got, ok = cache.Read(key)
	if !ok || got[0].ID != "drive" {
		t.Fatalf("cache leaked mutation: %+v", got)
	}
	other := ConnectorCacheKey{ChatGPTBaseURL: "https://chatgpt.com"}
	if _, ok := cache.Read(other); ok {
		t.Fatalf("Read(other key) ok = true, want false")
	}
	now = now.Add(2 * time.Minute)
	if _, ok := cache.Read(key); ok {
		t.Fatalf("Read(expired) ok = true, want false")
	}
}
