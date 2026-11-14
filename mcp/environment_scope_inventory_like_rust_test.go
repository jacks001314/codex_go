package mcp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// scopedInventoryTestServer is a minimal streamable-HTTP MCP server that
// identifies itself by origin so a test can tell two same-named registrations
// apart.
func scopedInventoryTestServer(t *testing.T, origin string) *httptest.Server {
	t.Helper()
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
			t.Errorf("Decode() error = %v", err)
			return
		}
		switch request.Method {
		case "initialize":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"protocolVersion": defaultMCPProtocol,
				"capabilities": map[string]any{
					"tools":     map[string]any{},
					"resources": map[string]any{},
				},
				"serverInfo": map[string]string{"name": "notes", "version": "1"},
			})
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
		case "tools/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"tools": []map[string]any{{
				"name":        "lookup",
				"description": "lookup",
				"inputSchema": map[string]any{"type": "object"},
			}}})
		case "tools/call":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{
				"content": []map[string]any{{"type": "text", "text": origin}},
			})
		case "resources/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resources": []map[string]any{{
				"uri": "memo://shared", "name": "shared", "mimeType": "text/plain",
			}}})
		case "resources/templates/list":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"resourceTemplates": []any{}})
		case "resources/read":
			writeHTTPMCPResponse(t, w, request.ID, map[string]any{"contents": []map[string]any{{
				"uri": "memo://shared", "mimeType": "text/plain", "text": origin,
			}}})
		default:
			writeHTTPMCPError(t, w, request.ID, -32601, "not found")
		}
	}))
	t.Cleanup(server.Close)
	return server
}

// TestSameNamedServerKeepsEnvironmentInventoryLikeRust mirrors Rust #61257: two
// environments may register the same server, tool, and resource names, and the
// inventory, status lookup, tool call, and resource read all stay scoped.
func TestSameNamedServerKeepsEnvironmentInventoryLikeRust(t *testing.T) {
	first := scopedInventoryTestServer(t, "http://first.example")
	second := scopedInventoryTestServer(t, "http://second.example")

	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"notes": {
			Name:              "notes",
			ContributionOrder: 0,
			Config:            ServerConfig{URL: first.URL, EnvironmentID: "env-a", Enabled: true},
		},
		ServerRegistrationKey("notes", "env-b"): {
			Name:              "notes",
			ContributionOrder: 1,
			Config:            ServerConfig{URL: second.URL, EnvironmentID: "env-b", Enabled: true},
		},
	}})

	full := &MCPServerStatusDetail{Mode: MCPServerStatusDetailFull}
	listed, err := service.ListStatusChecked(&MCPListServerStatusParams{Detail: full})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Data) != 2 {
		t.Fatalf("status entries = %#v, want both environments", listed.Data)
	}
	for index, want := range []struct{ environment, origin string }{
		{"env-a", first.URL},
		{"env-b", second.URL},
	} {
		status := listed.Data[index]
		if status.Name != "notes" {
			t.Fatalf("status[%d].Name = %q", index, status.Name)
		}
		if status.EnvironmentID == nil || *status.EnvironmentID != want.environment {
			t.Fatalf("status[%d].EnvironmentID = %v, want %q", index, status.EnvironmentID, want.environment)
		}
		if status.HTTPOrigin == nil || *status.HTTPOrigin != want.origin {
			t.Fatalf("status[%d].HTTPOrigin = %v, want %q", index, status.HTTPOrigin, want.origin)
		}
		names := make([]string, 0, len(status.Tools))
		for _, tool := range status.Tools {
			names = append(names, tool.Name)
		}
		if len(names) != 1 || names[0] != "lookup" {
			t.Fatalf("status[%d].Tools = %#v, want lookup", index, names)
		}
	}

	environmentB := "env-b"
	explicit, err := service.ListStatusChecked(&MCPListServerStatusParams{
		ServerName:    strPtrForEnvTest("notes"),
		EnvironmentID: &environmentB,
		Detail:        full,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(explicit.Data) != 1 || explicit.Data[0].EnvironmentID == nil || *explicit.Data[0].EnvironmentID != "env-b" {
		t.Fatalf("explicit env-b status = %#v", explicit.Data)
	}
	if explicit.Data[0].HTTPOrigin == nil || *explicit.Data[0].HTTPOrigin != second.URL {
		t.Fatalf("explicit env-b origin = %v, want %q", explicit.Data[0].HTTPOrigin, second.URL)
	}

	nameOnly, err := service.ListStatusChecked(&MCPListServerStatusParams{ServerName: strPtrForEnvTest("notes"), Detail: full})
	if err != nil {
		t.Fatal(err)
	}
	if len(nameOnly.Data) != 1 || nameOnly.Data[0].EnvironmentID == nil || *nameOnly.Data[0].EnvironmentID != "env-a" {
		t.Fatalf("name-only status = %#v, want the first registration", nameOnly.Data)
	}

	call := func(environmentID *string) string {
		t.Helper()
		response, err := service.CallTool(&MCPToolCallParams{Server: "notes", Tool: "lookup", EnvironmentID: environmentID})
		if err != nil {
			t.Fatalf("CallTool(%v) error = %v", environmentID, err)
		}
		if len(response.Content) != 1 {
			t.Fatalf("CallTool(%v) content = %#v", environmentID, response.Content)
		}
		return response.Content[0].Text
	}
	if got := call(&environmentB); got != "http://second.example" {
		t.Fatalf("env-b tool result = %q", got)
	}
	if got := call(nil); got != "http://first.example" {
		t.Fatalf("name-only tool result = %q", got)
	}

	response, err := service.ReadResource(&MCPResourceReadParams{Server: "notes", URI: "memo://shared", EnvironmentID: &environmentB})
	if err != nil {
		t.Fatalf("ReadResource(env-b) error = %v", err)
	}
	if len(response.Contents) != 1 || response.Contents[0].Text != "http://second.example" {
		t.Fatalf("env-b resource = %#v", response.Contents)
	}
	response, err = service.ReadResource(&MCPResourceReadParams{Server: "notes", URI: "memo://shared"})
	if err != nil {
		t.Fatalf("ReadResource(name-only) error = %v", err)
	}
	if len(response.Contents) != 1 || response.Contents[0].Text != "http://first.example" {
		t.Fatalf("name-only resource = %#v", response.Contents)
	}

	missing := "missing"
	if _, err := service.CallTool(&MCPToolCallParams{Server: "notes", Tool: "lookup", EnvironmentID: &missing}); err == nil ||
		!strings.Contains(err.Error(), "unknown MCP server") {
		t.Fatalf("unknown environment error = %v", err)
	}
}
