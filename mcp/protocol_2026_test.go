package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestRuntimeConfigEnablesMCP20260728OnlyWhenRequested(t *testing.T) {
	legacy := RuntimeConfigFromValues(map[string]any{}, "")
	if legacy.ProtocolMode != MCPProtocolLegacy {
		t.Fatalf("default protocol mode = %d, want legacy", legacy.ProtocolMode)
	}
	modern := RuntimeConfigFromValues(map[string]any{
		"features": map[string]any{"mcp_2026_07_28": true},
		"mcp_servers": map[string]any{
			"docs": map[string]any{"url": "https://example.test/mcp"},
		},
	}, "")
	if modern.ProtocolMode != MCPProtocol20260728 {
		t.Fatalf("enabled protocol mode = %d, want 2026-07-28", modern.ProtocolMode)
	}
	service := NewMCPService(modern)
	config, ok := service.serverConfig("docs")
	if !ok || config.ProtocolMode != MCPProtocol20260728 {
		t.Fatalf("service config = %#v, want modern protocol", config)
	}
}

func TestMCP2026HTTPDiscoveryMetadataAndMultiRoundInput(t *testing.T) {
	methods := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64          `json:"id"`
			Method string         `json:"method"`
			Params map[string]any `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		methods = append(methods, request.Method)
		if got := r.Header.Get(mcpHTTPProtocolVersionHeader); got != modernMCPProtocol {
			t.Fatalf("protocol header = %q, want %q", got, modernMCPProtocol)
		}
		meta, _ := request.Params["_meta"].(map[string]any)
		if meta[mcpProtocolVersionMetadataKey] != modernMCPProtocol {
			t.Fatalf("protocol metadata = %#v", meta)
		}
		clientInfo, _ := meta[mcpClientInfoMetadataKey].(map[string]any)
		if clientInfo["name"] != "codex-go" {
			t.Fatalf("client metadata = %#v", clientInfo)
		}
		switch request.Method {
		case "server/discover":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{modernMCPProtocol},
				"capabilities":      map[string]any{"tools": map[string]any{}},
				"serverInfo":        map[string]any{"name": "modern", "version": "1"},
			})
		case "tools/call":
			if meta["caller"] != "preserved" {
				t.Fatalf("caller metadata was not preserved: %#v", meta)
			}
			if _, ok := request.Params["inputResponses"]; !ok {
				writeHTTPMCPResponse(t, w, request.ID, map[string]any{
					"resultType": "input_required",
					"inputRequests": map[string]any{
						"confirmation": map[string]any{
							"method": "elicitation/create",
							"params": map[string]any{
								"message":         "Confirm",
								"requestedSchema": map[string]any{"type": "object"},
							},
						},
					},
					"requestState": "opaque-state",
				})
				return
			}
			if request.Params["requestState"] != "opaque-state" {
				t.Fatalf("requestState = %#v", request.Params["requestState"])
			}
			responses, _ := request.Params["inputResponses"].(map[string]any)
			confirmation, _ := responses["confirmation"].(map[string]any)
			responseMeta, _ := confirmation["_meta"].(map[string]any)
			content, _ := confirmation["content"].(map[string]any)
			if confirmation["action"] != "accept" || content["confirmed"] != true || responseMeta["handled"] != true {
				t.Fatalf("input response = %#v", confirmation)
			}
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"resultType": "complete",
				"content":    []any{map[string]any{"type": "text", "text": "done"}},
			})
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, ProtocolMode: MCPProtocol20260728})
	var result MCPToolCallResponse
	err := client.CallWithOptions(&httpClientCallOptions{
		ServerName: "modern",
		Elicitation: MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			if request.Message != "Confirm" {
				t.Fatalf("elicitation request = %#v", request)
			}
			return &MCPElicitationResponse{
				Action:  MCPElicitationActionAccept,
				Content: map[string]any{"confirmed": true},
				Meta:    map[string]any{"handled": true},
			}, nil
		}),
	}, "tools/call", map[string]any{
		"name":      "confirm",
		"arguments": map[string]any{},
		"_meta":     map[string]any{"caller": "preserved"},
	}, &result)
	if err != nil {
		t.Fatalf("CallWithOptions() error = %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "done" {
		t.Fatalf("result = %#v", result)
	}
	if got := strings.Join(methods, ","); got != "server/discover,tools/call,tools/call" {
		t.Fatalf("methods = %q", got)
	}
}

func TestMCP2026HTTPFallsBackOnlyFromCorrelatedMethodNotFound(t *testing.T) {
	methods := []string{}
	headers := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Fatalf("Decode() error = %v", err)
		}
		methods = append(methods, request.Method)
		headers = append(headers, r.Header.Get(mcpHTTPProtocolVersionHeader))
		switch request.Method {
		case "server/discover":
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      request.ID,
				"error":   map[string]any{"code": mcpJSONRPCMethodNotFoundCode, "message": "method not found"},
			})
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"protocolVersion": defaultMCPProtocol, "capabilities": map[string]any{}})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []any{}})
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
	}))
	defer server.Close()

	client := newMCPHTTPClient(&ServerConfig{URL: server.URL, ProtocolMode: MCPProtocol20260728})
	var result struct {
		Tools []any `json:"tools"`
	}
	if err := client.Call("tools/list", map[string]any{}, &result); err != nil {
		t.Fatalf("Call() error = %v", err)
	}
	if got := strings.Join(methods, ","); got != "server/discover,initialize,notifications/initialized,tools/list" {
		t.Fatalf("methods = %q", got)
	}
	if got := strings.Join(headers, ","); got != modernMCPProtocol+","+defaultMCPProtocol+","+defaultMCPProtocol+","+defaultMCPProtocol {
		t.Fatalf("protocol headers = %q", got)
	}
}

func TestMCP2026StdioDiscoveryAndMultiRoundInput(t *testing.T) {
	if os.Getenv("MCP_STDIO_2026_HELPER") == "1" {
		runMCP2026StdioHelper(t)
		return
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	client := newMCPStdioClient(&ServerConfig{
		Command:      executable,
		Args:         []string{"-test.run=TestMCP2026StdioDiscoveryAndMultiRoundInput", "--"},
		ProtocolMode: MCPProtocol20260728,
		Env: map[string]string{
			"MCP_STDIO_2026_HELPER":  "1",
			mcpProtocolVersionEnvVar: modernMCPProtocol,
		},
	})
	defer client.Close()
	var result MCPToolCallResponse
	err = client.CallWithOptions(&stdioCallOptions{
		ServerName: "stdio-modern",
		Elicitation: MCPElicitationHandlerFunc(func(ctx context.Context, request *MCPElicitationRequest) (*MCPElicitationResponse, error) {
			return &MCPElicitationResponse{Action: MCPElicitationActionAccept, Content: map[string]any{"approved": true}}, nil
		}),
	}, "tools/call", map[string]any{"name": "confirm", "arguments": map[string]any{}}, &result)
	if err != nil {
		t.Fatalf("CallWithOptions() error = %v", err)
	}
	if len(result.Content) != 1 || result.Content[0].Text != "stdio-done" {
		t.Fatalf("result = %#v", result)
	}
}

func runMCP2026StdioHelper(t *testing.T) {
	t.Helper()
	if _, ok := os.LookupEnv(mcpProtocolVersionEnvVar); ok {
		t.Fatalf("%s leaked into child process", mcpProtocolVersionEnvVar)
	}
	reader := bufio.NewReader(os.Stdin)
	for {
		data, err := readMCPFrame(reader)
		if err != nil {
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params map[string]any  `json:"params"`
		}
		if json.Unmarshal(data, &request) != nil || request.Method == "" {
			continue
		}
		meta, _ := request.Params["_meta"].(map[string]any)
		if meta[mcpProtocolVersionMetadataKey] != modernMCPProtocol {
			t.Fatalf("protocol metadata = %#v", meta)
		}
		var result any
		switch request.Method {
		case "server/discover":
			result = map[string]any{
				"resultType":        "complete",
				"supportedVersions": []string{modernMCPProtocol},
				"capabilities":      map[string]any{"tools": map[string]any{}},
			}
		case "tools/call":
			if _, ok := request.Params["inputResponses"]; !ok {
				result = map[string]any{
					"resultType":   "input_required",
					"requestState": "stdio-state",
					"inputRequests": map[string]any{
						"approval": map[string]any{
							"method": "elicitation/create",
							"params": map[string]any{"message": "Approve"},
						},
					},
				}
			} else {
				responses, _ := request.Params["inputResponses"].(map[string]any)
				approval, _ := responses["approval"].(map[string]any)
				content, _ := approval["content"].(map[string]any)
				if request.Params["requestState"] != "stdio-state" || approval["action"] != "accept" || content["approved"] != true {
					t.Fatalf("stdio MRTR params = %#v", request.Params)
				}
				result = map[string]any{"resultType": "complete", "content": []any{map[string]any{"type": "text", "text": "stdio-done"}}}
			}
		default:
			t.Fatalf("unexpected method %q", request.Method)
		}
		if err := writeMCPFrame(os.Stdout, map[string]any{"jsonrpc": "2.0", "id": request.ID, "result": result}); err != nil {
			t.Fatalf("write response error = %v", err)
		}
	}
}

func TestMCP2026MessageLimitsAndStdioOptIn(t *testing.T) {
	modern, env, strip, err := mcpStdioLaunchConfig(&ServerConfig{
		ProtocolMode: MCPProtocol20260728,
		Env:          map[string]string{mcpProtocolVersionEnvVar: modernMCPProtocol, "KEEP": "yes"},
	})
	if err != nil || modern != MCPProtocol20260728 || !strip || env["KEEP"] != "yes" {
		t.Fatalf("modern stdio launch = mode %d env %#v strip %t err %v", modern, env, strip, err)
	}
	if _, ok := env[mcpProtocolVersionEnvVar]; ok {
		t.Fatalf("protocol marker was not removed: %#v", env)
	}
	legacy, _, _, err := mcpStdioLaunchConfig(&ServerConfig{ProtocolMode: MCPProtocol20260728})
	if err != nil || legacy != MCPProtocolLegacy {
		t.Fatalf("stdio without marker = mode %d err %v, want legacy", legacy, err)
	}
	if _, _, _, err := mcpStdioLaunchConfig(&ServerConfig{ProtocolMode: MCPProtocol20260728, Env: map[string]string{mcpProtocolVersionEnvVar: "1999-01-01"}}); err == nil {
		t.Fatal("unknown stdio protocol marker was accepted")
	}

	if _, err := readMCPFrameWithLimit(bufio.NewReader(strings.NewReader(`{"padding":"123456789"}`+"\n")), 8); err == nil {
		t.Fatal("oversized stdio message was accepted")
	}
	response := &http.Response{Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"jsonrpc":"2.0","id":1}`))}
	if _, err := readMCPHTTPRPCResponseWithHandlerAndLimit(response, 1, 8, nil); err == nil {
		t.Fatal("oversized HTTP JSON message was accepted")
	}
	if _, err := readMCPHTTPSSEWithLimit(strings.NewReader("data: {\"jsonrpc\":\"2.0\",\"id\":1}\n\n"), 1, 8, nil); err == nil {
		t.Fatal("oversized HTTP SSE event was accepted")
	}
}
