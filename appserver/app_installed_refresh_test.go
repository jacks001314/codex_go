package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"codex_go/apps"
	"codex_go/mcp"
	"codex_go/session"
)

func codexAppsCatalogTool(name string, connectorID string) map[string]any {
	return map[string]any{
		"name":        name,
		"description": name,
		"inputSchema": map[string]any{"type": "object"},
		"_meta": map[string]any{
			"codex_apps": map[string]any{
				"connector_id":   connectorID,
				"connector_name": connectorID,
				"is_enabled":     true,
			},
		},
	}
}

// TestAppInstalledForceRefreshReplacesThreadToolsLikeRust mirrors Rust #43039:
// app/installed with a thread and forceRefresh must refresh the live codex_apps
// catalog that the same thread's subsequent turns consume, rather than a
// separate runtime snapshot. Go lists MCP tools live from the shared service
// for every turn (mcpRuntimeInputsForServiceWithRequirements) and
// app/installed drives that service's Refresh(), so the refreshed catalog
// replaces the previous tools.
func TestAppInstalledForceRefreshReplacesThreadToolsLikeRust(t *testing.T) {
	var mu sync.Mutex
	currentTools := []map[string]any{codexAppsCatalogTool("search_v1", "drive")}
	failing := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode MCP request error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set("Mcp-Session-Id", "session-1")
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "codex_apps", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			mu.Lock()
			tools := append([]map[string]any(nil), currentTools...)
			fail := failing
			mu.Unlock()
			if fail {
				http.Error(w, "catalog unavailable", http.StatusInternalServerError)
				return
			}
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"tools": tools})
		case "resources/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
		case "resources/templates/list":
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		default:
			writeRuntimeRouterMCPResponse(t, w, request.ID, map[string]any{})
		}
	}))
	defer server.Close()

	mcpService := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		mcp.RuntimeCodexAppsMCPServerName: {Config: mcp.ServerConfig{URL: server.URL, Enabled: true}},
	}})
	const threadID = "app-installed-refresh-thread"
	now := time.Date(2026, 9, 12, 1, 0, 0, 0, time.UTC)
	store := session.NewStore(t.TempDir())
	threadRouter := NewRouter(store)
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: "app-installed-refresh-session",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			ModelProvider: "openai",
			HistoryMode:   "paginated",
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: threadRouter, MCP: mcpService})

	installed := func(id int64, forceRefresh bool) *apps.AppsInstalledResponse {
		t.Helper()
		response := router.Handle(requestWithParams(t, IntID(id), MethodAppInstalled, apps.AppsInstalledParams{
			ThreadID:     stringPtrIfNotEmpty(threadID),
			ForceRefresh: forceRefresh,
		}))
		if response.Error != nil {
			t.Fatalf("app/installed error = %#v", response.Error)
		}
		return response.Result.(*apps.AppsInstalledResponse)
	}

	first := installed(1, false)
	if !hasInstalledAppID(first.Apps, "drive") || len(first.Apps) != 1 || !first.Apps[0].Callable {
		t.Fatalf("initial installed apps = %#v", first.Apps)
	}
	if tools := runtimeToolNames(t, router.runtimeMCPToolsForTest(threadID, mcpService)); !containsString(tools, "search_v1") {
		t.Fatalf("initial thread tools = %#v, want search_v1", tools)
	}

	mu.Lock()
	currentTools = []map[string]any{codexAppsCatalogTool("search_v2", "calendar")}
	mu.Unlock()

	generationBefore := mcpService.Generation()
	refreshed := installed(2, true)
	if mcpService.Generation() == generationBefore {
		t.Fatal("app/installed force refresh did not bump the MCP service generation")
	}
	if !hasInstalledAppID(refreshed.Apps, "calendar") || len(refreshed.Apps) != 1 {
		t.Fatalf("refreshed installed apps = %#v", refreshed.Apps)
	}
	tools := runtimeToolNames(t, router.runtimeMCPToolsForTest(threadID, mcpService))
	if !containsString(tools, "search_v2") || containsString(tools, "search_v1") {
		t.Fatalf("refreshed thread tools = %#v, want only search_v2", tools)
	}

	mu.Lock()
	failing = true
	mu.Unlock()
	response := router.Handle(requestWithParams(t, IntID(3), MethodAppInstalled, apps.AppsInstalledParams{
		ThreadID:     stringPtrIfNotEmpty(threadID),
		ForceRefresh: true,
	}))
	if response.Error != nil {
		t.Fatalf("failed refresh app/installed error = %#v", response.Error)
	}
	preserved := response.Result.(*apps.AppsInstalledResponse)
	if len(preserved.Apps) != 1 || preserved.Apps[0].ID != "calendar" || !preserved.Apps[0].Callable {
		t.Fatalf("failed refresh dropped the last working tools: %#v", preserved.Apps)
	}
}

func (r *RuntimeRouter) runtimeMCPToolsForTest(threadID string, service *mcp.MCPService) []mcp.RuntimeToolInfo {
	tools, _ := r.mcpRuntimeInputsForServiceWithRequirements(threadID, nil, service, nil, nil)
	return tools
}

func runtimeToolNames(t *testing.T, tools []mcp.RuntimeToolInfo) []string {
	t.Helper()
	names := make([]string, 0, len(tools))
	for i := range tools {
		names = append(names, tools[i].Tool.Name)
	}
	return names
}

func hasInstalledAppID(entries []apps.InstalledApp, id string) bool {
	for i := range entries {
		if entries[i].ID == id {
			return true
		}
	}
	return false
}
