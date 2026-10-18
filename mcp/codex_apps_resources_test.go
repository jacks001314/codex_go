package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// TestCodexAppsResourceMimeFilterPersistsAcrossPagesLikeRust mirrors Rust
// #44548: every resources/list page carries the required top-level mimeType,
// and the returned page preserves its next cursor.
func TestCodexAppsResourceMimeFilterPersistsAcrossPagesLikeRust(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "apps", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/list":
			var params map[string]any
			if err := json.Unmarshal(request.Params, &params); err != nil {
				t.Fatalf("Unmarshal params error = %v", err)
			}
			if params["mimeType"] != "mcp/plugin" {
				t.Fatalf("mimeType = %#v", params["mimeType"])
			}
			call := calls.Add(1)
			if call == 1 {
				if _, ok := params["cursor"]; ok {
					t.Fatalf("first page cursor = %#v", params["cursor"])
				}
				writeHTTPMCPResponse(t, w, request.ID, map[string]any{
					"resources":  []map[string]any{{"uri": "plugin://first", "name": "first", "mimeType": "mcp/plugin"}},
					"nextCursor": "next-page",
				})
				return
			}
			if params["cursor"] != "next-page" {
				t.Fatalf("continuation cursor = %#v", params["cursor"])
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"resources": []map[string]any{{"uri": "plugin://second", "name": "second", "mimeType": "mcp/plugin"}},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		CodexAppsServerName: {
			Config: ServerConfig{URL: server.URL, Enabled: true},
			Source: string(CatalogSourceCompatibility),
		},
	}})
	first, err := service.ListCodexAppsResources(&CodexAppsResourceListParams{MimeType: "mcp/plugin"})
	if err != nil {
		t.Fatalf("ListCodexAppsResources(first) error = %v", err)
	}
	if len(first.Resources) != 1 || first.Resources[0].URI != "plugin://first" ||
		first.NextCursor == nil || *first.NextCursor != "next-page" {
		t.Fatalf("first page = %#v", first)
	}
	second, err := service.ListCodexAppsResources(&CodexAppsResourceListParams{Cursor: first.NextCursor, MimeType: "mcp/plugin"})
	if err != nil {
		t.Fatalf("ListCodexAppsResources(second) error = %v", err)
	}
	if len(second.Resources) != 1 || second.Resources[0].URI != "plugin://second" || second.NextCursor != nil {
		t.Fatalf("second page = %#v", second)
	}
}

func TestCodexAppsResourceListRejectsExtensionCollisionLikeRust(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		CodexAppsServerName: {
			Config: ServerConfig{URL: server.URL, Enabled: true},
			Source: "plugin",
		},
	}})
	if _, err := service.ListCodexAppsResources(&CodexAppsResourceListParams{MimeType: "mcp/plugin"}); err == nil {
		t.Fatal("ListCodexAppsResources() accepted an extension-owned codex_apps server")
	}
	if requests.Load() != 0 {
		t.Fatalf("extension collision sent %d requests", requests.Load())
	}
}
