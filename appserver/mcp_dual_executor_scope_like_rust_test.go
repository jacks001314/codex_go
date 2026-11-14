package appserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"codex_go/mcp"
)

func startDualExecutorMCPServer(t *testing.T, origin string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodDelete {
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		var rpc struct {
			ID     int64  `json:"id"`
			Method string `json:"method"`
		}
		if err := json.NewDecoder(request.Body).Decode(&rpc); err != nil {
			t.Errorf("decode MCP request: %v", err)
			return
		}
		writeResult := func(result any) {
			t.Helper()
			writer.Header().Set("Content-Type", "application/json")
			if err := json.NewEncoder(writer).Encode(map[string]any{"jsonrpc": "2.0", "id": rpc.ID, "result": result}); err != nil {
				t.Errorf("encode MCP response: %v", err)
			}
		}
		switch rpc.Method {
		case "initialize":
			writeResult(map[string]any{
				"protocolVersion": "2025-06-18",
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]string{"name": "notes", "version": "1"},
			})
		case "notifications/initialized":
			writer.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeResult(map[string]any{"tools": []map[string]any{{
				"name": "lookup", "description": "lookup", "inputSchema": map[string]any{"type": "object"},
			}}})
		case "tools/call":
			writeResult(map[string]any{"content": []map[string]any{{"type": "text", "text": origin}}})
		default:
			writer.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(writer).Encode(map[string]any{
				"jsonrpc": "2.0", "id": rpc.ID,
				"error": map[string]any{"code": -32601, "message": "not found"},
			})
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// Two independently connected executors can register the same MCP server and
// tool name. Calls with an explicit environment must travel through that
// executor's HTTP route and never reuse the other executor's connection.
func TestDualExecutorSameNamedMCPServerStaysScopedLikeRust(t *testing.T) {
	first := startDualExecutorMCPServer(t, "http://first.example")
	second := startDualExecutorMCPServer(t, "http://second.example")
	serverA, clientA := startExecServerTestClientForEnvironment(t, "executor-a")
	defer serverA.Close()
	defer clientA.Close()
	serverB, clientB := startExecServerTestClientForEnvironment(t, "executor-b")
	defer serverB.Close()
	defer clientB.Close()

	service := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"notes": {
			Name:              "notes",
			ContributionOrder: 0,
			Config: mcp.ServerConfig{
				URL: first.URL, EnvironmentID: "executor-a", Enabled: true,
				HTTPClient: executorHTTPDoer{client: clientA},
			},
		},
		mcp.ServerRegistrationKey("notes", "executor-b"): {
			Name:              "notes",
			ContributionOrder: 1,
			Config: mcp.ServerConfig{
				URL: second.URL, EnvironmentID: "executor-b", Enabled: true,
				HTTPClient: executorHTTPDoer{client: clientB},
			},
		},
	}})

	call := func(environmentID string) string {
		t.Helper()
		response, err := service.CallTool(&mcp.MCPToolCallParams{
			Server: "notes", Tool: "lookup", EnvironmentID: &environmentID,
		})
		if err != nil {
			t.Fatalf("CallTool(%s) error = %v", environmentID, err)
		}
		if len(response.Content) != 1 {
			t.Fatalf("CallTool(%s) content = %#v", environmentID, response.Content)
		}
		return response.Content[0].Text
	}
	if got := call("executor-a"); got != "http://first.example" {
		t.Fatalf("executor-a result = %q", got)
	}
	if got := call("executor-b"); got != "http://second.example" {
		t.Fatalf("executor-b result = %q", got)
	}

	listed, err := service.ListStatusChecked(&mcp.MCPListServerStatusParams{
		Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 2 {
		t.Fatalf("status entries = %#v, want both executors", listed.Data)
	}
	for index, want := range []struct{ environment, origin string }{
		{"executor-a", first.URL},
		{"executor-b", second.URL},
	} {
		status := listed.Data[index]
		if status.EnvironmentID == nil || *status.EnvironmentID != want.environment {
			t.Fatalf("status[%d].EnvironmentID = %v, want %q", index, status.EnvironmentID, want.environment)
		}
		if status.HTTPOrigin == nil || *status.HTTPOrigin != want.origin {
			t.Fatalf("status[%d].HTTPOrigin = %v, want %q", index, status.HTTPOrigin, want.origin)
		}
	}

	// Bind a fresh service to a closed executor route. Its call must fail rather
	// than falling back to the host HTTP client, while the other executor stays
	// usable. This proves the per-server route is actually exercised.
	closedServer, closedClient := startExecServerTestClientForEnvironment(t, "executor-closed")
	defer closedServer.Close()
	if err := closedClient.Close(); err != nil {
		t.Fatal(err)
	}
	scoped := mcp.NewMCPService(&mcp.RuntimeConfig{Servers: map[string]mcp.ServerRegistration{
		"notes": {
			Name: "notes", ContributionOrder: 0,
			Config: mcp.ServerConfig{
				URL: first.URL, EnvironmentID: "executor-closed", Enabled: true,
				HTTPClient: executorHTTPDoer{client: closedClient},
			},
		},
		mcp.ServerRegistrationKey("notes", "executor-b"): {
			Name: "notes", ContributionOrder: 1,
			Config: mcp.ServerConfig{
				URL: second.URL, EnvironmentID: "executor-b", Enabled: true,
				HTTPClient: executorHTTPDoer{client: clientB},
			},
		},
	}})
	closedEnvironment := "executor-closed"
	if _, err := scoped.CallTool(&mcp.MCPToolCallParams{Server: "notes", Tool: "lookup", EnvironmentID: &closedEnvironment}); err == nil {
		t.Fatal("closed executor route unexpectedly fell back to the host HTTP client")
	}
	liveEnvironment := "executor-b"
	if _, err := scoped.CallTool(&mcp.MCPToolCallParams{Server: "notes", Tool: "lookup", EnvironmentID: &liveEnvironment}); err != nil {
		t.Fatalf("live executor route failed after closed peer: %v", err)
	}
}
