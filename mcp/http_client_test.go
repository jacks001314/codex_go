package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type mcpTestRoundTripFunc func(*http.Request) (*http.Response, error)

func (f mcpTestRoundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

func TestMCPHTTPAndOAuthReuseSharedHTTPClient(t *testing.T) {
	var runtimeHeader string
	var oauthHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/mcp":
			runtimeHeader = r.Header.Get("X-Codex-Shared-Client")
		case "/oauth":
			oauthHeader = r.Header.Get("X-Codex-Shared-Client")
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	baseTransport := server.Client().Transport
	shared := &http.Client{Transport: mcpTestRoundTripFunc(func(request *http.Request) (*http.Response, error) {
		cloned := request.Clone(request.Context())
		cloned.Header = request.Header.Clone()
		cloned.Header.Set("X-Codex-Shared-Client", "configured")
		return baseTransport.RoundTrip(cloned)
	})}
	config := ServerConfig{URL: server.URL + "/mcp", Enabled: true}
	service := NewMCPService(&RuntimeConfig{
		Servers:    map[string]ServerRegistration{"docs": {Config: config}},
		HTTPClient: shared,
	})

	runtimeConfig, _ := service.serverConfig("docs")
	client := service.httpClientForServer("docs", &runtimeConfig)
	response, err := client.doHTTPRequest(runtimeConfig.URL, []byte(`{}`), "", "")
	if err != nil {
		t.Fatalf("MCP request error = %v", err)
	}
	_ = response.Body.Close()
	oauthRequest, err := http.NewRequest(http.MethodGet, server.URL+"/oauth", nil)
	if err != nil {
		t.Fatalf("NewRequest() error = %v", err)
	}
	oauthResponse, err := client.oauthHTTPClient(time.Second).Do(oauthRequest)
	if err != nil {
		t.Fatalf("OAuth request error = %v", err)
	}
	_ = oauthResponse.Body.Close()
	if runtimeHeader != "configured" || oauthHeader != "configured" {
		t.Fatalf("shared client headers runtime=%q oauth=%q", runtimeHeader, oauthHeader)
	}
}

func TestApplyRuntimeConfigReplacesConnectionWhenSharedHTTPClientChanges(t *testing.T) {
	config := ServerConfig{URL: "https://example.test/mcp", Enabled: true}
	firstShared := &http.Client{Transport: mcpTestRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("first")
	})}
	service := NewMCPService(&RuntimeConfig{
		Servers:    map[string]ServerRegistration{"docs": {Config: config}},
		HTTPClient: firstShared,
	})
	runtimeConfig, _ := service.serverConfig("docs")
	first := service.httpClientForServer("docs", &runtimeConfig)

	secondShared := &http.Client{Transport: mcpTestRoundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("second")
	})}
	service.ApplyRuntimeConfig(&RuntimeConfig{
		Servers:    map[string]ServerRegistration{"docs": {Config: config}},
		HTTPClient: secondShared,
	})
	runtimeConfig, _ = service.serverConfig("docs")
	second := service.httpClientForServer("docs", &runtimeConfig)
	if second == first {
		t.Fatal("ApplyRuntimeConfig() reused an MCP connection after the shared HTTP client changed")
	}
	if !first.isClosed() {
		t.Fatal("ApplyRuntimeConfig() did not close the connection using the old shared HTTP client")
	}
}

func TestHTTPMCPToolListCallAndResource(t *testing.T) {
	var authHeader string
	var toolCallParams map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if got := r.Header.Get(mcpHTTPProtocolVersionHeader); got != defaultMCPProtocol {
			t.Fatalf("MCP protocol header = %q", got)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
		case "tools/call":
			if err := json.Unmarshal(request.Params, &toolCallParams); err != nil {
				t.Fatalf("Unmarshal tool call params returned error: %v", err)
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"content":           []map[string]string{{"type": "text", "text": string(request.Params)}},
				"structuredContent": map[string]any{"echoed": "hi", "threadId": "thread-live"},
				"isError":           false,
				"_meta":             map[string]any{"calledBy": "mcp-app"},
			})
		case "resources/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
		case "resources/templates/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		case "resources/read":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"contents": []map[string]string{{"uri": "file://demo", "text": "demo"}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()
	t.Setenv("MCP_TOKEN", "secret-token")

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, BearerTokenEnvVar: "MCP_TOKEN", Enabled: true}},
	}})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || len(status.Data[0].Tools) != 1 || status.Data[0].Tools[0].Name != "echo" {
		t.Fatalf("status = %#v", status)
	}
	if authHeader != "Bearer secret-token" {
		t.Fatalf("Authorization = %q", authHeader)
	}
	call, err := service.CallTool(&MCPToolCallParams{
		ThreadID:   "thread-live",
		ServerName: "http",
		ToolName:   "echo",
		Arguments:  map[string]any{"text": "hi"},
		Meta:       map[string]any{"source": "test-client", "threadId": "stale-thread"},
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(call.Content) != 1 || !strings.Contains(call.Content[0].Text, `"text":"hi"`) {
		t.Fatalf("call = %#v", call)
	}
	structured, ok := call.StructuredContent.(map[string]any)
	if !ok || structured["echoed"] != "hi" || structured["threadId"] != "thread-live" {
		t.Fatalf("structuredContent = %#v", call.StructuredContent)
	}
	if call.IsError == nil || *call.IsError {
		t.Fatalf("isError = %#v", call.IsError)
	}
	responseMeta, ok := call.Meta.(map[string]any)
	if !ok || responseMeta["calledBy"] != "mcp-app" {
		t.Fatalf("response _meta = %#v", call.Meta)
	}
	meta, ok := toolCallParams["_meta"].(map[string]any)
	if !ok || meta["source"] != "test-client" || meta["threadId"] != "thread-live" {
		t.Fatalf("tool call _meta = %#v params=%#v", toolCallParams["_meta"], toolCallParams)
	}
	resource, err := service.ReadResource(&MCPResourceReadParams{ServerName: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource() error = %v", err)
	}
	if len(resource.Contents) != 1 || resource.Contents[0].Text != "demo" {
		t.Fatalf("resource = %#v", resource)
	}
}

func TestHTTPMCPAppliesStaticAndEnvironmentHeaders(t *testing.T) {
	t.Setenv("MCP_HEADER_TOKEN", "from-env")
	var gotStatic, gotEnv, gotAuthorization string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotStatic = r.Header.Get("X-Test-Static")
		gotEnv = r.Header.Get("X-Test-Env")
		gotAuthorization = r.Header.Get("Authorization")
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "header-session")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"protocolVersion": defaultMCPProtocol, "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "header-test", "version": "1"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"headers": {Config: ServerConfig{URL: server.URL, Enabled: true, HTTPHeaders: map[string]string{"X-Test-Static": "static", "Authorization": "Bearer configured"}, EnvHTTPHeaders: map[string]string{"X-Test-Env": "MCP_HEADER_TOKEN"}}},
	}})
	defer service.Close()
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if gotStatic != "static" || gotEnv != "from-env" || gotAuthorization != "Bearer configured" {
		t.Fatalf("headers static=%q env=%q authorization=%q", gotStatic, gotEnv, gotAuthorization)
	}
}

func TestHTTPMCPRuntimeHeadersOverrideConfiguredAuthorization(t *testing.T) {
	var gotAuthorization, gotProtocol string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuthorization = r.Header.Get("Authorization")
		gotProtocol = r.Header.Get(mcpHTTPProtocolVersionHeader)
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"protocolVersion": defaultMCPProtocol, "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "override-test", "version": "1"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"headers": {Config: ServerConfig{
			URL: server.URL, Enabled: true,
			HTTPHeaders: map[string]string{"Authorization": "Bearer configured", mcpHTTPProtocolVersionHeader: "invalid"},
			ApplyHTTPRequest: func(request *http.Request, _ []byte) error {
				request.Header.Set("Authorization", "Bearer runtime")
				return nil
			},
		}},
	}})
	defer service.Close()
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if gotAuthorization != "Bearer runtime" || gotProtocol != defaultMCPProtocol {
		t.Fatalf("authorization=%q protocol=%q", gotAuthorization, gotProtocol)
	}
}

func TestHTTPMCPIgnoresInvalidConfiguredHeadersLikeRust(t *testing.T) {
	t.Setenv("MCP_INVALID_HEADER_VALUE", "bad\nvalue")
	var gotValid, gotInvalidName, gotInvalidValue string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotValid = r.Header.Get("X-Test-Valid")
		gotInvalidName = r.Header.Get("Bad Header")
		gotInvalidValue = r.Header.Get("X-Test-Invalid-Value")
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"protocolVersion": defaultMCPProtocol, "capabilities": map[string]any{}, "serverInfo": map[string]string{"name": "invalid-header-test", "version": "1"}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"headers": {Config: ServerConfig{
			URL: server.URL, Enabled: true,
			HTTPHeaders:    map[string]string{"X-Test-Valid": "yes", "Bad Header": "ignored"},
			EnvHTTPHeaders: map[string]string{"X-Test-Invalid-Value": "MCP_INVALID_HEADER_VALUE"},
		}},
	}})
	defer service.Close()
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if gotValid != "yes" || gotInvalidName != "" || gotInvalidValue != "" {
		t.Fatalf("headers valid=%q invalid-name=%q invalid-value=%q", gotValid, gotInvalidName, gotInvalidValue)
	}
}

func TestHTTPMCPConfiguredAuthorizationSkipsStoredOAuthLikeRust(t *testing.T) {
	client := newMCPHTTPClient(&ServerConfig{
		URL:             "https://mcp.example.test",
		CodexHome:       t.TempDir(),
		OAuthServerName: "headers",
		HTTPHeaders:     map[string]string{"Authorization": "Bearer configured"},
	})
	if token, oauth := client.authorizationBearerToken(false); token != "" || oauth {
		t.Fatalf("authorizationBearerToken() = %q, %t, want configured header to bypass OAuth", token, oauth)
	}
}

func TestHTTPMCPInventoryFollowsPagination(t *testing.T) {
	var toolsCursors []string
	var resourceCursors []string
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
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			cursor := mcpTestCursorParam(t, request.Params)
			toolsCursors = append(toolsCursors, cursor)
			if cursor == "" {
				writeHTTPMCPResponse(t, w, request.ID, map[string]any{
					"tools":      []map[string]any{{"name": "first"}},
					"nextCursor": "page-2",
				})
				return
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "second"}}})
		case "resources/list":
			cursor := mcpTestCursorParam(t, request.Params)
			resourceCursors = append(resourceCursors, cursor)
			if cursor == "" {
				writeHTTPMCPResponse(t, w, request.ID, map[string]any{
					"resources":  []map[string]any{{"uri": "file://first"}},
					"nextCursor": "resource-page-2",
				})
				return
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []map[string]any{{"uri": "file://second"}}})
		case "resources/templates/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []map[string]any{{"uriTemplate": "file://{name}"}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	inventory, err := listMCPHTTPInventoryWithClient(newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true}))
	if err != nil {
		t.Fatalf("listMCPHTTPInventoryWithClient() error = %v", err)
	}
	if len(inventory.Tools) != 2 || inventory.Tools[0].Name != "first" || inventory.Tools[1].Name != "second" {
		t.Fatalf("tools = %#v", inventory.Tools)
	}
	if len(inventory.Resources) != 2 || inventory.Resources[0].URI != "file://first" || inventory.Resources[1].URI != "file://second" {
		t.Fatalf("resources = %#v", inventory.Resources)
	}
	if len(inventory.ResourceTemplates) != 1 || inventory.ResourceTemplates[0].URITemplate != "file://{name}" {
		t.Fatalf("resource templates = %#v", inventory.ResourceTemplates)
	}
	if strings.Join(toolsCursors, ",") != ",page-2" {
		t.Fatalf("tools cursors = %#v", toolsCursors)
	}
	if strings.Join(resourceCursors, ",") != ",resource-page-2" {
		t.Fatalf("resource cursors = %#v", resourceCursors)
	}
}

func TestHTTPMCPCatalogPaginationUsesOneConfiguredTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64           `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Errorf("Decode() error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-timeout")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"protocolVersion": defaultMCPProtocol, "capabilities": map[string]any{}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			time.Sleep(45 * time.Millisecond)
			next := "second"
			if mcpTestCursorParam(t, request.Params) == "second" {
				next = "third"
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{}, "nextCursor": next})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()
	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true, ToolTimeout: 70 * time.Millisecond})
	_, err := listMCPHTTPTools(client, nil)
	if err == nil || err.Error() != "tools/list pagination timed out after 70ms" {
		t.Fatalf("listMCPHTTPTools() error = %v", err)
	}
}

func TestMCPStatusNilParamsDefaultsToFullInventory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
		case "resources/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []map[string]any{{"uri": "file://demo"}}})
		case "resources/templates/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []map[string]any{{"uriTemplate": "file://{name}"}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	status, err := service.ListStatusChecked(nil)
	if err != nil {
		t.Fatalf("ListStatusChecked(nil) error = %v", err)
	}
	if len(status.Data) != 1 || len(status.Data[0].Tools) != 1 || status.Data[0].Tools[0].Name != "echo" {
		t.Fatalf("tools status = %#v", status)
	}
	if len(status.Data[0].Resources) != 1 || status.Data[0].Resources[0].URI != "file://demo" {
		t.Fatalf("resources status = %#v", status.Data[0].Resources)
	}
	if len(status.Data[0].ResourceTemplates) != 1 || status.Data[0].ResourceTemplates[0].URITemplate != "file://{name}" {
		t.Fatalf("templates status = %#v", status.Data[0].ResourceTemplates)
	}
}

func TestMCPServiceReusesHTTPClientSession(t *testing.T) {
	var initializeCount atomic.Int64
	var initializedNotifications atomic.Int64
	var deletedSessions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletedSessions = append(deletedSessions, r.Header.Get(mcpHTTPSessionIDHeader))
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			initializeCount.Add(1)
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			initializedNotifications.Add(1)
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if got := r.Header.Get(mcpHTTPSessionIDHeader); got != "session-1" {
				t.Fatalf("tools/list session header = %q", got)
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
		case "resources/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
		case "resources/templates/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		case "tools/call":
			if got := r.Header.Get(mcpHTTPSessionIDHeader); got != "session-1" {
				t.Fatalf("tools/call session header = %q", got)
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"content": []map[string]string{{"type": "text", "text": "done"}},
			})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	if _, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}}); err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if _, err := service.CallTool(&MCPToolCallParams{Server: "http", Tool: "echo"}); err != nil {
		t.Fatalf("CallTool(first) error = %v", err)
	}
	if initializeCount.Load() != 1 || initializedNotifications.Load() != 1 {
		t.Fatalf("initialize count = %d initialized notifications = %d, want 1 each", initializeCount.Load(), initializedNotifications.Load())
	}

	service.Refresh()
	if got := strings.Join(deletedSessions, ","); got != "session-1" {
		t.Fatalf("deleted sessions = %q, want session-1", got)
	}
	if _, err := service.CallTool(&MCPToolCallParams{Server: "http", Tool: "echo"}); err != nil {
		t.Fatalf("CallTool(after refresh) error = %v", err)
	}
	if initializeCount.Load() != 2 || initializedNotifications.Load() != 2 {
		t.Fatalf("after refresh initialize count = %d initialized notifications = %d, want 2 each", initializeCount.Load(), initializedNotifications.Load())
	}
}

func TestMCPServiceClosesHTTPClientWhenCacheKeyChanges(t *testing.T) {
	var deletedSessions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletedSessions = append(deletedSessions, r.Header.Get(mcpHTTPSessionIDHeader))
			w.WriteHeader(http.StatusAccepted)
			return
		}
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-config-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{})
	firstConfig := &ServerConfig{URL: server.URL, Enabled: true}
	firstClient := service.httpClientForServer("http", firstConfig)
	var first struct {
		Tools []MCPToolInfo `json:"tools"`
	}
	if err := firstClient.Call("tools/list", map[string]any{}, &first); err != nil {
		t.Fatalf("Call(first) error = %v", err)
	}

	secondConfig := &ServerConfig{URL: server.URL, OAuthClientID: "changed-client", Enabled: true}
	secondClient := service.httpClientForServer("http", secondConfig)
	if secondClient == firstClient {
		t.Fatalf("httpClientForServer returned old client after cache key change")
	}
	if got := strings.Join(deletedSessions, ","); got != "session-config-1" {
		t.Fatalf("deleted sessions = %q, want session-config-1", got)
	}
}

func TestHTTPMCPReinitializesExpiredSession(t *testing.T) {
	initializeCount := 0
	initializedNotifications := 0
	toolsListCount := 0
	var toolsListSessions []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			initializeCount++
			sessionID := "session-1"
			if initializeCount == 2 {
				sessionID = "session-2"
			}
			w.Header().Set(mcpHTTPSessionIDHeader, sessionID)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			initializedNotifications++
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			toolsListCount++
			toolsListSessions = append(toolsListSessions, r.Header.Get(mcpHTTPSessionIDHeader))
			if toolsListCount == 2 && r.Header.Get(mcpHTTPSessionIDHeader) == "session-1" {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte("session expired"))
				return
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	var first struct {
		Tools []MCPToolInfo `json:"tools"`
	}
	if err := client.Call("tools/list", map[string]any{}, &first); err != nil {
		t.Fatalf("Call(first) error = %v", err)
	}
	var second struct {
		Tools []MCPToolInfo `json:"tools"`
	}
	if err := client.Call("tools/list", map[string]any{}, &second); err != nil {
		t.Fatalf("Call(second) error = %v", err)
	}
	if initializeCount != 2 || initializedNotifications != 2 {
		t.Fatalf("initialize count = %d initialized notifications = %d, want 2 each", initializeCount, initializedNotifications)
	}
	if got := strings.Join(toolsListSessions, ","); got != "session-1,session-1,session-2" {
		t.Fatalf("tools/list sessions = %q", got)
	}
	if len(second.Tools) != 1 || second.Tools[0].Name != "echo" {
		t.Fatalf("second tools = %#v", second.Tools)
	}
}

func TestHTTPMCPReadResourceUsesCacheAndClones(t *testing.T) {
	readCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/read":
			readCount++
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"contents": []map[string]any{{
				"uri":  "file://demo",
				"text": "demo",
				"_meta": map[string]any{
					"nested": map[string]any{"value": "original"},
				},
			}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	first, err := service.ReadResource(&MCPResourceReadParams{ServerName: "http", URI: " file://demo "})
	if err != nil {
		t.Fatalf("ReadResource(first) error = %v", err)
	}
	first.Contents[0].Text = "mutated"
	first.Contents[0].Meta["nested"].(map[string]any)["value"] = "mutated"

	second, err := service.ReadResource(&MCPResourceReadParams{Server: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource(second) error = %v", err)
	}
	if readCount != 1 {
		t.Fatalf("resources/read count = %d, want 1", readCount)
	}
	if second.Contents[0].Text != "demo" || second.Contents[0].Meta["nested"].(map[string]any)["value"] != "original" {
		t.Fatalf("cached response leaked mutation: %#v", second)
	}
}

func TestHTTPMCPResourceCacheClearedWhenOpenAIFormCapabilityChanges(t *testing.T) {
	readCount := 0
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
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/read":
			readCount++
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"contents": []map[string]any{{
				"uri":  "file://demo",
				"text": fmt.Sprintf("demo-%d", readCount),
			}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	first, err := service.ReadResource(&MCPResourceReadParams{ServerName: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource(first) error = %v", err)
	}
	second, err := service.ReadResource(&MCPResourceReadParams{ServerName: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource(second) error = %v", err)
	}
	service.SetOpenAIFormElicitationEnabled(true)
	third, err := service.ReadResource(&MCPResourceReadParams{ServerName: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource(third) error = %v", err)
	}
	if first.Contents[0].Text != "demo-1" || second.Contents[0].Text != "demo-1" || third.Contents[0].Text != "demo-2" {
		t.Fatalf("resource texts = %q, %q, %q", first.Contents[0].Text, second.Contents[0].Text, third.Contents[0].Text)
	}
	if readCount != 2 {
		t.Fatalf("resources/read count = %d, want 2", readCount)
	}
}

func TestHTTPMCPReadResourceCacheIsScopedByThread(t *testing.T) {
	readCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "resources/read":
			readCount++
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"contents": []map[string]any{{
				"uri":  "file://demo",
				"text": fmt.Sprintf("demo-%d", readCount),
			}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	threadA := "thread-a"
	threadB := "thread-b"
	rootsByThread := map[string]string{
		threadA: "file:///thread-a",
		threadB: "file:///thread-b",
	}
	var rootThreads []string
	service.SetRootsProvider(MCPRootsProviderFunc(func(threadID string) []MCPRoot {
		rootThreads = append(rootThreads, threadID)
		return []MCPRoot{{URI: rootsByThread[threadID]}}
	}))

	first, err := service.ReadResource(&MCPResourceReadParams{ThreadID: &threadA, ServerName: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource(first) error = %v", err)
	}
	second, err := service.ReadResource(&MCPResourceReadParams{ThreadID: &threadB, ServerName: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource(second) error = %v", err)
	}
	third, err := service.ReadResource(&MCPResourceReadParams{ThreadID: &threadA, ServerName: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource(third) error = %v", err)
	}
	rootsByThread[threadA] = "file:///thread-a-updated"
	fourth, err := service.ReadResource(&MCPResourceReadParams{ThreadID: &threadA, ServerName: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource(fourth) error = %v", err)
	}
	fifth, err := service.ReadResource(&MCPResourceReadParams{ThreadID: &threadA, ServerName: "http", URI: "file://demo"})
	if err != nil {
		t.Fatalf("ReadResource(fifth) error = %v", err)
	}
	if first.Contents[0].Text != "demo-1" || second.Contents[0].Text != "demo-2" || third.Contents[0].Text != "demo-1" ||
		fourth.Contents[0].Text != "demo-3" || fifth.Contents[0].Text != "demo-3" {
		t.Fatalf("resource texts = %q, %q, %q, %q, %q", first.Contents[0].Text, second.Contents[0].Text, third.Contents[0].Text, fourth.Contents[0].Text, fifth.Contents[0].Text)
	}
	if readCount != 3 {
		t.Fatalf("resources/read count = %d, want 3", readCount)
	}
	if got := strings.Join(rootThreads, ","); got != "thread-a,thread-b,thread-a,thread-a,thread-a" {
		t.Fatalf("root provider threads = %q", got)
	}
}

func TestHTTPMCPUsesStoredOAuthToken(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		var request struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
	}))
	defer server.Close()

	home := t.TempDir()
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       server.URL,
		ClientID:        "client-1",
		AccessToken:     "oauth-token",
		ExpiresAtMillis: &expiresAt,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: server.URL, OAuthClientID: "client-1", Enabled: true}},
	}, CodexHome: home})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].AuthStatus != MCPAuthOAuth {
		t.Fatalf("status = %#v", status)
	}
	if authHeader != "Bearer oauth-token" {
		t.Fatalf("Authorization = %q", authHeader)
	}
}

func TestHTTPMCPSetServerConfigInheritsOAuthStoreCodexHome(t *testing.T) {
	var authHeader string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader = r.Header.Get("Authorization")
		var request struct {
			ID int64 `json:"id"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
	}))
	defer server.Close()

	home := t.TempDir()
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       server.URL,
		ClientID:        "client-1",
		AccessToken:     "oauth-token",
		ExpiresAtMillis: &expiresAt,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{CodexHome: home})
	service.SetServerConfig("docs", &ServerConfig{URL: server.URL, OAuthClientID: "client-1", Enabled: true})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].AuthStatus != MCPAuthOAuth {
		t.Fatalf("status = %#v", status)
	}
	if authHeader != "Bearer oauth-token" {
		t.Fatalf("Authorization = %q", authHeader)
	}
}

func TestHTTPMCPRefreshesExpiredOAuthToken(t *testing.T) {
	var authHeader string
	var sawRefresh bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			sawRefresh = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-old" || r.Form.Get("client_id") != "client-1" || r.Form.Get("client_secret") != "secret-1" {
				t.Fatalf("refresh form = %#v", r.Form)
			}
			writeJSON(t, w, map[string]any{
				"access_token":  "oauth-new",
				"refresh_token": "refresh-new",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			authHeader = r.Header.Get("Authorization")
			var request struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if request.Method == "initialize" {
				w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			}
			if request.Method == "notifications/initialized" {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			result := map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			}
			if request.Method == "tools/list" {
				result = map[string]any{"tools": []any{}}
			}
			writeHTTPMCPResponse(t, w, request.ID, result)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configURL := server.URL + "/mcp"
	expired := time.Now().Add(-time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		ClientSecret:    " secret-1 ",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-old",
		Scopes:          []string{"read"},
		ExpiresAtMillis: &expired,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: configURL, OAuthClientID: "client-1", Enabled: true}},
	}, CodexHome: home})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].AuthStatus != MCPAuthOAuth {
		t.Fatalf("status = %#v", status)
	}
	if !sawRefresh {
		t.Fatalf("refresh endpoint was not called")
	}
	if authHeader != "Bearer oauth-new" {
		t.Fatalf("Authorization = %q", authHeader)
	}
	loaded, err := NewOAuthStore(home).Load("docs", configURL)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != "oauth-new" || loaded.RefreshToken != "refresh-new" || loaded.ClientSecret != "secret-1" {
		t.Fatalf("loaded tokens = %#v", loaded)
	}
}

func TestHTTPMCPRefreshesOAuthTokenAfterUnauthorizedResponse(t *testing.T) {
	var sawRefresh bool
	var initializeAuthHeaders []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			sawRefresh = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-1" {
				t.Fatalf("refresh form = %#v", r.Form)
			}
			writeJSON(t, w, map[string]any{
				"access_token":  "oauth-new",
				"refresh_token": "refresh-2",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			var request struct {
				ID     int64  `json:"id"`
				Method string `json:"method"`
			}
			if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			if request.Method == "initialize" {
				initializeAuthHeaders = append(initializeAuthHeaders, r.Header.Get("Authorization"))
				if r.Header.Get("Authorization") == "Bearer oauth-old" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte("stale token"))
					return
				}
				w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			}
			if request.Method == "notifications/initialized" {
				w.WriteHeader(http.StatusAccepted)
				return
			}
			result := map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			}
			if request.Method == "tools/list" {
				result = map[string]any{"tools": []any{}}
			}
			writeHTTPMCPResponse(t, w, request.ID, result)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configURL := server.URL + "/mcp"
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-1",
		ExpiresAtMillis: &expiresAt,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: configURL, OAuthClientID: "client-1", Enabled: true}},
	}, CodexHome: home})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].State != MCPServerReady {
		t.Fatalf("status = %#v", status)
	}
	if !sawRefresh {
		t.Fatalf("refresh endpoint was not called")
	}
	if strings.Join(initializeAuthHeaders, ",") != "Bearer oauth-old,Bearer oauth-new" {
		t.Fatalf("initialize Authorization headers = %#v", initializeAuthHeaders)
	}
}

func TestHTTPMCPDeletesStoredOAuthTokenWhenRefreshIsPermanentFailure(t *testing.T) {
	var sawRefresh bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			sawRefresh = true
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(t, w, map[string]any{
				"error":             "invalid_grant",
				"error_description": "refresh token revoked",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configURL := server.URL + "/mcp"
	expired := time.Now().Add(-time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-old",
		ExpiresAtMillis: &expired,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	client := newMCPHTTPClient(&ServerConfig{
		URL:             configURL,
		OAuthClientID:   "client-1",
		OAuthServerName: "docs",
		CodexHome:       home,
		Enabled:         true,
	})
	if token := client.bearerToken(); token != "" {
		t.Fatalf("bearerToken() = %q, want empty after failed refresh", token)
	}
	if !sawRefresh {
		t.Fatalf("refresh endpoint was not called")
	}
	loaded, err := NewOAuthStore(home).Load("docs", configURL)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded != nil {
		t.Fatalf("stored token = %#v, want deleted after invalid_grant", loaded)
	}
}

func TestMCPStatusRecomputesOAuthStatusAfterRefreshDeletesToken(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			w.WriteHeader(http.StatusBadRequest)
			writeJSON(t, w, map[string]any{"error": "invalid_grant"})
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte("login required"))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configURL := server.URL + "/mcp"
	expired := time.Now().Add(-time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-old",
		ExpiresAtMillis: &expired,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: configURL, OAuthClientID: "client-1", Enabled: true}},
	}, CodexHome: home})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailToolsAndAuthOnly}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 {
		t.Fatalf("status = %#v", status)
	}
	if status.Data[0].AuthStatus != MCPAuthNotLoggedIn {
		t.Fatalf("AuthStatus = %q, want %q", status.Data[0].AuthStatus, MCPAuthNotLoggedIn)
	}
	if status.Data[0].State != MCPServerFailed {
		t.Fatalf("State = %q, want failed", status.Data[0].State)
	}
	if status.Data[0].FailureReason != nil {
		t.Fatalf("FailureReason = %#v, want nil after token deletion", status.Data[0].FailureReason)
	}
	if status.Data[0].Error == nil || !strings.Contains(*status.Data[0].Error, "is not logged in") {
		t.Fatalf("Error = %#v, want not-logged-in message", status.Data[0].Error)
	}
}

func TestHTTPMCPReadsSSERPCResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		if request.Method == "initialize" {
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
		}
		if request.Method == "notifications/initialized" {
			w.WriteHeader(http.StatusAccepted)
			return
		}
		if request.Method == "tools/list" && r.Header.Get(mcpHTTPSessionIDHeader) != "session-1" {
			t.Fatalf("tools/list missing MCP session header: %q", r.Header.Get(mcpHTTPSessionIDHeader))
		}
		result := map[string]any{
			"protocolVersion": defaultMCPProtocol,
			"capabilities":    map[string]any{},
			"serverInfo":      map[string]string{"name": "helper", "version": "test"},
		}
		if request.Method == "tools/list" {
			result = map[string]any{"tools": []map[string]any{{"name": "echo"}}}
		}
		payload, err := json.Marshal(map[string]any{
			"jsonrpc": "2.0",
			"id":      request.ID,
			"result":  result,
		})
		if err != nil {
			t.Fatalf("Marshal SSE payload returned error: %v", err)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte(": keepalive\n"))
		_, _ = w.Write([]byte("event: message\n"))
		_, _ = w.Write([]byte("data: " + string(payload) + "\n\n"))
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || len(status.Data[0].Tools) != 1 || status.Data[0].Tools[0].Name != "echo" {
		t.Fatalf("status = %#v", status)
	}
}

func TestHTTPMCPRequiredServerStatusFailureBlocksToolCall(t *testing.T) {
	var failInventory atomic.Bool
	failInventory.Store(true)
	var toolCalls atomic.Int64
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
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			if failInventory.Load() {
				writeHTTPMCPError(t, w, request.ID, -32001, "inventory down")
				return
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{"name": "echo"}}})
		case "resources/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []any{}})
		case "resources/templates/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		case "tools/call":
			toolCalls.Add(1)
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"content": []map[string]string{{"type": "text", "text": "ok"}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true, Required: true}},
	}})
	status, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}})
	if err != nil {
		t.Fatalf("ListStatusChecked() error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].State != MCPServerFailed || status.Data[0].Error == nil || !strings.Contains(*status.Data[0].Error, "inventory down") {
		t.Fatalf("failed status = %#v", status.Data)
	}
	if _, err := service.CallTool(&MCPToolCallParams{ServerName: "http", ToolName: "echo"}); !errors.Is(err, ErrInvalidMCPRequest) || !strings.Contains(err.Error(), "inventory down") {
		t.Fatalf("CallTool(failed required server) error = %v", err)
	}
	if toolCalls.Load() != 0 {
		t.Fatalf("toolCalls = %d, want required gate to block before tools/call", toolCalls.Load())
	}

	failInventory.Store(false)
	status, err = service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}})
	if err != nil {
		t.Fatalf("ListStatusChecked(recovered) error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].State != MCPServerReady || status.Data[0].Error != nil {
		t.Fatalf("recovered status = %#v", status.Data)
	}
	if _, err := service.CallTool(&MCPToolCallParams{ServerName: "http", ToolName: "echo"}); err != nil {
		t.Fatalf("CallTool(recovered required server) error = %v", err)
	}
	if toolCalls.Load() != 1 {
		t.Fatalf("toolCalls = %d, want recovered call to reach server", toolCalls.Load())
	}

	failInventory.Store(true)
	status, err = service.ListStatusChecked(&MCPListServerStatusParams{Detail: &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}})
	if err != nil {
		t.Fatalf("ListStatusChecked(failed again) error = %v", err)
	}
	if len(status.Data) != 1 || status.Data[0].State != MCPServerFailed || len(status.Data[0].Tools) != 0 {
		t.Fatalf("failed-again status = %#v, want failed without stale tools", status.Data)
	}
	if _, err := service.CallTool(&MCPToolCallParams{ServerName: "http", ToolName: "echo"}); !errors.Is(err, ErrInvalidMCPRequest) || !strings.Contains(err.Error(), "inventory down") {
		t.Fatalf("CallTool(failed-again required server) error = %v", err)
	}
	if toolCalls.Load() != 1 {
		t.Fatalf("toolCalls = %d, want failed status to block stale tool call", toolCalls.Load())
	}
}

func TestHTTPMCPHandlesSSEElicitationRequest(t *testing.T) {
	clientResponses := make(chan map[string]any, 1)
	progressEvents := make(chan *MCPProgressNotification, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var raw map[string]any
		if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		method, _ := raw["method"].(string)
		id, _ := raw["id"].(float64)
		switch method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, int64(id), map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			w.Header().Set("Content-Type", "text/event-stream")
			progressPayload, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"method":  "notifications/progress",
				"params": map[string]any{
					"progressToken": "token-1",
					"progress":      float64(1),
					"total":         float64(2),
					"message":       "Working",
				},
			})
			if err != nil {
				t.Fatalf("Marshal progress payload error = %v", err)
			}
			_, _ = w.Write([]byte("data: " + string(progressPayload) + "\n\n"))
			elicitationPayload, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      "elicitation-1",
				"method":  "elicitation/create",
				"params": map[string]any{
					"message":         "Approve?",
					"requestedSchema": map[string]any{"type": "object"},
				},
			})
			if err != nil {
				t.Fatalf("Marshal elicitation payload error = %v", err)
			}
			_, _ = w.Write([]byte("data: " + string(elicitationPayload) + "\n\n"))
			if flusher, ok := w.(http.Flusher); ok {
				flusher.Flush()
			}
			select {
			case response := <-clientResponses:
				result, ok := response["result"].(map[string]any)
				if !ok || result["action"] != "accept" {
					t.Fatalf("client response = %#v", response)
				}
			case <-time.After(time.Second):
				t.Fatalf("timed out waiting for client response")
			}
			finalPayload, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"id":      int64(id),
				"result": map[string]any{
					"content": []map[string]string{{"type": "text", "text": "done"}},
				},
			})
			if err != nil {
				t.Fatalf("Marshal final payload error = %v", err)
			}
			_, _ = w.Write([]byte("data: " + string(finalPayload) + "\n\n"))
		case "":
			clientResponses <- raw
			w.WriteHeader(http.StatusAccepted)
		default:
			writeHTTPMCPError(t, w, int64(id), -32601, "not found")
		}
	}))
	defer server.Close()

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"http": {Config: ServerConfig{URL: server.URL, Enabled: true}},
	}})
	service.SetElicitationHandler(MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		if request.ServerName != "http" || request.ThreadID != "thread-live" || request.TurnID != "turn-live" || request.Message != "Approve?" {
			t.Fatalf("elicitation request = %#v", request)
		}
		return &MCPElicitationResponse{Action: MCPElicitationActionAccept}, nil
	}))
	service.SetProgressHandler(MCPProgressHandlerFunc(func(ctx context.Context, notification *MCPProgressNotification) {
		progressEvents <- notification
	}))
	call, err := service.CallTool(&MCPToolCallParams{
		ThreadID: "thread-live",
		TurnID:   "turn-live",
		ItemID:   "item-live",
		Server:   "http",
		Tool:     "echo",
	})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(call.Content) != 1 || call.Content[0].Text != "done" {
		t.Fatalf("call = %#v", call)
	}
	select {
	case notification := <-progressEvents:
		if notification.ServerName != "http" || notification.ThreadID != "thread-live" || notification.TurnID != "turn-live" || notification.ItemID != "item-live" || notification.Message != "Working" || notification.ProgressToken != "token-1" {
			t.Fatalf("progress notification = %#v", notification)
		}
	case <-time.After(time.Second):
		t.Fatalf("timed out waiting for progress notification")
	}
}

func TestHTTPMCPRefreshesOAuthTokenForSSEClientResponse(t *testing.T) {
	var sawRefresh bool
	var clientResponseAuthHeaders []string
	clientResponses := make(chan map[string]any, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/.well-known/oauth-authorization-server/mcp":
			writeJSON(t, w, map[string]any{
				"authorization_endpoint": "https://issuer.example.test/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
			})
		case r.Method == http.MethodPost && r.URL.Path == "/token":
			sawRefresh = true
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			if r.Form.Get("grant_type") != "refresh_token" || r.Form.Get("refresh_token") != "refresh-1" {
				t.Fatalf("refresh form = %#v", r.Form)
			}
			writeJSON(t, w, map[string]any{
				"access_token":  "oauth-new",
				"refresh_token": "refresh-2",
				"token_type":    "Bearer",
				"expires_in":    3600,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/mcp":
			var raw map[string]any
			if err := json.NewDecoder(r.Body).Decode(&raw); err != nil {
				t.Fatalf("Decode() error = %v", err)
			}
			method, _ := raw["method"].(string)
			id, _ := raw["id"].(float64)
			switch method {
			case "initialize":
				w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
				writeHTTPMCPResponse(t, w, int64(id), map[string]any{
					"protocolVersion": defaultMCPProtocol,
					"capabilities":    map[string]any{},
					"serverInfo":      map[string]string{"name": "helper", "version": "test"},
				})
			case "notifications/initialized":
				w.WriteHeader(http.StatusAccepted)
			case "tools/call":
				w.Header().Set("Content-Type", "text/event-stream")
				elicitationPayload, err := json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      "elicitation-1",
					"method":  "elicitation/create",
					"params":  map[string]any{"message": "Approve?"},
				})
				if err != nil {
					t.Fatalf("Marshal elicitation payload error = %v", err)
				}
				_, _ = w.Write([]byte("data: " + string(elicitationPayload) + "\n\n"))
				if flusher, ok := w.(http.Flusher); ok {
					flusher.Flush()
				}
				select {
				case response := <-clientResponses:
					result, ok := response["result"].(map[string]any)
					if !ok || result["action"] != "accept" {
						t.Fatalf("client response = %#v", response)
					}
				case <-time.After(time.Second):
					t.Fatalf("timed out waiting for refreshed client response")
				}
				finalPayload, err := json.Marshal(map[string]any{
					"jsonrpc": "2.0",
					"id":      int64(id),
					"result": map[string]any{
						"content": []map[string]string{{"type": "text", "text": "done"}},
					},
				})
				if err != nil {
					t.Fatalf("Marshal final payload error = %v", err)
				}
				_, _ = w.Write([]byte("data: " + string(finalPayload) + "\n\n"))
			case "":
				clientResponseAuthHeaders = append(clientResponseAuthHeaders, r.Header.Get("Authorization"))
				if r.Header.Get("Authorization") == "Bearer oauth-old" {
					w.WriteHeader(http.StatusUnauthorized)
					_, _ = w.Write([]byte("stale client response token"))
					return
				}
				clientResponses <- raw
				w.WriteHeader(http.StatusAccepted)
			default:
				writeHTTPMCPError(t, w, int64(id), -32601, "not found")
			}
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	home := t.TempDir()
	configURL := server.URL + "/mcp"
	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	if err := NewOAuthStore(home).Save(&OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       configURL,
		ClientID:        "client-1",
		AccessToken:     "oauth-old",
		RefreshToken:    "refresh-1",
		ExpiresAtMillis: &expiresAt,
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{URL: configURL, OAuthClientID: "client-1", Enabled: true}},
	}, CodexHome: home})
	service.SetElicitationHandler(MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
		return &MCPElicitationResponse{Action: MCPElicitationActionAccept}, nil
	}))
	call, err := service.CallTool(&MCPToolCallParams{Server: "docs", Tool: "echo"})
	if err != nil {
		t.Fatalf("CallTool() error = %v", err)
	}
	if len(call.Content) != 1 || call.Content[0].Text != "done" {
		t.Fatalf("call = %#v", call)
	}
	if !sawRefresh {
		t.Fatalf("refresh endpoint was not called")
	}
	if strings.Join(clientResponseAuthHeaders, ",") != "Bearer oauth-old,Bearer oauth-new" {
		t.Fatalf("client response Authorization headers = %#v", clientResponseAuthHeaders)
	}
	loaded, err := NewOAuthStore(home).Load("docs", configURL)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.AccessToken != "oauth-new" || loaded.RefreshToken != "refresh-2" {
		t.Fatalf("loaded tokens = %#v", loaded)
	}
}

func TestMCPOAuthLoginUsesStreamableHTTPURL(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"docs": {Config: ServerConfig{
			URL:           "https://mcp.example.test/mcp",
			OAuthClientID: "client-1",
			OAuthResource: "resource-1",
			Enabled:       true,
		}},
	}})
	response, err := service.OauthLogin(&MCPServerOauthLoginParams{Name: "docs", Scopes: []string{"read", "write"}})
	if err != nil {
		t.Fatalf("OauthLogin() error = %v", err)
	}
	for _, want := range []string{"https://mcp.example.test/mcp/oauth/authorize", "client_id=client-1", "resource=resource-1", "scope=read+write"} {
		if !strings.Contains(response.AuthorizationURL, want) {
			t.Fatalf("AuthorizationURL missing %q: %s", want, response.AuthorizationURL)
		}
	}
}

func TestHTTPMCPIncludesErrorBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte("short and stout"))
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	err := client.Call("tools/list", map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "418 I'm a teapot") || !strings.Contains(err.Error(), "short and stout") {
		t.Fatalf("Call() error = %v", err)
	}
}

func TestHTTPMCPInitializeRetriesRustTransientStatuses(t *testing.T) {
	var initializeCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			if initializeCount.Add(1) == 1 {
				http.Error(w, "temporary", http.StatusBadGateway)
				return
			}
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	var delays []time.Duration
	client.retrySleep = func(delay time.Duration) { delays = append(delays, delay) }
	if err := client.Call("tools/list", map[string]any{}, nil); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if initializeCount.Load() != 2 {
		t.Fatalf("initialize attempts = %d, want 2", initializeCount.Load())
	}
	if len(delays) != 1 || delays[0] != 250*time.Millisecond {
		t.Fatalf("retry delays = %v, want [250ms]", delays)
	}
}

func TestHTTPMCPInitializeDoesNotRetryForbidden(t *testing.T) {
	var initializeCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		initializeCount.Add(1)
		http.Error(w, "forbidden", http.StatusForbidden)
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	var delays []time.Duration
	client.retrySleep = func(delay time.Duration) { delays = append(delays, delay) }
	err := client.Call("tools/list", map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "403 Forbidden") {
		t.Fatalf("Call() error = %v, want 403", err)
	}
	if initializeCount.Load() != 1 {
		t.Fatalf("initialize attempts = %d, want 1", initializeCount.Load())
	}
	if len(delays) != 0 {
		t.Fatalf("retry delays = %v, want none", delays)
	}
}

func TestHTTPMCPInitializedNotificationRetriesWholeHandshake(t *testing.T) {
	var initializeCount atomic.Int64
	var notificationCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			attempt := initializeCount.Add(1)
			w.Header().Set(mcpHTTPSessionIDHeader, fmt.Sprintf("session-%d", attempt))
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			if notificationCount.Add(1) == 1 {
				http.Error(w, "temporary", http.StatusServiceUnavailable)
				return
			}
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	client.retrySleep = func(time.Duration) {}
	if err := client.Call("tools/list", map[string]any{}, nil); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if initializeCount.Load() != 2 || notificationCount.Load() != 2 {
		t.Fatalf("initialize attempts = %d notifications = %d, want 2 each", initializeCount.Load(), notificationCount.Load())
	}
}

func TestHTTPMCPToolsListRetriesTransientFailures(t *testing.T) {
	var toolsListCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			switch toolsListCount.Add(1) {
			case 1:
				http.Error(w, "temporary", http.StatusInternalServerError)
			case 2:
				http.Error(w, "temporary", http.StatusBadGateway)
			default:
				writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
			}
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	var delays []time.Duration
	client.retrySleep = func(delay time.Duration) { delays = append(delays, delay) }
	if err := client.Call("tools/list", map[string]any{}, nil); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if toolsListCount.Load() != 3 {
		t.Fatalf("tools/list attempts = %d, want 3", toolsListCount.Load())
	}
	wantDelays := []time.Duration{250 * time.Millisecond, time.Second}
	if len(delays) != len(wantDelays) || delays[0] != wantDelays[0] || delays[1] != wantDelays[1] {
		t.Fatalf("retry delays = %v, want %v", delays, wantDelays)
	}
}

func TestHTTPMCPToolsCallDoesNotRetryTransientFailure(t *testing.T) {
	var toolsCallCount atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/call":
			toolsCallCount.Add(1)
			http.Error(w, "temporary", http.StatusInternalServerError)
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	var delays []time.Duration
	client.retrySleep = func(delay time.Duration) { delays = append(delays, delay) }
	err := client.Call("tools/call", map[string]any{"name": "echo"}, nil)
	if err == nil || !strings.Contains(err.Error(), "500 Internal Server Error") {
		t.Fatalf("Call() error = %v, want 500", err)
	}
	if toolsCallCount.Load() != 1 || len(delays) != 0 {
		t.Fatalf("tools/call attempts = %d retry delays = %v, want one attempt", toolsCallCount.Load(), delays)
	}
}

func TestRetryableMCPStreamableHTTPErrorMatchesRustClassifier(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "request timeout", err: &mcpHTTPStatusError{StatusCode: http.StatusRequestTimeout}, want: true},
		{name: "too many requests", err: &mcpHTTPStatusError{StatusCode: http.StatusTooManyRequests}, want: true},
		{name: "internal server error", err: &mcpHTTPStatusError{StatusCode: http.StatusInternalServerError}, want: true},
		{name: "bad gateway", err: &mcpHTTPStatusError{StatusCode: http.StatusBadGateway}, want: true},
		{name: "service unavailable", err: &mcpHTTPStatusError{StatusCode: http.StatusServiceUnavailable}, want: true},
		{name: "gateway timeout", err: &mcpHTTPStatusError{StatusCode: http.StatusGatewayTimeout}, want: true},
		{name: "forbidden", err: &mcpHTTPStatusError{StatusCode: http.StatusForbidden}, want: false},
		{name: "session expired", err: &mcpHTTPStatusError{StatusCode: http.StatusNotFound}, want: false},
		{name: "transport rpc error", err: &MCPRemoteError{Code: -32603, Message: "http/request failed: reset"}, want: true},
		{name: "other rpc error", err: &MCPRemoteError{Code: -32603, Message: "internal failure"}, want: false},
		{name: "network error", err: &net.DNSError{Err: "temporary", Name: "mcp.test"}, want: true},
		{name: "truncated stream", err: io.ErrUnexpectedEOF, want: true},
		{name: "clean eof", err: io.EOF, want: false},
		{name: "decode error", err: errors.New("invalid character"), want: false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := isRetryableMCPStreamableHTTPError(test.err); got != test.want {
				t.Fatalf("isRetryableMCPStreamableHTTPError(%v) = %v, want %v", test.err, got, test.want)
			}
		})
	}
}

func TestHTTPMCPRemoteErrorIsStructured(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-1")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "helper", "version": "test"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			writeHTTPMCPError(t, w, request.ID, -32001, "remote blew up")
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true})
	err := client.Call("resources/read", map[string]any{"uri": "file://missing"}, nil)
	var remoteErr *MCPRemoteError
	if !errors.As(err, &remoteErr) {
		t.Fatalf("Call() error = %v, want MCPRemoteError", err)
	}
	if remoteErr.Method != "resources/read" || remoteErr.Code != -32001 || remoteErr.Message != "remote blew up" {
		t.Fatalf("remote error = %#v", remoteErr)
	}
}

func mcpTestCursorParam(t *testing.T, params json.RawMessage) string {
	t.Helper()
	var raw map[string]any
	if len(params) == 0 {
		return ""
	}
	if err := json.Unmarshal(params, &raw); err != nil {
		t.Fatalf("Unmarshal params returned error: %v", err)
	}
	cursor, _ := raw["cursor"].(string)
	return cursor
}

func writeHTTPMCPResponse(t *testing.T, w http.ResponseWriter, id int64, result any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "result": result}); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}

func writeHTTPMCPError(t *testing.T, w http.ResponseWriter, id int64, code int64, message string) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]any{"jsonrpc": "2.0", "id": id, "error": map[string]any{"code": code, "message": message}}); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}

func TestHTTPMCPHandshakeDeadlineBoundsInitializeAndClearsAfterwards(t *testing.T) {
	// A stalled handshake must not block beyond the remaining initialization
	// deadline (Rust e244a9d94e, #37168).
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		// Never respond: the handshake must be bounded by the deadline instead
		// of hanging on the stalled endpoint. Block until the request context is
		// cancelled by the bounded handshake.
		<-r.Context().Done()
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, Enabled: true, StartupTimeout: 80 * time.Millisecond})
	started := time.Now()
	err := client.Call("tools/list", map[string]any{}, nil)
	if err == nil {
		t.Fatalf("stalled handshake should fail, err = nil")
	}
	quick := time.Since(started)
	if quick < 20*time.Millisecond {
		t.Fatalf("stalled handshake returned too quickly (%v) without a real request attempt; err = %v", quick, err)
	}
	elapsed := time.Since(started)
	if elapsed > 3*time.Second {
		t.Fatalf("stalled handshake took %v, want bounded by the deadline and retries", elapsed)
	}

	// The deadline is cleared after the handshake finishes, so a subsequent
	// client with a responsive endpoint is not affected by a stale deadline.
	okServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		switch request.Method {
		case "initialize":
			w.Header().Set(mcpHTTPSessionIDHeader, "session-ok")
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities":    map[string]any{},
				"serverInfo":      map[string]string{"name": "ok", "version": "test"},
			})
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer okServer.Close()
	okClient := newMCPHTTPClient(&ServerConfig{URL: okServer.URL, Enabled: true, StartupTimeout: 80 * time.Millisecond})
	if err := okClient.Call("tools/list", map[string]any{}, nil); err != nil {
		t.Fatalf("responsive handshake Call() error = %v", err)
	}
	if timeout := okClient.handshakeRequestTimeout("initialize"); timeout != 0 {
		t.Fatalf("handshake deadline should be cleared after success, timeout = %v", timeout)
	}
}
