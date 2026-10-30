package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Rust #58352 gates early Apps catalog discovery because its resource client
// waited for the Apps server's initial tool inventory. The Go resource path has
// no inventory gate: a page request completes the MCP handshake on demand and
// then sends resources/list, so plugin and skill pages are readable before any
// tools/list has run and later pages reuse the same handshake.
func TestCodexAppsResourceReadSkipsToolInventoryLikeRust(t *testing.T) {
	var mu sync.Mutex
	methods := []string{}
	var initializeCount, resourceCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		mu.Lock()
		methods = append(methods, request.Method)
		mu.Unlock()
		switch request.Method {
		case "initialize":
			initializeCount.Add(1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}, "resources": map[string]any{}},
				"serverInfo":      map[string]string{"name": "apps", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/list":
			resourceCount.Add(1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []map[string]any{{
				"uri":      "plugin://early",
				"name":     "early",
				"mimeType": "mcp/plugin",
			}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "unexpected method "+request.Method)
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		CodexAppsServerName: {
			Config: ServerConfig{URL: server.URL, Enabled: true, StartupTimeout: 2 * time.Second},
			Source: string(CatalogSourceCompatibility),
		},
	}})
	for _, mimeType := range []string{"mcp/plugin", "mcp/skill"} {
		page, err := service.ListCodexAppsResources(&CodexAppsResourceListParams{MimeType: mimeType})
		if err != nil {
			t.Fatalf("ListCodexAppsResources(%s) error = %v", mimeType, err)
		}
		if len(page.Resources) != 1 || page.Resources[0].URI != "plugin://early" {
			t.Fatalf("page(%s) = %#v", mimeType, page)
		}
	}
	mu.Lock()
	seen := append([]string(nil), methods...)
	mu.Unlock()
	for _, method := range seen {
		if method == "tools/list" {
			t.Fatalf("Apps resource reads ran the tool inventory: %v", seen)
		}
	}
	if initializeCount.Load() != 1 || resourceCount.Load() != 2 {
		t.Fatalf("initialize=%d resources/list=%d, want one handshake shared by both pages (%v)",
			initializeCount.Load(), resourceCount.Load(), seen)
	}
}

// Rust #58352 bounds early client acquisition by the configured startup
// timeout and propagates a handshake failure instead of waiting for readiness.
func TestCodexAppsResourceReadStartupTimeoutLikeRust(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Consume the body so the server notices the client's bounded handshake
		// cancelling the connection instead of blocking Close on this handler.
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		CodexAppsServerName: {
			Config: ServerConfig{URL: server.URL, Enabled: true, StartupTimeout: 80 * time.Millisecond},
			Source: string(CatalogSourceCompatibility),
		},
	}})
	started := time.Now()
	if _, err := service.ListCodexAppsResources(&CodexAppsResourceListParams{MimeType: "mcp/plugin"}); err == nil {
		t.Fatal("stalled Apps handshake should fail the resource read")
	}
	if elapsed := time.Since(started); elapsed > 3*time.Second {
		t.Fatalf("stalled Apps resource read took %v, want the startup timeout to bound it", elapsed)
	}
}

func TestCodexAppsResourceReadPropagatesInitializationFailureLikeRust(t *testing.T) {
	var initializeCount, resourceCount atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			initializeCount.Add(1)
			writeHTTPMCPError(t, w, request.ID, -32603, "apps unavailable")
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/list":
			resourceCount.Add(1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "unexpected method "+request.Method)
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		CodexAppsServerName: {
			Config: ServerConfig{URL: server.URL, Enabled: true, StartupTimeout: 2 * time.Second},
			Source: string(CatalogSourceCompatibility),
		},
	}})
	_, err := service.ListCodexAppsResources(&CodexAppsResourceListParams{MimeType: "mcp/plugin"})
	if err == nil || !strings.Contains(err.Error(), "apps unavailable") {
		t.Fatalf("ListCodexAppsResources() error = %v, want the initialization failure", err)
	}
	if initializeCount.Load() != 1 || resourceCount.Load() != 0 {
		t.Fatalf("initialize=%d resources/list=%d, want the failed handshake to stop the page",
			initializeCount.Load(), resourceCount.Load())
	}
}
