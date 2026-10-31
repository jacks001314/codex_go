package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"codex_go/sandbox"
)

// clientCatalogTestServer is an HTTP MCP server whose catalog, failures, and
// blocked tool calls a test can steer.
type clientCatalogTestServer struct {
	server *httptest.Server

	mu          sync.Mutex
	tools       []string
	fail        bool
	lists       int
	calls       int
	blockCalls  <-chan struct{}
	callStarted chan struct{}
}

func newClientCatalogTestServer(t *testing.T, tools ...string) *clientCatalogTestServer {
	t.Helper()
	ts := &clientCatalogTestServer{
		tools:       append([]string(nil), tools...),
		callStarted: make(chan struct{}, 8),
	}
	ts.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
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
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "catalog", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			ts.mu.Lock()
			ts.lists++
			fail := ts.fail
			tools := append([]string(nil), ts.tools...)
			ts.mu.Unlock()
			if fail {
				http.Error(w, "catalog unavailable", http.StatusInternalServerError)
				return
			}
			entries := make([]map[string]any, 0, len(tools))
			for _, name := range tools {
				entries = append(entries, map[string]any{
					"name":        name,
					"description": name,
					"inputSchema": map[string]any{"type": "object"},
				})
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": entries})
		case "tools/call":
			ts.mu.Lock()
			ts.calls++
			block := ts.blockCalls
			ts.mu.Unlock()
			if block != nil {
				select {
				case ts.callStarted <- struct{}{}:
				default:
				}
				<-block
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": "ok"}},
			})
		case "resources/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
		case "resources/templates/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	t.Cleanup(ts.server.Close)
	return ts
}

func (s *clientCatalogTestServer) url() string { return s.server.URL }

func (s *clientCatalogTestServer) setTools(tools ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tools = append([]string(nil), tools...)
}

func (s *clientCatalogTestServer) setFail(fail bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.fail = fail
}

func (s *clientCatalogTestServer) setBlockCalls(release <-chan struct{}) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blockCalls = release
}

func (s *clientCatalogTestServer) listCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lists
}

func newClientCatalogTestService(t *testing.T, apps *clientCatalogTestServer, docs *clientCatalogTestServer) *MCPService {
	t.Helper()
	servers := map[string]ServerRegistration{
		CodexAppsServerName: {Config: ServerConfig{URL: apps.url(), Enabled: true}},
	}
	profiles := map[string]*sandbox.PermissionProfile{CodexAppsServerName: mcpTestThreadProfile()}
	if docs != nil {
		servers["docs"] = ServerRegistration{Config: ServerConfig{URL: docs.url(), Enabled: true}}
		profiles["docs"] = mcpTestThreadProfile()
	}
	service := NewMCPService(&RuntimeConfig{Servers: servers, ServerPermissionProfiles: profiles})
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func clientCatalogListStatus(t *testing.T, service *MCPService) []MCPServerStatus {
	t.Helper()
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{
		Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	return status.Data
}

func clientCatalogToolNames(tools []MCPToolInfo) []string {
	names := make([]string, 0, len(tools))
	for i := range tools {
		names = append(names, tools[i].Name)
	}
	return names
}

// TestAppsCatalogRefreshStaysWithClientLikeRust mirrors Rust #43031: a refresh
// publishes on the exact client that listed it, keeps the connection, and
// invalidates only that client's prepared calls.
func TestAppsCatalogRefreshStaysWithClientLikeRust(t *testing.T) {
	apps := newClientCatalogTestServer(t, "search_v1")
	docs := newClientCatalogTestServer(t, "run")
	service := newClientCatalogTestService(t, apps, docs)
	binding := service.CaptureBinding(clientCatalogListStatus(t, service))
	generationBefore := service.Generation()
	appsClientBefore := service.httpClients[CodexAppsServerName].client
	docsClientBefore := service.httpClients["docs"].client

	apps.setTools("search_v1", "search_v2")
	snapshot, err := service.RefreshCodexAppsTools(context.Background())
	if err != nil {
		t.Fatalf("RefreshCodexAppsTools() error = %v", err)
	}
	if got := clientCatalogToolNames(snapshot.Tools); len(got) != 2 || got[0] != "search_v1" || got[1] != "search_v2" {
		t.Fatalf("refreshed tools = %#v, want search_v1 and search_v2", got)
	}
	if !snapshot.ModelVisibleToolNames["search_v1"] || !snapshot.ModelVisibleToolNames["search_v2"] {
		t.Fatalf("model visible names = %#v, want both refreshed tools", snapshot.ModelVisibleToolNames)
	}
	if service.Generation() != generationBefore {
		t.Fatal("Apps refresh must not invalidate unrelated MCP clients")
	}
	if service.httpClients[CodexAppsServerName].client != appsClientBefore {
		t.Fatal("Apps refresh replaced the client instead of reusing it")
	}
	if service.httpClients["docs"].client != docsClientBefore {
		t.Fatal("Apps refresh replaced the unrelated docs client")
	}

	// A call prepared against the previous Apps catalog is rejected before it
	// is dispatched, even for a tool the refreshed catalog still contains.
	_, err = binding.CallTool(&MCPToolCallParams{Server: CodexAppsServerName, Tool: "search_v1"})
	if !errors.Is(err, ErrInvalidMCPRequest) || !strings.Contains(err.Error(), "catalog changed") {
		t.Fatalf("stale Apps call error = %v, want catalog-changed rejection", err)
	}
	// Prepared calls for unrelated servers stay valid.
	if _, err := binding.CallTool(&MCPToolCallParams{Server: "docs", Tool: "run"}); err != nil {
		t.Fatalf("unrelated prepared call error = %v", err)
	}

	refreshed := service.CaptureBinding(clientCatalogListStatus(t, service))
	if _, err := refreshed.CallTool(&MCPToolCallParams{Server: CodexAppsServerName, Tool: "search_v2"}); err != nil {
		t.Fatalf("refreshed call error = %v", err)
	}
	appsRevision := refreshed.catalogs[CodexAppsServerName]
	if appsRevision.revision != 1 {
		t.Fatalf("refreshed Apps catalog revision = %d, want 1", appsRevision.revision)
	}
	docsRevision := refreshed.catalogs["docs"]
	if docsRevision.revision != 0 {
		t.Fatalf("docs catalog revision = %d, want 0", docsRevision.revision)
	}
	if appsRevision.catalog == docsRevision.catalog {
		t.Fatal("bindings must key catalogs by client identity")
	}
	// Connection reuse keeps the refreshed catalog revision across another
	// binding capture.
	repeated := service.CaptureBinding(clientCatalogListStatus(t, service))
	if got := repeated.catalogs[CodexAppsServerName]; got.catalog != appsRevision.catalog || got.revision != 1 {
		t.Fatalf("reused client catalog = %#v, want revision 1 on the same client", got)
	}
}

// TestAppsCatalogRefreshFailureKeepsPublishedCatalogLikeRust verifies a failed
// refresh leaves the client's catalog and revision untouched.
func TestAppsCatalogRefreshFailureKeepsPublishedCatalogLikeRust(t *testing.T) {
	apps := newClientCatalogTestServer(t, "search_v1")
	service := newClientCatalogTestService(t, apps, nil)
	if _, err := service.RefreshCodexAppsTools(context.Background()); err != nil {
		t.Fatalf("initial RefreshCodexAppsTools() error = %v", err)
	}
	binding := service.CaptureBinding(clientCatalogListStatus(t, service))
	apps.setFail(true)
	_, err := service.RefreshCodexAppsTools(context.Background())
	if err == nil || !strings.Contains(err.Error(), "failed to refresh tools for MCP server") {
		t.Fatalf("failed refresh error = %v", err)
	}
	catalog := service.liveClientToolCatalog(CodexAppsServerName)
	if catalog == nil || catalog.currentRevision() != 1 {
		t.Fatalf("catalog after failed refresh = %#v, want revision 1", catalog)
	}
	if got := clientCatalogToolNames(catalog.listedTools()); len(got) != 1 || got[0] != "search_v1" {
		t.Fatalf("catalog tools after failed refresh = %#v, want search_v1", got)
	}
	// Failure is not a publication, so the previously prepared binding stays
	// valid.
	if _, err := binding.CallTool(&MCPToolCallParams{Server: CodexAppsServerName, Tool: "search_v1"}); err != nil {
		t.Fatalf("prepared call after failed refresh error = %v", err)
	}
}

// TestAppsCatalogPublicationWaitsForActiveCallsLikeRust verifies that a refresh
// cannot advance the revision while a prepared call still holds catalog
// authority.
func TestAppsCatalogPublicationWaitsForActiveCallsLikeRust(t *testing.T) {
	release := make(chan struct{})
	apps := newClientCatalogTestServer(t, "search_v1")
	apps.setBlockCalls(release)
	service := newClientCatalogTestService(t, apps, nil)
	binding := service.CaptureBinding(clientCatalogListStatus(t, service))

	callDone := make(chan error, 1)
	go func() {
		_, err := binding.CallTool(&MCPToolCallParams{Server: CodexAppsServerName, Tool: "search_v1"})
		callDone <- err
	}()
	select {
	case <-apps.callStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("tool call never reached the server")
	}

	apps.setTools("search_v1", "search_v2")
	refreshDone := make(chan error, 1)
	go func() {
		_, err := service.RefreshCodexAppsTools(context.Background())
		refreshDone <- err
	}()
	select {
	case err := <-refreshDone:
		t.Fatalf("refresh published while a call held the catalog (err = %v)", err)
	case <-time.After(150 * time.Millisecond):
	}
	close(release)
	if err := <-callDone; err != nil {
		t.Fatalf("prepared call error = %v", err)
	}
	if err := <-refreshDone; err != nil {
		t.Fatalf("refresh error = %v", err)
	}
	if _, err := binding.CallTool(&MCPToolCallParams{Server: CodexAppsServerName, Tool: "search_v1"}); !errors.Is(err, ErrInvalidMCPRequest) {
		t.Fatalf("binding after completed refresh error = %v, want rejection", err)
	}
}
