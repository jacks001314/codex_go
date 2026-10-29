package appserver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/config"
	"codex_go/mcp"
	"codex_go/plugin"
	"codex_go/state"
)

func catalogMetricRecords(t *testing.T, router *RuntimeRouter, name string) []string {
	t.Helper()
	statuses := []string{}
	for _, record := range router.metrics.Records() {
		if record.Name != name || record.Kind != "histogram" {
			continue
		}
		statuses = append(statuses, record.Tags["stage"]+"/"+record.Tags["status"])
	}
	return statuses
}

func TestCloudPluginCatalogEntriesMetricLikeRust(t *testing.T) {
	provider := &staticPluginCatalogProvider{catalog: &plugin.PluginCatalog{
		Entries:  []plugin.PluginCatalogEntry{{ID: plugin.PluginIdentity{Kind: plugin.PluginIdentityLocal, PluginID: "alpha@test"}}},
		Warnings: []string{"one root was skipped"},
	}}
	router := newCloudCatalogTestRouter(t, provider)
	router.metrics = state.NewTaskMetrics()
	cfg := &config.Config{}

	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-metrics", "turn-1")
	if got := catalogMetricRecords(t, router, pluginCatalogEntriesMetric); len(got) != 1 || got[0] != "cloud/warning" {
		t.Fatalf("cloud plugin metric = %#v, want one cloud/warning sample", got)
	}
	provider.catalog = &plugin.PluginCatalog{IsPartial: true, Warnings: []string{"cloud listing was truncated"}}
	router.refreshCloudPluginCatalogForStep(context.Background(), cfg, "thread-metrics", "turn-2")
	if got := catalogMetricRecords(t, router, pluginCatalogEntriesMetric); len(got) != 2 || got[1] != "cloud/partial" {
		t.Fatalf("cloud plugin metric = %#v, want partial to outrank the warning", got)
	}
}

func TestExecutorPluginCatalogEntriesMetricSkipsUnchangedDiscoveryLikeRust(t *testing.T) {
	router := newCloudCatalogTestRouter(t, nil)
	router.metrics = state.NewTaskMetrics()
	catalog := &plugin.PluginCatalog{Entries: []plugin.PluginCatalogEntry{{
		ID:        plugin.PluginIdentity{Kind: plugin.PluginIdentityLocal, PluginID: "alpha@test"},
		Locations: []plugin.PluginSourceLocation{{Kind: plugin.PluginSourceExecutor, EnvironmentID: "executor-a", Root: "/plugins"}},
	}}}

	router.recordExecutorPluginCatalog("thread-metrics", catalog)
	router.recordExecutorPluginCatalog("thread-metrics", catalog)
	if got := catalogMetricRecords(t, router, pluginCatalogEntriesMetric); len(got) != 1 || got[0] != "executor/complete" {
		t.Fatalf("executor plugin metric = %#v, want one executor/complete sample", got)
	}
	catalog.Entries = append(catalog.Entries, plugin.PluginCatalogEntry{
		ID:        plugin.PluginIdentity{Kind: plugin.PluginIdentityLocal, PluginID: "beta@test"},
		Locations: []plugin.PluginSourceLocation{{Kind: plugin.PluginSourceExecutor, EnvironmentID: "executor-a", Root: "/plugins"}},
	})
	router.recordExecutorPluginCatalog("thread-metrics", catalog)
	if got := catalogMetricRecords(t, router, pluginCatalogEntriesMetric); len(got) != 2 {
		t.Fatalf("executor plugin metric = %#v, want a new sample for a changed discovery", got)
	}
}

func TestExecutorSkillCatalogEntriesMetricMarksPartialLikeRust(t *testing.T) {
	router := newCloudCatalogTestRouter(t, nil)
	router.metrics = state.NewTaskMetrics()
	entries := []SkillsListEntry{{Name: "alpha", Scope: "environment", Path: "/plugins/alpha/SKILL.md"}}
	warnings := []string{"executor skills unavailable: discovery failed"}

	router.recordExecutorSkillCatalog("thread-metrics", entries, warnings, true)
	router.recordExecutorSkillCatalog("thread-metrics", entries, warnings, true)
	if got := catalogMetricRecords(t, router, skillCatalogEntriesMetric); len(got) != 1 || got[0] != "executor/partial" {
		t.Fatalf("executor skill metric = %#v, want one executor/partial sample", got)
	}
	router.recordExecutorSkillCatalog("thread-metrics", entries, nil, false)
	if got := catalogMetricRecords(t, router, skillCatalogEntriesMetric); len(got) != 2 || got[1] != "executor/complete" {
		t.Fatalf("executor skill metric = %#v, want a complete sample after recovery", got)
	}
}

func TestCloudSkillCatalogEntriesMetricLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode MCP request error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"resources": map[string]any{}},
				"serverInfo":      map[string]string{"name": "codex_apps", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		case "resources/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"resources": []map[string]any{{
					"uri":         "skill://drive/docs",
					"name":        "docs",
					"description": "Search shared docs.",
					"mimeType":    "mcp/skill",
					"_meta":       map[string]any{"plugin_name": "Drive", "skill_name": "doc-search"},
				}},
			})
		case "resources/templates/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		default:
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{})
		}
	}))
	defer server.Close()
	router := NewRuntimeRouter(RuntimeServices{
		MCP: mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
			mcp.RuntimeCodexAppsMCPServerName: {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
		}}),
	})
	router.metrics = state.NewTaskMetrics()
	metadata, warnings := router.orchestratorSkillMetadataForRuntime("thread-cloud-skill-metrics", "turn-1")
	if len(metadata) != 1 || len(warnings) != 0 {
		t.Fatalf("cloud skill catalog = %#v warnings=%#v", metadata, warnings)
	}
	if got := catalogMetricRecords(t, router, skillCatalogEntriesMetric); len(got) != 1 || got[0] != "cloud/complete" {
		t.Fatalf("cloud skill metric = %#v, want one cloud/complete sample", got)
	}
	if records := router.metrics.Records(); len(records) == 0 {
		t.Fatal("no metric records captured")
	}
}

type staticPluginCatalogProvider struct {
	catalog *plugin.PluginCatalog
	calls   int
}

func (p *staticPluginCatalogProvider) ListPlugins(context.Context, plugin.PluginListQuery) (*plugin.PluginCatalog, error) {
	p.calls++
	return p.catalog, nil
}
