package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestModelSearchDiscoveryProjectsConnectorInvocationContextLikeRust(t *testing.T) {
	const toolName = "search_service.web_run"
	var scopedMeta map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": CodexAppsServerName, "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			meta, _ := request.Params["_meta"].(map[string]any)
			turnMeta, scoped := meta["x-codex-turn-metadata"].(map[string]any)
			tool := map[string]any{
				"name":        toolName,
				"description": "Search",
				"inputSchema": map[string]any{"type": "object"},
				"_meta": map[string]any{
					"connector_id": SearchConnector,
					"_codex_apps": map[string]any{
						"connector_name":        "search_service",
						"connector_description": "Hosted search",
					},
				},
			}
			if scoped {
				scopedMeta = turnMeta
				tool["inputSchema"] = map[string]any{
					"type": "object",
					"properties": map[string]any{
						"literature_search_query": map[string]any{"type": "array"},
					},
				}
				tool["_meta"].(map[string]any)["_codex_apps"].(map[string]any)["invocation_context"] = map[string]any{
					"revision": "connector-selected",
				}
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{tool}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		CodexAppsServerName: {Name: CodexAppsServerName, Source: string(CatalogSourceCompatibility), Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	defer service.Close()
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || !status.Data[0].hostOwnedApps {
		t.Fatalf("Apps status = %#v", status.Data)
	}
	binding := service.CaptureBinding(status.Data)
	if got := binding.toolInfos[CodexAppsServerName]; len(got) != 1 || !IsModelSearchMCPTool(&got[0]) {
		t.Fatalf("captured search tool = %#v", got)
	}
	discovery := binding.DiscoverModelSearch(context.Background(), map[string]any{
		"mcp_request_meta": map[string]any{
			"openai/search_context": map[string]any{"model_supported_tools": []string{"literature_search_query"}},
		},
	})
	if discovery.Kind != SearchDiscoveryReady || discovery.Binding == nil {
		t.Fatalf("discovery = %#v", discovery)
	}
	if scopedMeta == nil {
		t.Fatal("discovery did not send scoped turn metadata")
	}
	requestMeta, _ := scopedMeta["mcp_request_meta"].(map[string]any)
	searchContext, _ := requestMeta["openai/search_context"].(map[string]any)
	if searchContext["model_supported_tools"] == nil {
		t.Fatalf("discovered search context = %#v", scopedMeta)
	}
	tool, ok := discovery.Binding.SearchTool()
	if !ok || tool.ConnectorID != SearchConnector || tool.NamespaceDescription != "Hosted search" {
		t.Fatalf("projected runtime tool = %#v", tool)
	}
	properties, _ := tool.Tool.InputSchema["properties"].(map[string]any)
	if _, ok := properties["literature_search_query"]; !ok {
		t.Fatalf("projected schema = %#v", tool.Tool.InputSchema)
	}
	pin, ok := discovery.Binding.SearchInvocationContext()
	if !ok {
		t.Fatal("missing invocation context")
	}
	fields := pin.(map[string]any)
	if fields["revision"] != "connector-selected" {
		t.Fatalf("invocation context = %#v", pin)
	}
	fields["revision"] = "mutated"
	again, _ := discovery.Binding.SearchInvocationContext()
	if again.(map[string]any)["revision"] != "connector-selected" {
		t.Fatalf("mutating the returned pin changed the binding: %#v", again)
	}
}

func TestModelSearchDiscoveryClassifiesFailuresLikeRust(t *testing.T) {
	for _, test := range []struct {
		name string
		err  error
		kind SearchDiscoveryKind
	}{
		{name: "deadline", err: context.DeadlineExceeded, kind: SearchDiscoveryCritical},
		{name: "internal rpc", err: &MCPRemoteError{Code: mcpJSONRPCInternalErrorCode}, kind: SearchDiscoveryCritical},
		{name: "service unavailable", err: &mcpHTTPStatusError{StatusCode: http.StatusServiceUnavailable}, kind: SearchDiscoveryCritical},
		{name: "forbidden", err: &mcpHTTPStatusError{StatusCode: http.StatusForbidden}, kind: SearchDiscoveryUnavailable},
		{name: "invalid request", err: &MCPRemoteError{Code: -32602}, kind: SearchDiscoveryUnavailable},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := modelSearchDiscoveryFailure(test.err); got.Kind != test.kind {
				t.Fatalf("modelSearchDiscoveryFailure(%v) = %#v, want %s", test.err, got, test.kind)
			}
		})
	}
}
