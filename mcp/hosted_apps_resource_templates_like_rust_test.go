package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

// TestHostedAppsSkipResourceTemplateListingLikeRust covers Rust #57861: the
// host-owned Apps registration never asks for resource templates and reports an
// empty template list, while an extension that contributes the same server name
// still discovers them.
func TestHostedAppsSkipResourceTemplateListingLikeRust(t *testing.T) {
	testCases := []struct {
		name              string
		registration      ServerRegistration
		wantTemplates     int
		wantTemplateCalls int
	}{
		{
			name: "host owned apps",
			registration: ServerRegistration{
				Name:   CodexAppsServerName,
				Config: ServerConfig{URL: "", Enabled: true},
				Source: string(CatalogSourceCompatibility),
			},
			wantTemplates:     0,
			wantTemplateCalls: 0,
		},
		{
			name: "extension contributes codex_apps",
			registration: ServerRegistration{
				Name:          CodexAppsServerName,
				Config:        ServerConfig{Enabled: true},
				Source:        string(CatalogSourceExtension),
				ContributorID: "extension",
			},
			wantTemplates:     1,
			wantTemplateCalls: 1,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			methods := map[string]int{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					ID     int64  `json:"id"`
					Method string `json:"method"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Fatalf("Decode() error = %v", err)
				}
				mu.Lock()
				methods[request.Method]++
				mu.Unlock()
				switch request.Method {
				case "initialize":
					w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
					writeHTTPMCPResponse(t, w, request.ID, map[string]any{
						"protocolVersion": defaultMCPProtocol,
						"capabilities":    map[string]any{"resources": map[string]any{}},
						"serverInfo":      map[string]string{"name": "apps", "version": "test"},
					})
				case "notifications/initialized":
					w.WriteHeader(http.StatusAccepted)
				case "tools/list":
					writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
				case "resources/list":
					writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
				case "resources/templates/list":
					writeHTTPMCPResponse(t, w, request.ID, map[string]any{
						"resourceTemplates": []map[string]any{{"uriTemplate": "test://{id}", "name": "test-template"}},
					})
				default:
					writeHTTPMCPError(t, w, request.ID, -32601, "not found")
				}
			}))
			defer server.Close()

			registration := tc.registration
			registration.Config.URL = server.URL
			service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
				CodexAppsServerName: registration,
			}})
			status, err := service.ListStatusChecked(&MCPListServerStatusParams{
				Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull},
			})
			if err != nil {
				t.Fatalf("ListStatusChecked() error = %v", err)
			}
			if len(status.Data) != 1 {
				t.Fatalf("status = %#v", status.Data)
			}
			if got := len(status.Data[0].ResourceTemplates); got != tc.wantTemplates {
				t.Fatalf("resource templates = %d, want %d", got, tc.wantTemplates)
			}
			mu.Lock()
			templateCalls := methods["resources/templates/list"]
			resourceCalls := methods["resources/list"]
			mu.Unlock()
			if templateCalls != tc.wantTemplateCalls {
				t.Fatalf("resources/templates/list calls = %d, want %d", templateCalls, tc.wantTemplateCalls)
			}
			// Discovery still lists resources; only the template request is skipped.
			if resourceCalls == 0 {
				t.Fatal("resources/list was not requested")
			}
		})
	}
}
